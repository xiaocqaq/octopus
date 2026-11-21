package relay

import (
	"context"
	"crypto/sha256"
	"errors"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/bestruirui/octopus/internal/model"
	"github.com/charmbracelet/log"
	"github.com/google/uuid"
	"github.com/looplj/axonhub/llm"
	"github.com/looplj/axonhub/llm/httpclient"
	"github.com/tidwall/gjson"
)

const (
	responseSessionIdleTTL  = 2 * time.Hour
	responseSessionCapacity = 10000
	sessionRequestLimitCode = "session_request_limit_exceeded"
)

var (
	errSessionRequestLimit     = errors.New(sessionRequestLimitCode)
	errResponseSessionCapacity = errors.New("Responses session mapping capacity reached; retry after idle sessions expire")
)

type responseSessionKey struct {
	apiKeyID, channelID, channelKeyID int
	header                            string
	clientID                          [sha256.Size]byte
}

type responseSessionEntry struct {
	upstreamID string
	attempts   int
	lastUsed   time.Time
}

// 单进程映射，空闲到期或重启后分配新的上游 ID。多副本需要共享的原子计数存储。
type responseSessionStore struct {
	mu        sync.Mutex
	entries   map[responseSessionKey]responseSessionEntry
	nextSweep time.Time
}

var responseSessions = responseSessionStore{entries: make(map[responseSessionKey]responseSessionEntry)}

// reserve 在出站前为本次尝试预留次数。针对过期代的并发拒绝复用已经换好的新 ID。
func (s *responseSessionStore) reserve(key responseSessionKey, maxRequests int, rejectedID string, now time.Time) string {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.entries == nil {
		s.entries = make(map[responseSessionKey]responseSessionEntry)
	}
	if !now.Before(s.nextSweep) || len(s.entries) >= responseSessionCapacity {
		for key, entry := range s.entries {
			if now.Sub(entry.lastUsed) >= responseSessionIdleTTL {
				delete(s.entries, key)
			}
		}
		s.nextSweep = now.Add(time.Minute)
	}
	entry, ok := s.entries[key]
	if !ok && len(s.entries) >= responseSessionCapacity {
		return ""
	}
	if !ok || now.Sub(entry.lastUsed) >= responseSessionIdleTTL || entry.attempts >= maxRequests || entry.upstreamID == rejectedID {
		entry = responseSessionEntry{upstreamID: uuid.NewString()}
	}
	entry.attempts++
	entry.lastUsed = now
	s.entries[key] = entry
	return entry.upstreamID
}

// responseSessionIdentity 只接管自包含请求，带上游状态引用的请求沿用原有透传语义。
func responseSessionIdentity(format llm.APIFormat, raw, request *httpclient.Request, channel model.Channel, apiKeyID, channelKeyID int) (responseSessionKey, bool) {
	header := channel.SessionIDHeader
	if raw == nil || request == nil || format != llm.APIFormatOpenAIResponse || apiKeyID <= 0 || channel.ID <= 0 || channelKeyID <= 0 || (header != "session-id" && header != "session_id") {
		return responseSessionKey{}, false
	}
	alternate := "session_id"
	if header == alternate {
		alternate = "session-id"
	}
	if len(raw.Headers.Values(alternate)) != 0 || len(request.Headers.Values(alternate)) != 0 {
		return responseSessionKey{}, false // 双别名存在时不能确定上游采用哪一个。
	}
	values := raw.Headers.Values(header)
	if len(values) != 1 || len(request.Headers.Values(header)) != 1 || request.Headers.Get(header) != values[0] {
		return responseSessionKey{}, false // 尊重渠道设置的自定义 Header。
	}
	id := strings.TrimSpace(values[0])
	if id == "" || strings.Contains(id, ",") {
		return responseSessionKey{}, false
	}
	for _, body := range [][]byte{raw.Body, request.Body} {
		if !gjson.ValidBytes(body) {
			return responseSessionKey{}, false
		}
		payload := gjson.ParseBytes(body)
		if !payload.IsObject() {
			return responseSessionKey{}, false
		}
		if payload.Get("previous_response_id").Type != gjson.Null || payload.Get("conversation").Type != gjson.Null {
			return responseSessionKey{}, false
		}
		for _, item := range payload.Get("input").Array() {
			if item.Get("type").String() == "item_reference" {
				return responseSessionKey{}, false
			}
		}
	}
	return responseSessionKey{apiKeyID, channel.ID, channelKeyID, header, sha256.Sum256([]byte(id))}, true
}

func hasResponseSessionLimitCode(status int, body []byte) bool {
	return status == http.StatusBadRequest && gjson.ValidBytes(body) && gjson.GetBytes(body, "error.code").Str == sessionRequestLimitCode
}

func isResponseSessionLimitError(err error) bool {
	var upstream *httpclient.Error
	return errors.Is(err, errSessionRequestLimit) || (errors.As(err, &upstream) && hasResponseSessionLimitCode(upstream.StatusCode, upstream.Body))
}

// sendWithResponsesSession wraps only the pre-commit send, so an accepted stream is never replayed.
func (s *responseSessionStore) sendWithResponsesSession(ctx context.Context, format llm.APIFormat, raw, request *httpclient.Request, channel model.Channel, apiKeyID, channelKeyID int, retryUsed *bool, send func() (*upstreamResponse, error)) (*upstreamResponse, error) {
	key, ok := responseSessionIdentity(format, raw, request, channel, apiKeyID, channelKeyID)
	if ctx.Err() != nil {
		return nil, ctx.Err()
	}
	if !ok || retryUsed == nil {
		return send()
	}
	maxRequests := channel.SessionMaxRequests
	if maxRequests < 1 || maxRequests > 99 {
		maxRequests = model.DefaultSessionMaxRequests
	}
	id := s.reserve(key, maxRequests, "", time.Now())
	if id == "" {
		return nil, errResponseSessionCapacity
	}
	request.Headers.Set(key.header, id)
	result, err := send()
	if *retryUsed || ctx.Err() != nil || !isResponseSessionLimitError(err) {
		return result, err
	}
	next := s.reserve(key, maxRequests, id, time.Now())
	if next == "" {
		return result, err
	}
	*retryUsed = true
	log.Debug("Retrying Responses request after upstream session limit", "channel_id", channel.ID)
	request.Headers.Set(key.header, next)
	return send()
}
