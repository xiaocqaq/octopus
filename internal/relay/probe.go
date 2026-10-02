package relay

import (
	"context"
	"errors"
	"fmt"
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

// probeTimeout 是一次探测的**硬上限**。无论分组怎么配置, 超过 45 秒就直接判定为超时失败。
// 理由: 测活应当快速失败 —— 上游假死会把 120 秒(默认非流式超时)整得没动静,
// 用户点"立刻测一次"却要等两分钟, 体感远远超出"测活"这个动作该有的范围。
// 45 秒把"等待"拖到可接受的水平, 同时仍然留出余量应对慢响应(比如大模型首 token)。
// 取 min(分组配置, 45s): 不会放松已有的更严配置(如 30 秒), 只在默认 120 秒时收紧。
const probeTimeout = 45 * time.Second

// probeResultTTL 是**由指令触发的那类测活**的结论有效期, 也是所有没有自带有效期的结论的兜底值。
// 测活打的是真实上游, 结论只是"那一刻"的快照: 上游的限流, 余额, 网络抖动随时会变,
// 把几小时前的结论一直挂在界面上, 等于拿旧快照当现状看。
// 过期后结论既不展示也不参与选路, 要看就重新测活 —— 界面上那个徽标到点自己消失, 就是这条规则的可见形态。
//
// 定时测活不走这个常量: 它的有效期按所属任务的轮转周期算, 随结论发布(见 ProbeResult.ExpiresAt)。
// 那个周期是用户配出来的数, 与这个默认值没有必然关系 —— 从前拿它当唯一有效期, 配 30 分钟间隔的
// 任务就会"每 30 分钟里空 20 分钟", 看起来像结论丢了。
//
// 取 10 分钟而不是更短: 它是定时测活的默认间隔, 对没配任务的手动测活来说是个不松不紧的兜底。
//
// 与前端 PROBE_RESULT_TTL_MS(web/src/api/group.ts) 必须保持一致, 两侧各管一段:
// 后端保证"读出来就已经没有过期的结论"(刷新, 换设备, 新标签页都一致),
// 前端保证"页面开着不动时, 到点那个徽标自己消失"(不依赖后端推送)。
const probeResultTTL = 10 * time.Minute

// probeVoteDown 是测活不通过时给健康分的降档幅度。只降一档且不累积: 同一成员只会留一条最新结论,
// 反复测活失败不会越叠越低 —— "持续故障"的语义由真实调用失败的多次记录与冷却承担, 不靠这里叠数。
const probeVoteDown = -1

// probeFresh 判断一条测活结论是否仍在有效期内, now 为 Unix 毫秒。
//
// 结论自带失效时刻时以它为准(定时测活按任务轮转周期给出), 否则回落到 probeResultTTL。
// 兜底是必要的: 结论由多个入口写入, 指令触发的那条路没有任务可依, 也就不该硬套某个任务的节奏。
//
// 用"失效时刻 > now"而不是"now - 产生时间 < 有效期": 前者在系统时钟被往回调时同样成立,
// 不会把带未来时间戳的结论误判成过期(节点间时钟不齐或手动对时都会造成这种时间戳)。
func probeFresh(result ProbeResult, now int64) bool {
	if result.ExpiresAt > 0 {
		return result.ExpiresAt > now
	}
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

	request, err := buildProbeRequest(ctx, outbound, channel, channelModel.Name, probePrompt, probeMaxTokens, streaming)
	if err != nil {
		return recordProbe(group, itemID, false, err.Error(), 0), nil
	}

	// 超时按与转发同一套配置取值, 但不超过 45 秒硬上限: 见 probeTimeout 注释。
	timeout := time.Duration(group.RelayConfig.MemberNonStreamResponseTimeoutSeconds) * time.Second
	if streaming {
		timeout = time.Duration(group.RelayConfig.MemberStreamFirstEventTimeoutSeconds) * time.Second
	}
	if timeout > probeTimeout {
		timeout = probeTimeout
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

	targets := probeTargetsOf(group, itemIDs)
	if len(targets) == 0 {
		return []ProbeResult{}, nil
	}
	return probeByChannel(probeChannelIDsOf(group, targets), func(index int) ProbeResult {
		itemID := targets[index]
		result, err := ProbeItem(ctx, groupID, itemID, streaming)
		if err != nil {
			result = ProbeResult{GroupID: groupID, ItemID: itemID, Message: err.Error(), ProbedAt: time.Now().UnixMilli()}
		}
		return result
	}), nil
}

// probeTargetsOf 取本轮要探测的成员 ID: 未指定时取分组内全部, 指定时按提交顺序取,
// 且只保留确实属于该分组的成员 —— 传进来的 ID 可能来自已过期的界面状态。
func probeTargetsOf(group model.Group, itemIDs []int) []int {
	if len(itemIDs) == 0 {
		targets := make([]int, 0, len(group.Items))
		for _, item := range group.Items {
			targets = append(targets, item.ID)
		}
		return targets
	}
	targets := make([]int, 0, len(itemIDs))
	for _, itemID := range itemIDs {
		if itemOf(group, itemID).ID != 0 {
			targets = append(targets, itemID)
		}
	}
	return targets
}

// probeChannelIDsOf 取每个待测成员所属的渠道 ID, 供 probeByChannel 按渠道分批。
// 取不到授权的成员留 0: 它自己会被单独分到"0 号渠道"那一批里串行执行, 不会因此漏测 ——
// 它本来就该被测出一条"配置不完整"的结论, 而不是从批量里静默消失。
func probeChannelIDsOf(group model.Group, targets []int) []int {
	channelIDs := make([]int, len(targets))
	for index, itemID := range targets {
		if grant, err := op.ChannelGrantGet(itemOf(group, itemID).ChannelGrantID); err == nil && grant.ChannelModel != nil {
			channelIDs[index] = grant.ChannelModel.ChannelID
		}
	}
	return channelIDs
}

// buildProbeRequest 构造一次测活的上游请求。地址与认证取自出站转换器对占位请求的转换结果,
// 与 buildPassthroughRequest 同源, 使测活与转发的地址拼接规则不会分歧; 请求体由本函数自己造, 不依赖客户端请求。
//
// prompt 与 maxTokens 由调用方给定: 普通测活只要"上游尽快出声", 给 probePrompt/probeMaxTokens;
// 智商探针要模型完整作答, 给题面与 iqProbeMaxTokens。把这两项参数化而不是让本函数分辨测活类型,
// 是因为"发什么话"属于调用方的意图, 不属于地址与认证的拼接规则。
func buildProbeRequest(ctx context.Context, outbound transformer.Outbound, channel model.Channel, modelName string, prompt string, maxTokens int64, streaming bool) (*httpclient.Request, error) {
	streamFlag := streaming
	request, err := outbound.TransformRequest(ctx, &llm.Request{
		Model:     modelName,
		Messages:  []llm.Message{{Role: "user", Content: llm.MessageContent{Content: stringPtr(prompt)}}},
		Stream:    &streamFlag,
		MaxTokens: int64Ptr(maxTokens),
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

// runProbeBody 与 runProbe 走同两条发送路径, 但把非流式响应的正文带回来。
//
// 存在的理由: runProbe 及它的两条实现只回答"通不通", 流式下更是拿到首事件就关流,
// 正文从不流过手边。智商探针必须读到模型说的话才能判分, 于是需要一条"读完整正文"的路径。
//
// 只做非流式: 流式下要拼齐整个事件流才能得到完整回答, 而探针用非流式一次拿全更简单可靠 ——
// 定时测活本来就是非流式(见 runScheduledProbe), 这条限制不牺牲任何现有能力。
// streaming 为真时返回 nil 正文, 调用方据此把结论记为"未问出答案"而不是判错。
func runProbeBody(ctx context.Context, outbound transformer.Outbound, channel model.Channel, modelName string, request *httpclient.Request, passthrough, streaming bool) ([]byte, error) {
	if streaming {
		return nil, runProbe(ctx, outbound, channel, modelName, request, passthrough, streaming)
	}
	if passthrough {
		result, err := sendPassthrough(ctx, outbound.APIFormat(), request, channel, outbound, false, modelName)
		if err != nil {
			return nil, err
		}
		return result.body, nil
	}
	result, err := sendConverted(ctx, outbound.APIFormat(), request, channel, outbound, false)
	if err != nil {
		return nil, err
	}
	return result.body, nil
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

// ProbeChannelGrant 对一条当前渠道授权复用真实转发的出站构造和发送逻辑, 但不写入分组路由状态。
// 模型页测活尚未把模型加入分组, 因此不能借用 ProbeItem 的分组成员 ID；网络请求路径仍完全复用 runProbe。
func ProbeChannelGrant(ctx context.Context, grantID int, streaming bool) ProbeResult {
	grant, err := op.ChannelGrantGet(grantID)
	if err != nil {
		return ProbeResult{ItemID: grantID, Message: err.Error(), ProbedAt: time.Now().UnixMilli()}
	}
	channelModel := grant.ChannelModel
	channelKey := grant.ChannelKey
	if channelModel == nil || channelKey == nil {
		return ProbeResult{ItemID: grantID, Message: "channel grant is incomplete", ProbedAt: time.Now().UnixMilli()}
	}
	channel, err := op.ChannelGet(channelModel.ChannelID)
	if err != nil {
		return ProbeResult{ItemID: grantID, Message: err.Error(), ProbedAt: time.Now().UnixMilli()}
	}
	if !channel.Enabled {
		return ProbeResult{ItemID: grantID, Message: "channel is disabled", ProbedAt: time.Now().UnixMilli()}
	}
	outbound, _, passthrough, err := buildOutbound(channel, grant, *channelKey, model.ProtocolOpenAIChatCompletion)
	if err != nil {
		return ProbeResult{ItemID: grantID, Message: err.Error(), ProbedAt: time.Now().UnixMilli()}
	}
	request, err := buildProbeRequest(ctx, outbound, channel, channelModel.Name, probePrompt, probeMaxTokens, streaming)
	if err != nil {
		return ProbeResult{ItemID: grantID, Message: err.Error(), ProbedAt: time.Now().UnixMilli()}
	}
	config := model.DefaultGroupRelayConfig()
	timeout := time.Duration(config.MemberNonStreamResponseTimeoutSeconds) * time.Second
	if streaming {
		timeout = time.Duration(config.MemberStreamFirstEventTimeoutSeconds) * time.Second
	}
	if timeout > probeTimeout {
		timeout = probeTimeout
	}
	probeCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	startedAt := time.Now()
	probeErr := runProbe(probeCtx, outbound, channel, channelModel.Name, request, passthrough, streaming)
	latency := time.Since(startedAt).Milliseconds()
	if probeErr != nil {
		message := probeErr.Error()
		if errors.Is(probeCtx.Err(), context.DeadlineExceeded) {
			message = fmt.Sprintf("probe timeout after %s", timeout)
		}
		return ProbeResult{ItemID: grantID, LatencyMS: latency, Message: message, ProbedAt: time.Now().UnixMilli()}
	}
	return ProbeResult{ItemID: grantID, OK: true, LatencyMS: latency, ProbedAt: time.Now().UnixMilli()}
}

func stringPtr(value string) *string { return &value }

func int64Ptr(value int64) *int64 { return &value }

// recordProbe 把一次人工测活的结论落进路由状态, 返回给调用方的完整结论(含耗时与消息)。
// 失败不冷却: 人工测活是"此刻通不通"的一次快照, 未必是持续故障, 直接冷却会让一次误判把成员关进小黑屋。
// 定时测活走 landProbe(..., true), 那里失败即按配置冷却 —— 它是持续监控的一环, 语义与一次性体检不同。
//
// 有效期与定时测活共用同一条规则(见 withProbeExpiry): 有任务在监控这条授权时,
// 用户手动测出来的结论同样挂到下一轮复测之前, 而不是被一个与本次测试无关的短时限提前收走。
func recordProbe(group model.Group, itemID int, ok bool, message string, latencyMS int64) ProbeResult {
	return landProbe(group, itemID, withProbeExpiry(ProbeResult{
		GroupID:   group.ID,
		ItemID:    itemID,
		OK:        ok,
		LatencyMS: latencyMS,
		Message:   message,
		ProbedAt:  time.Now().UnixMilli(),
	}, itemOf(group, itemID).ChannelGrantID), false)
}

// landProbe 把一条测活结论落进分组的路由状态, 返回给调用方的完整结论。
//
// 健康优先的落点不在这里, 而在 probeVote: 测活调通的成员在选路时按满档上浮, 失败的沉一档,
// 未测活或结论已过期的保持 0 分; orderGroupItems 按"基础分 + 有效期内的测活加权"降序排,
// 于是"体检合格的排在前面"自然成立, 且不改写人工排定的 Priority(Priority 仍是同分时的次序)。
//
// 加权故意不写进 route.Scores: 结论有 probeResultTTL 的有效期, 写进表里就还得在到期时回滚,
// 而回滚必然与真实调用升降的健康分打架(同一张表, 分不清哪一档是谁加的);
// 现算则到期自然归零, 也不需要在后台跑定时器清理。
//
// 与 recordRouteSuccess/Failure 的差别: 测活是独立发起的一次尝试, 不代表"当前路由"调通了,
// 故一律不动 CurrentItemID 与亲和 —— 那两样属于正在承载客户端请求的那条路由。
//
// cooldownOnFailure 是两种调用方唯一的差别: 人工测活(false)失败只沉降不冷却, 定时测活(true)失败即冷却让位。
func landProbe(group model.Group, itemID int, result ProbeResult, cooldownOnFailure bool) ProbeResult {
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

	applyProbeLocked(route, group, itemID, result, cooldownOnFailure)
	route.Probes[itemID] = result
	publishRouteLocked(route)
	return result
}

// applyProbeLocked 按测活结论调整成员的健康分与冷却; 调用方必须持有锁, 并负责写入结论与发布状态。
func applyProbeLocked(route *RouteState, group model.Group, itemID int, result ProbeResult, cooldownOnFailure bool) {
	if result.OK {
		// 调通即解除冷却与强制失败的旧账, 并清零连续成功计数: 该成员已被证明可用, 无需再累计成功轮数。
		delete(route.Cooldowns, itemID)
		delete(route.pinnedFailures, itemID)
		route.successes[itemID] = 0
		if route.ProbeItemID == itemID {
			route.ProbeItemID = 0
		}
		// 体检通过就抵掉一档负分: 与真实成功同一条规则(见 recordRouteSuccess)。
		// 少了这一步, 之前因失败沉下去的分会一直压在底下, 而测活给的那点加权只有一个结论有效期。
		if baseScore(route, itemID, result.ProbedAt) < 0 {
			markScore(route, itemID, result.ProbedAt, 1)
		} else if route.Scores[itemID] < 0 {
			// 体检说它好, 就把那份已经衰减到 0 的负分旧账一并销掉: 留着会让它下一次失败被当成惯犯多压一档。
			delete(route.Scores, itemID)
			delete(route.ScoreAt, itemID)
		}
		return
	}

	// 失败一律打断连续成功计数。
	route.successes[itemID] = 0
	if !cooldownOnFailure {
		// 人工测活只沉降不分, 也不直接冷却: 让它在顺序上主动让位给体检合格的成员即可。
		return
	}

	// 定时测活失败即冷却: 这是用户设的持续监控, 结论就是"这一刻它不通", 继续把它排在选路前面没有依据。
	// 冷却时长沿用分组自己的配置, 与真实调用失败同一个口径, 不另立一套时长。
	route.Cooldowns[itemID] = result.ProbedAt + int64(group.RelayConfig.MemberCooldownSeconds)*1000
	if route.ProbeItemID == itemID {
		route.ProbeItemID = 0
	}
	// 不再叠一档基础负分: 这次失败已经通过有效期内的测活加权(probeVote)让它在选路上沉了一档,
	// 再压一档等于对同一次失败罚两次, 而基础分在冷却到期后还会继续压着它 ——
	// 一次瞬时抖动不该留下比冷却时长更久的后果。
}
