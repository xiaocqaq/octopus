package relay

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"slices"
	"time"

	"github.com/bestruirui/octopus/internal/model"
	"github.com/bestruirui/octopus/internal/op"
	"github.com/gin-contrib/sse"
	"github.com/gin-gonic/gin"
	"github.com/looplj/axonhub/llm"
	"github.com/looplj/axonhub/llm/httpclient"
	"github.com/looplj/axonhub/llm/transformer"
	"github.com/looplj/axonhub/llm/transformer/anthropic"
	"github.com/looplj/axonhub/llm/transformer/openai"
	"github.com/looplj/axonhub/llm/transformer/openai/responses"
	"github.com/tidwall/sjson"
)

// Forward 按客户端协议承载一个请求的完整转发过程: 解析请求, 定位分组, 循环选目标请求上游, 直至提交响应或请求结束。
func Forward(format llm.APIFormat) gin.HandlerFunc {
	// 客户端协议同时定出入站转换器和请求协议位: 后者随请求状态推给界面, 也是每轮选择上游协议的首选。
	var inbound transformer.Inbound
	requestProtocol := model.ProtocolOpenAIChatCompletion
	switch format {
	case llm.APIFormatOpenAIResponse:
		inbound = responses.NewInboundTransformer()
		requestProtocol = model.ProtocolOpenAIResponse
	case llm.APIFormatAnthropicMessage:
		inbound = anthropic.NewInboundTransformer()
		requestProtocol = model.ProtocolAnthropicMessage
	default:
		inbound = openai.NewInboundTransformer()
	}

	return func(c *gin.Context) {
		// 完整读取客户端请求, 正文先登记到请求状态, 后续每轮直接改写为当前目标请求。
		raw, err := httpclient.ReadHTTPRequest(c.Request)
		if err != nil {
			rejectRequest(c, inbound, err)
			return
		}

		// 此处只读取选组和分流所需字段; 完整协议校验由同协议上游或跨协议 pipeline 完成。
		var metadata struct {
			Model     string `json:"model"`  // 客户端请求的分组名称。
			Streaming bool   `json:"stream"` // 客户端是否请求流式响应。
		}
		if err := json.Unmarshal(raw.Body, &metadata); err != nil {
			rejectRequest(c, inbound, err)
			return
		}

		// API Key 限定了模型范围时只放行范围内的模型, 为空表示不限制。
		if allowed, ok := c.Get("supported_models"); ok {
			if names, _ := allowed.([]string); len(names) > 0 && !slices.Contains(names, metadata.Model) {
				rejectRequest(c, inbound, errors.New("model not supported by this api key"))
				return
			}
		}

		// 客户端请求的模型名称即分组名称; 分组不存在说明模型名错误, 等待也不会出现该分组。
		// 分组主键随请求状态一并登记, 界面由此可直接按主键取分组而不必按名称回查。
		group, err := op.GroupGetByName(metadata.Model)
		if err != nil {
			rejectRequest(c, inbound, errors.New("model not found"))
			return
		}

		// 登记进程内请求状态, 返回的记录是后续全部状态写入和前端可视化推送的入口。
		request := newRequestState(c.Request.Context(), metadata.Model, group.ID, requestProtocol, string(raw.Body), c.GetInt("api_key_id"))
		ctx := c.Request.Context()
		failedItemID := 0 // 当前累计连续失败次数的成员 ID。
		failures := 0     // 该成员包含首次请求的连续失败次数。
		// reasoningStripped 表示已为去掉思维凭据额外重试过一次; 每个请求只做一次, 之后凭据已不在请求体里。
		reasoningStripped := false
		// blockedRounds 是连续"一个可转发的目标都选不出来"的轮数: 没选中成员, 或选中了而它指向的授权/渠道已不可用。
		// 这类等待原本没有出口, 客户端只能挂到超时: 等满一轮仍是同样结果就如实报错, 让客户端看到原因。
		blockedRounds := 0
		// blocked 登记一次"本轮选不出目标"并判断该不该结束请求; 需要结束则返回 true。
		// 第一轮照旧等待一个重试间隔(成员可能只是正在冷却, 配置可能正在被修好), 连续两轮同样结果即报错。
		// 状态码用 503: 这是"目标暂时不可用"而非"请求写错了", 客户端据此重试是有意义的。
		blocked := func(reason string) bool {
			blockedRounds++
			if blockedRounds <= 1 {
				return false
			}
			err := errors.New(reason)
			request.markFailed(err, "", nil)
			rejectRequestStatus(c, inbound, http.StatusServiceUnavailable, err)
			return true
		}

		for {
			if ctx.Err() != nil {
				request.markCanceled(ctx.Err(), "", nil)
				return
			}

			// 分组配置和成员随时可改, 故每轮重新读取; 分组被删除时先等一轮, 仍未恢复即报错。
			group, err = op.GroupGetByName(metadata.Model)
			if err != nil {
				if blocked(fmt.Sprintf("group %q not found: it may have been deleted", metadata.Model)) {
					return
				}
				if !request.wait(ctx, model.DefaultGroupRelayConfig().MemberRetryIntervalSeconds) {
					return
				}
				continue
			}

			// 手动模式取人工指定的成员, 故障转移模式按优先级选择未禁用且不在冷却中的成员。
			// 没有目标时先等一轮, 期间人工切换渠道, 补齐成员或成员冷却到期即可让请求继续;
			// 一整轮过去仍然选不出目标就不再等: 报文里带上冷却成员数与最早恢复时间。
			item := pickGroupItem(group)
			if item.ID == 0 {
				if blocked(noAvailableMemberReason(group)) {
					return
				}
				if !request.wait(ctx, group.RelayConfig.MemberRetryIntervalSeconds) {
					return
				}
				continue
			}

			// 换成员不预先剥离: 报错后的剥离重发路径(见下方 reasoningStripped 分支)已覆盖这种情况,
			// 预先剥离会让每一轮故障转移都白丢思维连续性, 而多数上游其实认这份凭据。

			// 成员指向的授权缺失, 凭据被停用或两侧已被删除时等待, 该成员可能很快被改回可用配置。
			// ChannelGrantGet 一次校验齐这几种情况, 取到的授权必然可直接转发, 无需再逐项检查。
			// 本轮选路若占用了探测名额, 凡未走到成败定局点就换成员或结束请求的路径都要先归还,
			// 否则该成员会被一个已不存在的探测永久占用: 冷却到期也进不去, 界面之外无人能解。
			grant, err := op.ChannelGrantGet(item.ChannelGrantID)
			if err != nil {
				releaseRouteProbe(group, item.ID)
				if blocked(fmt.Sprintf("member of group %q is not usable: %v", group.Name, err)) {
					return
				}
				if !request.wait(ctx, group.RelayConfig.MemberRetryIntervalSeconds) {
					return
				}
				continue
			}
			channelModel := grant.ChannelModel
			channelKey := grant.ChannelKey

			// 成员指向的渠道已被删除时同样等待, 该成员可能很快被改回可用渠道。
			channel, err := op.ChannelGet(channelModel.ChannelID)
			if err != nil {
				releaseRouteProbe(group, item.ID)
				if blocked(fmt.Sprintf("channel of a member in group %q is unavailable: %v", group.Name, err)) {
					return
				}
				if !request.wait(ctx, group.RelayConfig.MemberRetryIntervalSeconds) {
					return
				}
				continue
			}
			// 已选出完整可转发的目标: 从这一刻起请求不再算"选不出目标", 后续失败重试重新起算。
			blockedRounds = 0

			// 将分组成员配置的真实模型写入本轮上游请求。
			raw.Body, err = sjson.SetBytes(raw.Body, "model", channelModel.Name)
			if err != nil {
				releaseRouteProbe(group, item.ID)
				request.markFailed(err, "", nil)
				rejectRequest(c, inbound, err)
				return
			}
			// OpenAI Chat 流式响应需显式要求上游在末尾附带用量。
			if metadata.Streaming && format == llm.APIFormatOpenAIChatCompletion {
				raw.Body, err = sjson.SetBytes(raw.Body, "stream_options.include_usage", true)
				if err != nil {
					releaseRouteProbe(group, item.ID)
					request.markFailed(err, "", nil)
					rejectRequest(c, inbound, err)
					return
				}
			}

			// 在渠道授权支持的协议内选出本轮上游协议, 按该协议的路径与授权绑定的凭据构造出站转换器。
			// 先于登记本轮目标: 选中的协议是本轮目标的一部分, 需与渠道和模型一并推给界面。
			outbound, targetProtocol, passthrough, err := buildOutbound(channel, grant, *channelKey, requestProtocol)

			// 为本轮上游调用建立独立取消入口并登记当前目标; 取消原因用于区分人工中止与响应超时。
			roundCtx, cancelRoundCause := context.WithCancelCause(ctx)
			// 人工中止和本轮完成都使用普通 canceled 原因, 超时回调则写入具体的超时错误。
			cancelRound := func() {
				cancelRoundCause(context.Canceled)
			}
			request.startRound(cancelRound, channel.Name, channelModel.Name, targetProtocol)

			roundStartedAt := time.Now() // 本轮上游调用的开始时间, 用于统计首个有效响应耗时。

			// 请求上游并等待首个有效响应: 非流式等待完整响应, 流式等待首个事件。
			// 同协议渠道原样直通, 跨协议渠道经转换后请求; 此时尚未写给客户端, 失败仍可换目标重试。
			var result *upstreamResponse
			// 只有真正发起上游调用后返回的快速错误才可能是思维凭据问题。
			// buildOutbound 失败属于本地配置/协议错误, 不应触发剥离重试。
			upstreamAttempted := err == nil
			if err == nil {
				timeoutSeconds := group.RelayConfig.MemberNonStreamResponseTimeoutSeconds // 非流式等待完整响应, 流式分支改为首事件超时。
				timeoutErr := errors.New("upstream non-stream response timeout")          // 具体错误用于区分超时与人工中止。
				if metadata.Streaming {
					timeoutSeconds = group.RelayConfig.MemberStreamFirstEventTimeoutSeconds
					timeoutErr = errors.New("upstream stream first event timeout")
				}
				// 计时器取消本轮上下文, 让正在等待 HTTP 响应或首个流事件的调用及时返回。
				timeoutTimer := time.AfterFunc(time.Duration(timeoutSeconds)*time.Second, func() {
					cancelRoundCause(timeoutErr)
				})
				// 客户端与渠道协议一致时直接透传, 其余组合通过 pipeline 转换。
				if passthrough {
					result, err = sendPassthrough(roundCtx, format, raw, channel, outbound, metadata.Streaming, channelModel.Name)
				} else {
					result, err = sendConverted(roundCtx, format, raw, channel, outbound, metadata.Streaming)
				}
				// 上游调用返回即结束首响应等待; Stop 失败说明已到期, 主动取消可避免等待异步回调完成。
				if !timeoutTimer.Stop() {
					cancelRoundCause(timeoutErr)
				}
				if context.Cause(roundCtx) == timeoutErr {
					err = timeoutErr
					// 超时与响应返回同时发生时舍弃尚未提交的流结果, 避免把超时误记为成功。
					if result != nil && result.events != nil {
						result.events.Close()
						if result.closeIdle != nil {
							result.closeIdle()
						}
					}
				}
			}

			if err != nil {
				// 记录本轮上游调用已经结束及其失败原因。
				request.finishRound(err.Error())
				// 父上下文结束说明客户端已经取消, 归还探测占用并以取消终态结束请求。
				if ctx.Err() != nil {
					releaseRouteProbe(group, item.ID)
					request.markCanceled(ctx.Err(), "", nil)
					return
				}
				// 仅人工中止本轮时不计失败也不等待; 响应超时属于真实失败并消耗尝试次数。
				if context.Cause(roundCtx) == context.Canceled {
					releaseRouteProbe(group, item.ID)
					continue
				}
				// 上游快速报错(不是超时, 故上下文没有取消原因)时才值得剥掉思维凭据再试:
				// 凭据只有签发它的账号认, 换账号或换密钥都会被拒, 这是转发的锅不该记在成员头上。
				// 不计失败也不等待, 也就不会把它推进冷却; 少了这一步, 坏凭据会让成员接连冷却, 整个分组停摆。
				// 超时不在此列: 那说明账号本身没响应, 剥掉凭据也救不回来, 再试只是让客户端多等一个超时。
				// 开启思维凭据过滤的渠道按可移植标准一次清净(记录 id, 服务端引用, store):
				// 它的账号随时在换, 只剥凭据救不回这些字段引来的后续报错。
				// 资源归属类报错同样按可移植标准清洗, 与渠道开关无关 —— 那句话字面就是"你的引用不属于这个资源",
				// 只剥凭据重试必然再撞一次(实测会一路重试到客户端超时), 而这些字段本就是被拒的原因。
				if upstreamAttempted && !reasoningStripped && context.Cause(roundCtx) == nil && shouldStripReasoning(err) {
					scrub := stripSignedReasoning
					if channel.ReasoningFilter || needsPortabilityScrub(err) {
						scrub = scrubSignedReasoning
					}
					if stripped, ok := scrub(raw.Body, requestProtocol); ok {
						raw.Body = stripped
						reasoningStripped = true
						// 重选路要能再次选中同一个成员才能重试: 本轮若占着它的探测名额,
						// 不换成员的单成员分组会因名额未释放而永远选不出目标。
						releaseRouteProbe(group, item.ID)
						continue
					}
				}
				cancelRound()
				// 本轮真实失败只计入当前渠道和成员, 客户端取消与人工中止不计为渠道故障。
				metrics := model.StatsMetrics{WaitTime: time.Since(roundStartedAt).Milliseconds(), RequestFailed: 1}
				_ = op.ChannelStatsUpdate(channel.ID, metrics)
				_ = op.ChannelModelStatsUpdate(channelModel.ID, metrics)
				_ = op.ChannelKeyStatsUpdate(channelKey.ID, metrics)

				// 成员改变时重新开始累计该成员在本请求内的连续失败次数。
				if failedItemID == item.ID {
					failures++
				} else {
					failedItemID = item.ID
					failures = 1
				}
				// 达到总尝试次数时成员进入冷却并立即重新选路, 否则等待后重试。
				if recordRouteFailure(group, item.ID, failures) {
					continue
				}
				if !request.wait(ctx, group.RelayConfig.MemberRetryIntervalSeconds) {
					return
				}
				continue
			}
			// 记录本轮已经取得可提交的上游响应。
			request.finishRound("")
			roundWaitTime := time.Since(roundStartedAt).Milliseconds() // 流式响应只统计等待首帧的时间。
			// 同协议透传时原样返回上游响应头; 跨协议响应没有需要透传的响应头。
			for key, values := range result.header {
				c.Writer.Header()[key] = values
			}

			// 非流式响应已经完整取得, 提交后一次写给客户端。
			if !metadata.Streaming {
				cancelRound()
				if c.Writer.Header().Get("Content-Type") == "" {
					c.Header("Content-Type", "application/json")
				}
				// 非流式响应已有完整用量, 本轮渠道和成员统计可在提交前一次完成。
				metrics := usageMetrics(channelModel.Name, result.usage)
				metrics.WaitTime = roundWaitTime
				metrics.RequestSuccess = 1
				_ = op.ChannelStatsUpdate(channel.ID, metrics)
				_ = op.ChannelModelStatsUpdate(channelModel.ID, metrics)
				_ = op.ChannelKeyStatsUpdate(channelKey.ID, metrics)
				recordRouteSuccess(group, item.ID)
				request.markCommitted()
				n, err := c.Writer.Write(result.body)
				if err == nil && n != len(result.body) {
					err = io.ErrShortWrite
				}
				if err != nil {
					if ctx.Err() != nil {
						request.markCanceled(ctx.Err(), string(result.body), result.usage)
					} else {
						request.markFailed(err, string(result.body), result.usage)
					}
					return
				}
				request.markSucceeded(string(result.body), result.usage)
				return
			}
			// 首帧提交后仍需逐个事件判断协议终态: 上游发出结束事件后未必立即关闭响应体, 继续读取会一直阻塞到
			// 客户端断开, 从而把已完整交付的响应误判为 context canceled。
			if c.Writer.Header().Get("Content-Type") == "" {
				c.Header("Content-Type", "text/event-stream")
			}
			// 首事件超时只保到首帧: 首帧之后上游仍可能迟迟不发下一个事件, 或挂着连接既不结束也不关闭,
			// 此时本轮既没有失败也没有终态, 客户端只能一直等。故为整轮流式响应再设一道总时长闸门:
			// 预算从本轮发起时算起(首帧等待也占用它), 到点即取消本轮上下文, 阻塞中的 Next 随之返回。
			// 取消原因单独一个错误值, 便于与人工中止, 客户端断开区分, 并作为本请求的失败原因如实记录。
			streamTotalErr := errors.New("upstream stream total timeout")
			streamTotalBudget := time.Duration(group.RelayConfig.MemberStreamTotalTimeoutSeconds) * time.Second
			// 预算为非正值说明该分组没配这项(存量分组的配置里没有该键): 此时不设总时长闸门。
			// 不按"0 秒预算"处理: 那等于闸门在本轮发起瞬间到期, 会掐断每一轮流式响应,
			// 而这既没有失败原因也没有终态, 客户端只会看到响应中途断掉。
			// 读取路径已在 groupSnapshot 里补齐默认值, 这里是第二道保险: 转发侧不再依赖调用方补过值。
			var streamTotalTimer *time.Timer
			if streamTotalBudget > 0 {
				// 首帧已耗掉一部分预算, 余额不足时给一个极小的正值: 交给计时器立即到期, 而不是用非正时长导致永不触发。
				streamTotalRemaining := streamTotalBudget - time.Since(roundStartedAt)
				if streamTotalRemaining <= 0 {
					streamTotalRemaining = time.Millisecond
				}
				streamTotalTimer = time.AfterFunc(streamTotalRemaining, func() {
					cancelRoundCause(streamTotalErr)
				})
			}
			var encoded bytes.Buffer
			var chunks []*httpclient.StreamEvent
			event := result.first
			last := result.last // 已转发的最后一个事件是否已按客户端协议结束整个响应流。
			committed := false
			streamFailure := false
			for {
				if event != nil {
					chunks = append(chunks, event)
					encoded.Reset()
					if encodeErr := sse.Encode(&encoded, sse.Event{Id: event.LastEventID, Event: event.Type, Data: event.Data}); encodeErr != nil {
						err = encodeErr
						break
					}
					if !committed {
						request.markCommitted()
						committed = true
					}
					n, writeErr := c.Writer.Write(encoded.Bytes())
					if writeErr == nil && n != encoded.Len() {
						writeErr = io.ErrShortWrite
					}
					if writeErr != nil {
						err = writeErr
						break
					}
					c.Writer.Flush()
				}
				if last {
					break
				}
				if !result.events.Next() {
					err = result.events.Err()
					if err == nil {
						err = errors.New("upstream stream ended before terminal event")
					}
					streamFailure = true
					break
				}
				event = result.events.Current()
				// 已提交的响应不能再换目标重试, 结束事件自身携带的失败原样转发给客户端, 并在转发后作为本请求终态。
				last, err = inspectStreamEvent(format, event)
				if err != nil {
					streamFailure = true
				}
			}
			// 循环已结束, 总时长闸门到此不再需要; Stop 失败说明已到期, 下面按取消原因认定本轮结果。
			// 没配这项的分组没有装闸门, 此处按空定时器跳过。
			if streamTotalTimer != nil {
				streamTotalTimer.Stop()
			}
			// 预算耗尽时上游读取是被本轮上下文掐断的, 取消带来的错误(甚至读到一半的正常返回)都要改判为超时:
			// 否则这类请求会以 context canceled 收尾, 看起来像客户端主动断开, 成员也就不会因此进入冷却。
			if context.Cause(roundCtx) == streamTotalErr {
				err = streamTotalErr
				streamFailure = true
			}
			result.events.Close()
			// 事件流已读完, 渠道专用代理的独占连接池到此归还。
			if result.closeIdle != nil {
				result.closeIdle()
			}
			cancelRound()
			// 使用客户端协议转换器聚合已转发事件, 统一取得最终响应正文和用量。
			responseBody, meta, aggregateErr := inbound.AggregateStreamChunks(context.WithoutCancel(ctx), chunks)
			if aggregateErr == nil {
				result.usage = meta.Usage
			}
			// 流式响应结束并聚合出用量后, 按最终结果完成本轮渠道和成员统计。
			metrics := usageMetrics(channelModel.Name, result.usage)
			metrics.WaitTime = roundWaitTime
			if err == nil {
				metrics.RequestSuccess = 1
			} else {
				metrics.RequestFailed = 1
			}
			_ = op.ChannelStatsUpdate(channel.ID, metrics)
			_ = op.ChannelModelStatsUpdate(channelModel.ID, metrics)
			_ = op.ChannelKeyStatsUpdate(channelKey.ID, metrics)
			if err != nil {
				if streamFailure && ctx.Err() == nil {
					// 流已提交后不能在本请求内重试, 异常终态直接让该成员进入冷却, 供下一请求切换。
					recordRouteFailure(group, item.ID, group.RelayConfig.MemberMaxAttempts)
				}
				if ctx.Err() != nil {
					request.markCanceled(ctx.Err(), string(responseBody), result.usage)
				} else {
					request.markFailed(err, string(responseBody), result.usage)
				}
				return
			}
			// 流式响应只有完整收到正常终态后才算成员成功; 首帧成功不代表本轮成功。
			recordRouteSuccess(group, item.ID)
			request.markSucceeded(string(responseBody), result.usage)
			return
		}
	}
}

// rejectRequest 以客户端协议的错误形状拒绝请求: 400 用于请求本身有误, 重试也不会变好。
// 尚未登记请求状态时用它收尾: 这类拒绝发生在任何状态定稿之前, 不会留下半截记录。
func rejectRequest(c *gin.Context, inbound transformer.Inbound, err error) {
	rejectRequestStatus(c, inbound, http.StatusBadRequest, err)
}

// rejectRequestStatus 同 rejectRequest 但可指定状态码: 目标暂时不可用属于可重试的临时状态, 用 503 更贴近实情,
// 客户端据此重试得到的结果会与"请求写错了"区分开。
func rejectRequestStatus(c *gin.Context, inbound transformer.Inbound, status int, err error) {
	errType := "invalid_request_error"
	if status >= http.StatusInternalServerError {
		errType = "api_error"
	}
	response := inbound.TransformError(c.Request.Context(), &llm.ResponseError{
		StatusCode: status,
		Detail:     llm.ErrorDetail{Message: err.Error(), Type: errType},
	})
	c.Data(response.StatusCode, "application/json", response.Body)
	c.Abort()
}

// noAvailableMemberReason 说明此刻为什么一个成员都选不出来: 成员总数、冷却中的数量与最早恢复时间。
// 全部成员只是冷却时这是可等待的临时状态, 报文里给出恢复时间, 客户端据此决定什么时候重试。
func noAvailableMemberReason(group model.Group) string {
	if len(group.Items) == 0 {
		return fmt.Sprintf("group %q has no enabled member", group.Name)
	}

	state := RouteStateOf(group)
	now := time.Now().UnixMilli()
	cooling := 0
	var earliest int64
	for _, item := range group.Items {
		deadline, ok := state.Cooldowns[item.ID]
		if !ok || deadline <= now {
			continue
		}
		cooling++
		if earliest == 0 || deadline < earliest {
			earliest = deadline
		}
	}

	switch {
	case group.Mode == model.GroupModeManual:
		return fmt.Sprintf("group %q has no active member in manual mode (%d member(s) configured)", group.Name, len(group.Items))
	case cooling > 0:
		// 向上取整, 避免把 0.4 秒说成"0 秒后恢复"。
		return fmt.Sprintf("group %q has no available member: %d of %d member(s) cooling down, earliest recovers in %ds",
			group.Name, cooling, len(group.Items), (earliest-now+999)/1000)
	default:
		// 没有冷却中的成员却仍选不出目标: 冷却成员正被另一个请求占着探测名额。
		return fmt.Sprintf("group %q has no selectable member: %d member(s), a cooling member is being probed by another request",
			group.Name, len(group.Items))
	}
}
