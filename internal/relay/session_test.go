package relay

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/bestruirui/octopus/internal/model"
	"github.com/google/uuid"
	"github.com/looplj/axonhub/llm"
	"github.com/looplj/axonhub/llm/httpclient"
)

func TestResponsesSessionConcurrentReservations(t *testing.T) {
	store := responseSessionStore{entries: make(map[responseSessionKey]responseSessionEntry)}
	now := time.Now()
	key := responseSessionKey{1, 2, 3, "session-id", sha256.Sum256([]byte("client"))}
	const limit = 90
	ids := make(chan string, limit*3)
	var wg sync.WaitGroup
	for range cap(ids) {
		wg.Go(func() { ids <- store.reserve(key, limit, "", now) })
	}
	wg.Wait()
	close(ids)
	counts := make(map[string]int)
	for id := range ids {
		if _, err := uuid.Parse(id); err != nil {
			t.Fatalf("invalid upstream ID %q", id)
		}
		counts[id]++
	}
	if len(counts) != 3 {
		t.Fatalf("got %d upstream sessions, want 3", len(counts))
	}
	for _, n := range counts {
		if n != limit {
			t.Fatalf("reserved %d attempts instead of %d", n, limit)
		}
	}
	old := store.reserve(key, limit, "", now)
	for _, other := range []responseSessionKey{
		{4, key.channelID, key.channelKeyID, key.header, key.clientID},
		{key.apiKeyID, 4, key.channelKeyID, key.header, key.clientID},
		{key.apiKeyID, key.channelID, 4, key.header, key.clientID},
		{key.apiKeyID, key.channelID, key.channelKeyID, "session_id", key.clientID},
		{key.apiKeyID, key.channelID, key.channelKeyID, key.header, sha256.Sum256([]byte("other"))},
	} {
		if id := store.reserve(other, limit, "", now); id == old {
			t.Fatal("session mapping leaked across scopes")
		}
	}
	next := store.reserve(key, limit, old, now)
	if next == old || store.reserve(key, limit, old, now) != next {
		t.Fatal("stale rejections must reuse the new generation")
	}
	if store.reserve(key, limit, "", now.Add(responseSessionIdleTTL)) == next {
		t.Fatal("expired session reused")
	}
}

func TestResponsesSessionEligibility(t *testing.T) {
	format := llm.APIFormatOpenAIResponse
	channel := model.Channel{ID: 2, ChannelConfig: model.ChannelConfig{SessionIDHeader: "session-id"}}
	raw := &httpclient.Request{Headers: make(http.Header), Body: []byte(`{"model":"mock","previous_response_id":null,"conversation":null,"input":"hello"}`)}
	raw.Headers.Set("Session-Id", "client")
	if _, ok := responseSessionIdentity(format, raw, raw, channel, 1, 3); !ok {
		t.Fatal("stateless request should be eligible")
	}
	for _, body := range []string{
		`{"previous_response_id":"resp_123","input":"continue"}`,
		`{"conversation":{"id":"conv_123"},"input":"continue"}`,
		`{"input":[{"type":"item_reference","id":"item_123"}]}`,
	} {
		request := *raw
		request.Body = []byte(body)
		if _, ok := responseSessionIdentity(format, raw, &request, channel, 1, 3); ok {
			t.Fatalf("stateful body was eligible: %s", body)
		}
	}
	request := *raw
	request.Headers = raw.Headers.Clone()
	request.Headers.Set("session-id", "channel override")
	if _, ok := responseSessionIdentity(format, raw, &request, channel, 1, 3); ok {
		t.Fatal("custom header override was ignored")
	}
	if _, ok := responseSessionIdentity(llm.APIFormatOpenAIChatCompletion, raw, raw, channel, 1, 3); ok {
		t.Fatal("other protocol was eligible")
	}
	if _, ok := responseSessionIdentity(format, raw, raw, channel, 0, 3); ok {
		t.Fatal("unauthenticated request was eligible")
	}
	channel.SessionIDHeader = ""
	if _, ok := responseSessionIdentity(format, raw, raw, channel, 1, 3); ok {
		t.Fatal("disabled channel was eligible")
	}
}

func TestResponsesSessionPassthroughLimitRetry(t *testing.T) {
	for _, streaming := range []bool{false, true} {
		t.Run(fmt.Sprint(streaming), func(t *testing.T) {
			format := llm.APIFormatOpenAIResponse
			body := fmt.Sprintf(`{"model":"mock", "stream":%t, "input":[{"role":"user","content":"hello"}]}`, streaming)
			seen := make(chan string, 4)
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
				data, err := io.ReadAll(req.Body)
				if err != nil || string(data) != body || req.Header.Get("thread-id") != "thread" {
					t.Errorf("passthrough changed body or thread header: %s, %v", data, err)
				}
				seen <- req.Header.Get("session-id")
				w.WriteHeader(http.StatusBadRequest)
				fmt.Fprint(w, `{"error":{"code":"session_request_limit_exceeded"}}`)
			}))
			defer server.Close()
			channel := model.Channel{ID: 2, ChannelConfig: model.ChannelConfig{SessionIDHeader: "session-id", SessionMaxRequests: 90}}
			raw := &httpclient.Request{Method: "POST", Headers: make(http.Header), Body: []byte(body)}
			raw.Headers.Set("session-id", "client-"+uuid.NewString())
			raw.Headers.Set("thread-id", "thread")
			originalID := raw.Headers.Get("session-id")
			outbound := conversionTestOutbound(t, format, server.URL)
			spent := false
			for range 2 {
				_, err := sendPassthrough(context.Background(), format, raw, channel, outbound, streaming, "mock", 1, 3, &spent)
				if !isResponseSessionLimitError(err) {
					t.Fatalf("unexpected error: %v", err)
				}
			}
			close(seen)
			var ids []string
			for id := range seen {
				ids = append(ids, id)
			}
			if !spent || len(ids) != 3 || ids[0] == "" || ids[0] == originalID || ids[0] == ids[1] || ids[1] != ids[2] {
				t.Fatalf("expected one retry with a new upstream ID, then reuse: %v", ids)
			}
			if raw.Headers.Get("session-id") != originalID || string(raw.Body) != body {
				t.Fatal("inbound request mutated")
			}
		})
	}
}

func TestResponsesSessionSendBoundaries(t *testing.T) {
	for _, test := range []struct {
		name                                 string
		nilRetry, cancelBefore, cancelOnSend bool
		sendErr                              error
	}{
		{name: "nil retry", nilRetry: true, sendErr: errSessionRequestLimit},
		{name: "canceled before", cancelBefore: true},
		{name: "canceled on send", cancelOnSend: true, sendErr: errSessionRequestLimit},
		{name: "ordinary error", sendErr: errors.New("transport failure")},
		{name: "accepted stream"},
	} {
		t.Run(test.name, func(t *testing.T) {
			store := responseSessionStore{}
			raw := &httpclient.Request{Headers: http.Header{"Session-Id": {"client"}}, Body: []byte(`{"input":"hello"}`)}
			request := *raw
			request.Headers = raw.Headers.Clone()
			channel := model.Channel{ID: 2, ChannelConfig: model.ChannelConfig{SessionIDHeader: "session-id"}}
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			if test.cancelBefore {
				cancel()
			}
			used := false
			retry := &used
			if test.nilRetry {
				retry = nil
			}
			calls := 0
			_, err := store.sendWithResponsesSession(ctx, llm.APIFormatOpenAIResponse, raw, &request, channel, 1, 3, retry, func() (*upstreamResponse, error) {
				calls++
				if test.cancelOnSend {
					cancel()
				}
				return &upstreamResponse{}, test.sendErr
			})
			want := 1
			if test.cancelBefore {
				want = 0
				if !errors.Is(err, context.Canceled) {
					t.Fatal(err)
				}
			}
			if calls != want || used {
				t.Fatalf("unexpected replay: calls=%d retry=%v", calls, used)
			}
			if test.nilRetry && request.Headers.Get("session-id") != "client" {
				t.Fatal("nil retry should bypass mapping")
			}
			if raw.Headers.Get("session-id") != "client" {
				t.Fatal("inbound header mutated")
			}
		})
	}
	if _, ok := responseSessionIdentity(llm.APIFormatOpenAIResponse, nil, nil, model.Channel{}, 1, 3); ok {
		t.Fatal("nil request eligible")
	}
}

func TestResponsesSessionCapacity(t *testing.T) {
	store := responseSessionStore{entries: make(map[responseSessionKey]responseSessionEntry)}
	now := time.Now()
	for i := range responseSessionCapacity {
		store.entries[responseSessionKey{apiKeyID: i + 2}] = responseSessionEntry{upstreamID: "existing", lastUsed: now}
	}
	raw := &httpclient.Request{Headers: http.Header{"Session-Id": {"client"}}, Body: []byte(`{"input":"hello"}`)}
	request := *raw
	request.Headers = raw.Headers.Clone()
	channel := model.Channel{ID: 2, ChannelConfig: model.ChannelConfig{SessionIDHeader: "session-id"}}
	used := false
	calls := 0
	_, err := store.sendWithResponsesSession(context.Background(), llm.APIFormatOpenAIResponse, raw, &request, channel, 1, 3, &used, func() (*upstreamResponse, error) { calls++; return nil, nil })
	if !errors.Is(err, errResponseSessionCapacity) || calls != 0 || request.Headers.Get("session-id") != "client" {
		t.Fatalf("capacity bypassed mapping: %v calls=%d", err, calls)
	}
	key := responseSessionKey{apiKeyID: 1}
	if store.reserve(key, 90, "", now.Add(responseSessionIdleTTL)) == "" {
		t.Fatal("expired entries not reclaimed")
	}
	if len(store.entries) != 1 {
		t.Fatalf("expired entries retained: %d", len(store.entries))
	}
}

func TestResponsesSessionHeaderAmbiguity(t *testing.T) {
	raw := &httpclient.Request{Headers: http.Header{"Session-Id": {"client"}}, Body: []byte(`{"input":"hello"}`)}
	channel := model.Channel{ID: 2, ChannelConfig: model.ChannelConfig{SessionIDHeader: "session-id"}}
	for _, badBody := range []string{"{", "[]", `{"previous_response_id":"x"}`} {
		request := *raw
		request.Body = []byte(badBody)
		if _, ok := responseSessionIdentity(llm.APIFormatOpenAIResponse, raw, &request, channel, 1, 3); ok {
			t.Fatalf("bad body eligible: %s", badBody)
		}
	}
	for _, values := range [][]string{{"one", "two"}, {"one,two"}, {""}} {
		request := *raw
		request.Headers = raw.Headers.Clone()
		request.Headers["Session-Id"] = values
		if _, ok := responseSessionIdentity(llm.APIFormatOpenAIResponse, &request, &request, channel, 1, 3); ok {
			t.Fatal("ambiguous ID eligible")
		}
	}
	raw.Headers.Set("session_id", "alternate")
	if _, ok := responseSessionIdentity(llm.APIFormatOpenAIResponse, raw, raw, channel, 1, 3); ok {
		t.Fatal("dual aliases eligible")
	}
}

func TestResponsesSessionLimitRequiresExactHTTPCode(t *testing.T) {
	for _, err := range []error{
		errors.New("session_request_limit_exceeded"),
		&httpclient.Error{StatusCode: 400, Body: []byte(`{"error":{"code":"other","message":"session_request_limit_exceeded"}}`)},
		&httpclient.Error{StatusCode: 429, Body: []byte(`{"error":{"code":"session_request_limit_exceeded"}}`)},
	} {
		if isResponseSessionLimitError(err) {
			t.Fatalf("unrelated error was classified as session limit: %v", err)
		}
	}
}
