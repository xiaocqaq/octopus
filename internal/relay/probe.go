package relay

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/bestruirui/octopus/internal/model"
	"github.com/bestruirui/octopus/internal/op"
	"github.com/looplj/axonhub/llm"
	"github.com/looplj/axonhub/llm/httpclient"
	"github.com/looplj/axonhub/llm/transformer"
)

// probePrompt 是测活请求的提示词。走最小代价路径: 让上游尽快产生首个有效响应即可,
// 不追求内容, 故给出的是一句必然立即作答的短指令。
const probePrompt = "hi"

// probeMaxTokens 限制测活响应长度, 把一次测活的成本压到最低。
// 给一个小而上限而不设为 1: 上限过小会让部分推理模型直接报参数错误,
// 那验的就不是通道可用性而是本地参数合法性了。
const probeMaxTokens = 16

// probeConcurrency 是一键测活的并发上限。测活要打真实上游, 全部成员一次性并发会同时占用多条连接,
// 既可能触发上游的并发限制, 也会让所有成员在同一瞬间争用同一份配额; 4 条并发足以把等待时间压到可接受。
const probeConcurrency = 4

// probeResultTTL 是一次人工测活结论的有效期。测活打的是真实上游, 结论只是"那一刻"的快照:
// 上游的限流, 余额, 网络抖动随时会变, 把几小时前的结论一直挂在界面上, 等于拿旧快照当现状看。
// 过期后结论既不展示也不参与选路, 要看就重新测活 —— 界面上那个徽标到点自己消失, 就是这条规则的可见形态。
//
// 与前端 PROBE_RESULT_TTL_MS(web/src/api/group.ts) 必须保持一致, 两侧各管一段:
// 后端保证"读出来就已经没有过期的结论"(刷新, 换设备, 新标签页都一致),
// 前端保证"页面开着不动时, 到点那个徽标自己消失"(不依赖后端推送)。
const probeResultTTL = 5 * time.Minute

// probeVoteDown 是测活不通过时给健康分的降档幅度。只降一档且不累积: 同一成员只会留一条最新结论,
// 反复测活失败不会越叠越低 —— "持续故障"的语义由真实调用失败的多次记录与冷却承担, 不靠这里叠数。
const probeVoteDown = -1

// probeFresh 判断一条测活结论是否仍在有效期内, now 为 Unix 毫秒。
// 用"产生时间 + 有效期 > now"而不是"now - 产生时间 < 有效期": 前者在系统时钟被往回调时同样成立,
// 不会把带未来时间戳的结论误判成过期(节点间时钟不齐或手动对时都会造成这种时间戳)。
func probeFresh(result ProbeResult, now int64) bool {
	return result.ProbedAt+probeResultTTL.Milliseconds() > now
}

// ProbeItem 人工探测分组内单个成员是否可用, 并把结论落在路由状态里供选路与界面消费。
// 这是与转发完全独立的一次上游调用: 不占客户端的路由, 也不占冷却到期后的探测名额
// (那个名额管的是"放行一个真实请求去试探", 而人工测活本身就是明确的一次性尝试)。
// 返回的 error 只表示"这次测活没能发起"(分组或成员不存在); 上游不通属于结论, 由 ProbeResult 表达。
func ProbeItem(ctx context.Context, groupID, itemID int, streaming bool) (ProbeResult, error) {
	group, err := op.GroupGet(groupID)
	if err != nil {
		return ProbeResult{}, err
	}
	item := itemOf(group, itemID)
	if item.ID == 0 {
		return ProbeResult{}, fmt.Errorf("group item not found")
	}

	// 与转发共用同一条取授权路径: 渠道或凭据被禁用, 两侧缺失时在此就得到可读的错误,
	// 无需再逐项检查, 也不必等上游超时。
	grant, err := op.ChannelGrantGet(item.ChannelGrantID)
	if err != nil {
		return recordProbe(group, itemID, false, err.Error(), 0), nil
	}
	// ChannelGrantGet 成功时两侧必然非空, 此处直接取用。
	channelModel := grant.ChannelModel
	channelKey := grant.ChannelKey

	channel, err := op.ChannelGet(channelModel.ChannelID)
	if err != nil {
		return recordProbe(group, itemID, false, err.Error(), 0), nil
	}

	// 协议按转发时的首选规则选, 保证测的就是转发会走的那条路。
	outbound, _, passthrough, err := buildOutbound(channel, grant, *channelKey, model.ProtocolOpenAIChatCompletion)
	if err != nil {
		return recordProbe(group, itemID, false, err.Error(), 0), nil
	}

	request, err := buildProbeRequest(ctx, outbound, channel, channelModel.Name, streaming)
	if err != nil {
		return recordProbe(group, itemID, false, err.Error(), 0), nil
	}

	// 超时按与转发同一套配置取值: 非流式等完整响应, 流式等首个事件。
	timeout := time.Duration(group.RelayConfig.MemberNonStreamResponseTimeoutSeconds) * time.Second
	if streaming {
		timeout = time.Duration(group.RelayConfig.MemberStreamFirstEventTimeoutSeconds) * time.Second
	}
	probeCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	startedAt := time.Now()
	probeErr := runProbe(probeCtx, outbound, channel, channelModel.Name, request, passthrough, streaming)
	latency := time.Since(startedAt).Milliseconds()

	if probeErr != nil {
		// 超时单独标注: "上游没响应"与"上游报了错"是两种不同的结论, 界面上要能区分。
		message := probeErr.Error()
		if errors.Is(probeCtx.Err(), context.DeadlineExceeded) {
			message = fmt.Sprintf("probe timeout after %s", timeout)
		}
		return recordProbe(group, itemID, false, message, latency), nil
	}
	return recordProbe(group, itemID, true, "", latency), nil
}

// ProbeGroup 一键测活分组内的全部成员(或指定子集), 返回每个成员的结论, 顺序与目标顺序一致。
// 并发受 probeConcurrency 限制: 顺序执行会让成员多的分组等上几十秒, 全并发又会同时打满上游。
// 单个成员失败不影响其余成员: 测活的意义就是逐个给出结论, 一个不通不该中断整轮体检。
func ProbeGroup(ctx context.Context, groupID int, itemIDs []int, streaming bool) ([]ProbeResult, error) {
	group, err := op.GroupGet(groupID)
	if err != nil {
		return nil, err
	}

	// 未指定成员时探测全部; 指定时按提交顺序探测, 且只探测确实属于该分组的成员。
	targets := make([]int, 0, len(group.Items))
	if len(itemIDs) == 0 {
		for _, item := range group.Items {
			targets = append(targets, item.ID)
		}
	} else {
		for _, itemID := range itemIDs {
			if itemOf(group, itemID).ID != 0 {
				targets = append(targets, itemID)
			}
		}
	}
	if len(targets) == 0 {
		return []ProbeResult{}, nil
	}

	results := make([]ProbeResult, len(targets))
	var waitGroup sync.WaitGroup
	slots := make(chan struct{}, probeConcurrency)
	for i, itemID := range targets {
		waitGroup.Add(1)
		go func(index, id int) {
			defer waitGroup.Done()
			slots <- struct{}{}
			defer func() { <-slots }()
			result, err := ProbeItem(ctx, groupID, id, streaming)
			if err != nil {
				// 分组本身消失才会走到这里, 同样给出一条结论而不是让整个批量请求失败。
				result = ProbeResult{GroupID: groupID, ItemID: id, Message: err.Error(), ProbedAt: time.Now().UnixMilli()}
			}
			results[index] = result
		}(i, itemID)
	}
	waitGroup.Wait()
	return results, nil
}

// buildProbeRequest 构造一次测活的上游请求。地址与认证取自出站转换器对占位请求的转换结果,
// 与 buildPassthroughRequest 同源, 使测活与转发的地址拼接规则不会分歧; 请求体由本函数自己造, 不依赖客户端请求。
func buildProbeRequest(ctx context.Context, outbound transformer.Outbound, channel model.Channel, modelName string, streaming bool) (*httpclient.Request, error) {
	streamFlag := streaming
	request, err := outbound.TransformRequest(ctx, &llm.Request{
		Model:     modelName,
		Messages:  []llm.Message{{Role: "user", Content: llm.MessageContent{Content: stringPtr(probePrompt)}}},
		Stream:    &streamFlag,
		MaxTokens: int64Ptr(probeMaxTokens),
	})
	if err != nil {
		return nil, fmt.Errorf("resolve upstream endpoint: %w", err)
	}
	if err := applyChannelConfig(channel, request); err != nil {
		return nil, err
	}
	return request, nil
}

// runProbe 按是否同协议透传选择发送路径。两条路都与转发共用同一份实现, 测的正是转发会走的那条。
func runProbe(ctx context.Context, outbound transformer.Outbound, channel model.Channel, modelName string, request *httpclient.Request, passthrough, streaming bool) error {
	if passthrough {
		return probePassthrough(ctx, outbound, channel, modelName, request, streaming)
	}
	return probeConverted(ctx, outbound, channel, request, streaming)
}

// probePassthrough 以同协议透传方式发一次测活请求并判定成败。
// 直接复用 sendPassthrough / sendPassthroughStream: 地址与认证的拼接规则, 以及
// "HTTP 200 但正文是错误终态"的识别都在那里, 测活要验的就是这条路径。
func probePassthrough(ctx context.Context, outbound transformer.Outbound, channel model.Channel, modelName string, request *httpclient.Request, streaming bool) error {
	result, err := sendPassthrough(ctx, outbound.APIFormat(), request, channel, outbound, streaming, modelName)
	if err != nil {
		return err
	}
	if streaming {
		// 首事件到手即证上游在正常响应; 关掉事件流并归还独占客户端, 测活不消费后续正文。
		if result.events != nil {
			result.events.Close()
		}
		if result.closeIdle != nil {
			result.closeIdle()
		}
	}
	return nil
}

// probeConverted 以跨协议转换方式发一次测活请求。
// 直接复用 sendConverted: 协议转换, 渠道参数覆盖与终态校验都在那里, 测活要验的就是那条路径。
func probeConverted(ctx context.Context, outbound transformer.Outbound, channel model.Channel, request *httpclient.Request, streaming bool) error {
	result, err := sendConverted(ctx, outbound.APIFormat(), request, channel, outbound, streaming)
	if err != nil {
		return err
	}
	if streaming {
		// 首事件到手即证上游在响应; 关掉事件流并归还独占客户端, 测活不消费后续正文。
		if result.events != nil {
			result.events.Close()
		}
		if result.closeIdle != nil {
			result.closeIdle()
		}
	}
	return nil
}

func stringPtr(value string) *string { return &value }

func int64Ptr(value int64) *int64 { return &value }

// recordProbe 把一次测活的结论落进路由状态, 返回给调用方的完整结论(含耗时与消息)。
//
// 健康优先的落点不在这里, 而在 probeVote: 测活调通的成员在选路时按满档上浮, 失败的沉一档,
// 未测活或结论已过期的保持 0 分; orderGroupItems 按"基础分 + 有效期内的测活加权"降序排,
// 于是"体检合格的排在前面"自然成立, 且不改写人工排定的 Priority(Priority 仍是同分时的次序)。
//
// 加权故意不写进 route.Scores: 结论有 5 分钟有效期, 写进表里就还得在到期时回滚,
// 而回滚必然与真实调用升降的健康分打架(同一张表, 分不清哪一档是谁加的);
// 现算则到期自然归零, 也不需要在后台跑定时器清理。
//
// 与 recordRouteSuccess/Failure 的差别: 测活是人工发起的独立尝试, 不代表"当前路由"调通了,
// 故一律不动 CurrentItemID 与亲和 —— 那两样属于正在承载客户端请求的那条路由。
func recordProbe(group model.Group, itemID int, ok bool, message string, latencyMS int64) ProbeResult {
	result := ProbeResult{
		GroupID:   group.ID,
		ItemID:    itemID,
		OK:        ok,
		LatencyMS: latencyMS,
		Message:   message,
		ProbedAt:  time.Now().UnixMilli(),
	}

	// 手动模式没有选路队列, 健康分不参与决策; 结论仍然记下供界面显示最近一次体检结果。
	routeMu.Lock()
	defer routeMu.Unlock()

	route := routes[group.ID]
	if route == nil {
		route = newRouteState(group.ID)
		routes[group.ID] = route
	}

	if group.Mode == model.GroupModeManual {
		// 只落结论, 不推路由增量: 手动模式的路由增量会带出 current_item_id=0, 把界面上"正在使用哪个成员"
		// 冲掉。结论由 handler 随完整分组一并广播(changed 事件里的 runtime 已含 Probes)。
		route.Probes[itemID] = result
		return result
	}

	if ok {
		// 调通即解除冷却与强制失败的旧账, 并清零连续成功计数: 该成员已被证明可用, 无需再累计成功轮数。
		delete(route.Cooldowns, itemID)
		delete(route.pinnedFailures, itemID)
		route.successes[itemID] = 0
		if route.ProbeItemID == itemID {
			route.ProbeItemID = 0
		}
	} else {
		// 失败只沉降不分, 也不直接冷却: 测活失败是"此刻不通", 未必是持续故障,
		// 直接冷却会让一次误判把成员关进小黑屋; 让它在顺序上主动让位给体检合格的成员即可。
		route.successes[itemID] = 0
	}
	route.Probes[itemID] = result
	publishRouteLocked(route)
	return result
}
