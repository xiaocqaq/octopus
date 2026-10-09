package relay

import (
	"context"
	"sort"
	"sync"
	"sync/atomic"
	"time"

	"github.com/bestruirui/octopus/internal/model"
	"github.com/bestruirui/octopus/internal/op"
	"github.com/looplj/axonhub/llm"
)

// 客户端请求在转发过程中的当前状态。
type Status string

const (
	StatusRunning   Status = "running"   // 循环中: 正在选目标, 等待或请求上游。
	StatusCommitted Status = "committed" // 首字节已写出客户端, 此后不可再重试。
	StatusSuccess   Status = "success"   // 响应已完整交付客户端。
	StatusFailed    Status = "failed"    // 请求以错误结束。
	StatusCanceled  Status = "canceled"  // 客户端提前断开或取消。
)

// RetryError 保留一个失败轮次, 即使后续重试成功或请求被取消也不清除。
// 三个字段与 RequestState 上的同名字段同源: 目标渠道是该轮选中的渠道名与 Key 名, 目标模型是该轮真正请求的上游模型。
// 选路失败轮次没有目标, 两者留空。
type RetryError struct {
	Round            int    `json:"round"`
	TargetChannelKey string `json:"target_channel_key"`
	TargetModel      string `json:"target_model"`
	Error            string `json:"error"`
}

// 客户端请求的完整进程内状态, 同时作为状态流的消息形状; 上半部分在请求到达时写入并在结束时定稿, 下半部分每轮循环覆盖。
type RequestState struct {
	ID                 uint64         `json:"id"`                   // 请求在当前进程内的唯一标识。
	Status             Status         `json:"status"`               // 请求当前状态。
	StartedAt          time.Time      `json:"started_at"`           // 请求到达时间。
	Duration           time.Duration  `json:"duration"`             // 请求从到达到结束的总耗时, 未结束时为零。
	FirstTokenDuration time.Duration  `json:"first_token_duration"` // 请求到达到首字节写出客户端的耗时, 含选路与重试; 非流式同样记录。
	StreamDuration     time.Duration  `json:"stream_duration"`      // 流式响应从首字节提交到响应结束的耗时, 非流式响应为零。
	ResponseDuration   time.Duration  `json:"response_duration"`    // 非流式正确响应轮次开始到完整响应提交的耗时, 流式响应为零。
	Model              string         `json:"model"`                // 客户端请求的模型名称, 即分组名称。
	ReasoningEffort    string         `json:"reasoning_effort"`     // 客户端请求的思考等级, 未指定时为空。
	Protocol           model.Protocol `json:"protocol"`             // 客户端请求使用的协议, 由入站格式定出, 单个协议位而非掩码组合。
	GroupID            int            `json:"group_id"`             // 承载本请求的分组 ID, 供界面按主键直接定位分组而不必按名称回查。
	APIKeyName         string         `json:"api_key_name"`         // 发起请求时的 API Key 名称。
	Usage              llm.Usage      `json:"usage"`                // 请求结束时写入的展示用量。
	Cost               float64        `json:"cost"`                 // 请求结束时写入的累计费用。
	OutputChars        int            `json:"output_chars"`         // 流式过程中按事件数量估算并实时累计的输出字符数, 仅用于界面展示, 不参与结算。

	Round            int            `json:"round"`              // 最新一轮循环的递增序号, 人工中止按此匹配以免误杀下一轮。
	RoundStartedAt   time.Time      `json:"round_started_at"`   // 最新一轮上游请求的开始时间。
	TargetChannelKey string         `json:"target_channel_key"` // 最新一轮选中的渠道名称和 Key 名称, 以空格分隔。
	TargetModel      string         `json:"target_model"`       // 最新一轮实际请求上游的模型名称。
	TargetProtocol   model.Protocol `json:"target_protocol"`    // 最新一轮实际请求上游的协议, 与 Protocol 不同即本轮做了跨协议转换; 0 表示尚未选出。
	Sending          bool           `json:"sending"`            // 最新一轮是否仍在等待上游响应。
	Error            string         `json:"error,omitempty"`    // 最新一轮的失败原因, 请求结束后即为最终错误。

	RetryErrors []RetryError `json:"retry_errors,omitempty"` // 本次请求全部失败轮次的历史, 只增不减, 最近 maxRetryErrors 条。

	requestBody   string
	responseBody  string
	apiKeyID      int
	finalized     bool // 终态与请求级统计只能定稿一次。
	requestCtx    context.Context
	requestCancel context.CancelFunc
	roundCancel   context.CancelFunc
	lastPublish   time.Time
	streamStarted time.Time
}

const maxRetryErrors = 20                            // 单个请求保留的失败轮次上限, 防止长重试把每次推送的快照撑大。
const streamBuffer = 16                              // 单个状态流连接的非阻塞消息缓冲容量。
const maxFinished = 50                               // 进程内最多保留的已结束请求数量。
const outputPublishInterval = 500 * time.Millisecond // 输出字符数实时推送的最短发布间隔。

var (
	idSeq    atomic.Uint64                          // 进程内严格递增的请求 ID。
	mu       sync.Mutex                             // 全部共享状态的互斥锁。
	requests = make(map[uint64]*RequestState)       // 按请求 ID 保存的全部请求状态。
	watchers = make(map[chan RequestState]struct{}) // 全部状态流 SSE 连接。
)

// newRequestState 分配请求 ID 并登记初始运行状态; 返回的记录是本请求后续全部状态写入的入口。
func newRequestState(ctx context.Context, modelName, reasoningEffort string, groupID int, protocol model.Protocol, body string, apiKeyID int) *RequestState {
	requestCtx, requestCancel := context.WithCancel(ctx)
	mu.Lock()
	defer mu.Unlock()

	request := &RequestState{
		ID:              idSeq.Add(1),
		Status:          StatusRunning,
		StartedAt:       time.Now(),
		Model:           modelName,
		ReasoningEffort: reasoningEffort,
		Protocol:        protocol,
		GroupID:         groupID,
		requestBody:     body,
		apiKeyID:        apiKeyID,
		requestCtx:      requestCtx,
		requestCancel:   requestCancel,
	}
	// 登记时保存名称快照, 查询失败时留空。
	if apiKey, err := op.APIKeyGet(apiKeyID, ctx); err == nil {
		request.APIKeyName = apiKey.Name
	}
	requests[request.ID] = request
	publishRequestLocked(request)
	return request
}

// startRound 记录本轮选中的目标并进入上游请求, cancel 供人工中止本轮, 返回递增的轮次序号。
func (r *RequestState) startRound(cancel context.CancelFunc, channelKeyName, modelName string, protocol model.Protocol) int {
	mu.Lock()
	defer mu.Unlock()

	r.Round++
	r.RoundStartedAt = time.Now()
	r.OutputChars = 0 // 新一轮从头计数, 避免累计上一轮未提交的输出。
	r.lastPublish = time.Time{}
	r.TargetChannelKey = channelKeyName
	r.TargetModel = modelName
	r.TargetProtocol = protocol
	r.Sending = true
	r.Error = ""
	r.roundCancel = cancel
	publishRequestLocked(r)
	return r.Round
}

// finishRound 记录本轮上游结果, errText 为空表示已取得可提交响应。
func (r *RequestState) finishRound(errText string) {
	mu.Lock()
	defer mu.Unlock()

	r.Sending = false
	r.Error = errText
	r.appendRetryErrorLocked(errText)
	r.roundCancel = nil
	publishRequestLocked(r)
}

// appendRetryErrorLocked 把本轮失败追加进历史, 同一轮的相同失败只记一次, 超出 maxRetryErrors 时丢弃最旧的。
// 每次都换用新数组而不是原地追加: 已发布的状态快照与请求状态共享同一份底层数组, 原地追加会改写历史快照。
func (r *RequestState) appendRetryErrorLocked(errText string) {
	if errText == "" {
		return
	}
	failure := RetryError{Round: r.Round, TargetChannelKey: r.TargetChannelKey, TargetModel: r.TargetModel, Error: errText}
	for _, previous := range r.RetryErrors {
		if previous == failure {
			return
		}
	}
	start := max(0, len(r.RetryErrors)-maxRetryErrors+1)
	next := make([]RetryError, len(r.RetryErrors)-start+1)
	copy(next, r.RetryErrors[start:])
	next[len(next)-1] = failure
	r.RetryErrors = next
}

// failSelection 为没有发起上游调用的选路失败保留独立轮次, 不沿用上一轮的目标。
func (r *RequestState) failSelection(reason string) {
	r.startRound(nil, "", "", 0)
	r.finishRound(reason)
}

// addOutput 每个转发事件累加一个输出字符并按节流间隔发布快照; 距上次发布不足阈值时只累加不出流。
func (r *RequestState) addOutput() {
	mu.Lock()
	defer mu.Unlock()

	r.OutputChars++
	if time.Since(r.lastPublish) >= outputPublishInterval {
		r.lastPublish = time.Now()
		if !r.streamStarted.IsZero() {
			r.StreamDuration = time.Since(r.streamStarted)
		}
		publishRequestLocked(r)
	}
}

// Interrupt 中止指定请求仍在等待响应且轮次匹配的上游请求; 轮次不匹配说明该轮已结束, 不影响后续轮次。
func Interrupt(id uint64, round int) {
	mu.Lock()
	request := requests[id]
	if request == nil || request.Round != round || request.roundCancel == nil {
		mu.Unlock()
		return
	}
	cancel := request.roundCancel
	request.roundCancel = nil
	mu.Unlock()

	cancel()
}

// CancelRequest 取消指定的完整请求; 已结束请求不会被重新改写状态。
func CancelRequest(id uint64) {
	mu.Lock()
	request := requests[id]
	if request == nil {
		mu.Unlock()
		return
	}
	cancel := request.requestCancel
	request.requestCancel = nil
	mu.Unlock()

	if cancel != nil {
		cancel()
	}
}

// wait 在重新选择目标之前退避 seconds 秒; 客户端在退避期间断开时以取消终态定稿并返回 false。
func (r *RequestState) wait(ctx context.Context, seconds int) bool {
	select {
	case <-ctx.Done():
		r.markCanceled(ctx.Err(), "", nil)
		return false
	case <-time.After(time.Duration(seconds) * time.Second):
		return true
	}
}

// markCommitted 记录客户端感受到的首字耗时及最终轮次的响应耗时。
func (r *RequestState) markCommitted(streaming bool) {
	mu.Lock()
	defer mu.Unlock()

	now := time.Now()
	r.Status = StatusCommitted
	if r.FirstTokenDuration == 0 {
		r.FirstTokenDuration = now.Sub(r.StartedAt)
	}
	if streaming {
		r.streamStarted = now
	} else {
		r.ResponseDuration = now.Sub(r.RoundStartedAt)
	}
	publishRequestLocked(r)
}

// finishStream 记录首字节提交至流式响应实际结束的耗时。
func (r *RequestState) finishStream() {
	mu.Lock()
	defer mu.Unlock()

	if !r.streamStarted.IsZero() {
		r.StreamDuration = time.Since(r.streamStarted)
	}
}

// markSucceeded 以成功终态定稿请求。
func (r *RequestState) markSucceeded(responseBody string, usage *llm.Usage) {
	mu.Lock()
	defer mu.Unlock()
	if r.finalized {
		return
	}

	r.Status = StatusSuccess
	r.Error = ""
	r.responseBody = responseBody
	r.finishLocked(usage)
}

// markFailed 以失败终态定稿请求, 最终错误取自本次失败原因。
func (r *RequestState) markFailed(err error, responseBody string, usage *llm.Usage) {
	mu.Lock()
	defer mu.Unlock()
	if r.finalized {
		return
	}

	if r.requestCtx != nil && r.requestCtx.Err() != nil {
		r.Status = StatusCanceled
		r.Error = r.requestCtx.Err().Error()
	} else {
		r.Status = StatusFailed
		r.Error = err.Error()
	}
	if responseBody != "" {
		r.responseBody = responseBody
	}
	r.finishLocked(usage)
}

// markCanceled 以取消终态定稿请求, 用于客户端提前断开或主动取消。
func (r *RequestState) markCanceled(err error, responseBody string, usage *llm.Usage) {
	mu.Lock()
	defer mu.Unlock()
	if r.finalized {
		return
	}

	r.Status = StatusCanceled
	r.Error = err.Error()
	if responseBody != "" {
		r.responseBody = responseBody
	}
	r.finishLocked(usage)
}

// finishLocked 写入用量和费用, 发布终态, 更新请求级统计并裁剪历史; 调用方必须持有锁。
func (r *RequestState) finishLocked(usage *llm.Usage) {
	r.Sending = false
	r.roundCancel = nil
	if r.requestCancel != nil {
		r.requestCancel()
	}
	r.requestCancel = nil
	if usage != nil {
		r.Usage = *usage
	}
	metrics := usageMetrics(r.TargetModel, usage)
	r.Cost = metrics.InputCost + metrics.OutputCost
	r.Duration = time.Since(r.StartedAt)
	metrics.WaitTime = r.Duration.Milliseconds()
	if r.Status == StatusSuccess {
		metrics.RequestSuccess = 1
	} else {
		metrics.RequestFailed = 1
	}
	if !r.finalized {
		r.finalized = true
		_ = op.StatsTotalUpdate(metrics)
		_ = op.StatsHourlyUpdate(metrics)
		_ = op.StatsDailyUpdate(metrics)
		if r.apiKeyID > 0 {
			_ = op.StatsAPIKeyUpdate(r.apiKeyID, metrics)
		}
		// 分组榜按接收该请求的分组记一次, 不按成员复制渠道模型总量。
		op.StatsGroupUpdate(r.GroupID, metrics)
	}
	publishRequestLocked(r)

	finished := 0
	oldest := uint64(0)
	for id, request := range requests {
		if request.Status == StatusRunning || request.Status == StatusCommitted {
			continue
		}
		finished++
		if oldest == 0 || id < oldest {
			oldest = id
		}
	}
	if finished > maxFinished {
		delete(requests, oldest)
	}
}

// usageMetrics 将统一用量按模型单价转换为 Token 与费用统计; 无用量或价格时对应费用为零。
func usageMetrics(modelName string, usage *llm.Usage) model.StatsMetrics {
	if usage == nil {
		return model.StatsMetrics{}
	}
	metrics := model.StatsMetrics{InputToken: usage.PromptTokens, OutputToken: usage.CompletionTokens}
	cachedTokens, writeCachedTokens := int64(0), int64(0)
	if usage.PromptTokensDetails != nil {
		cachedTokens = usage.PromptTokensDetails.CachedTokens
		writeCachedTokens = usage.PromptTokensDetails.WriteCachedTokens
	}
	// 缓存命中的部分是输入的子集, 一并计入统计, 界面据此算命中率。
	// 未知单价时费用为零, 但命中量仍要留下, 否则分组榜的缓存率会变成 0。
	metrics.CachedToken = cachedTokens
	metrics.CacheWriteToken = writeCachedTokens
	price, err := op.LLMGet(modelName)
	if err != nil {
		return metrics
	}
	inputTokens := max(int64(0), usage.PromptTokens-cachedTokens-writeCachedTokens)
	metrics.InputCost = (float64(inputTokens)*price.Input + float64(cachedTokens)*price.CacheRead + float64(writeCachedTokens)*price.CacheWrite) / 1_000_000
	metrics.OutputCost = float64(usage.CompletionTokens) * price.Output / 1_000_000
	return metrics
}

// publishRequestLocked 非阻塞发布最新请求状态, 连接拥塞时关闭它并交给客户端重连获取全量快照; 调用方必须持有锁。
func publishRequestLocked(request *RequestState) {
	for stream := range watchers {
		select {
		case stream <- *request:
		default:
			delete(watchers, stream)
			close(stream)
		}
	}
}

// OpenRequestStream 注册请求状态流连接, 返回按请求 ID 倒序的全部快照和后续增量通道。
// 日志页不提供排序开关, 而 requests 是 map, 遍历顺序随机, 故顺序须由此处定稿。
func OpenRequestStream() ([]RequestState, chan RequestState) {
	mu.Lock()
	defer mu.Unlock()

	stream := make(chan RequestState, streamBuffer)
	watchers[stream] = struct{}{}

	snapshot := make([]RequestState, 0, len(requests))
	for _, request := range requests {
		snapshot = append(snapshot, *request)
	}
	sort.Slice(snapshot, func(i, j int) bool { return snapshot[i].ID > snapshot[j].ID })
	return snapshot, stream
}

// CloseRequestStream 注销并关闭指定请求状态流连接。
func CloseRequestStream(stream chan RequestState) {
	mu.Lock()
	defer mu.Unlock()

	if _, exists := watchers[stream]; exists {
		delete(watchers, stream)
		close(stream)
	}
}

// RequestBody 返回指定请求保存的原始请求体, 记录不存在时返回空串。
func RequestBody(id uint64) string {
	mu.Lock()
	defer mu.Unlock()

	if request := requests[id]; request != nil {
		return request.requestBody
	}
	return ""
}

// ResponseBody 返回指定请求当前保存的响应体, 记录不存在或响应未完成时返回空串。
func ResponseBody(id uint64) string {
	mu.Lock()
	defer mu.Unlock()

	if request := requests[id]; request != nil {
		return request.responseBody
	}
	return ""
}

// Clear 删除全部已结束的请求记录。
func Clear() {
	mu.Lock()
	defer mu.Unlock()

	for id, request := range requests {
		if request.Status != StatusRunning && request.Status != StatusCommitted {
			delete(requests, id)
		}
	}
}
