package relay

import (
	"maps"
	"sort"
	"sync"
	"time"

	"github.com/bestruirui/octopus/internal/model"
)

// RouteState 是一个分组的进程内路由状态; 跨该分组的全部请求共享。
// 同时作为路由流的消息形状与分组读取响应中的 runtime 字段: 冷却, 探测与亲和都是本包路由算法的概念,
// 故状态形状由本包定义, 分组的持久化配置不含它; 内部标志未导出, 不会随消息出到 JSON。
// 两种模式共用 CurrentItemID: 手动模式下即人工指定的成员, 故障转移模式下由路由决定,
// 前端由此只读这一个字段即可知道当前承载请求的成员, 无需再按模式分支。
type RouteState struct {
	GroupID       int           `json:"group_id"`        // 状态所属的分组 ID, 供状态流按分组定位。
	CurrentItemID int           `json:"current_item_id"` // 当前承载请求的成员 ID, 0 表示尚未建立路由或未人工指定。
	ProbeItemID   int           `json:"probe_item_id"`   // 当前占用恢复探测的成员 ID, 同一分组同时只允许一个成员被探测; 手动模式恒为 0。
	AffinityUntil int64         `json:"affinity_until"`  // 当前路由的亲和截止 Unix 毫秒时间, 0 表示无亲和; 手动模式恒为 0。
	Cooldowns     map[int]int64 `json:"cooldowns"`       // 失败成员 ID 对应的冷却截止 Unix 毫秒时间, 已到期的条目由前端按当前时间忽略。
	// Scores 是成员 ID 对应的基础健康分: 正分在选路时上浮, 负分下沉, 0 表示按配置优先级。
	// 由调用结果自动升降而来, 与冷却一样只存在于本进程, 不改写人工排定的 Priority。
	// 表里存的是"变动当时"的加减值, 每一档都有寿命(routeScoreStepTTL): 展示与选路都用
	// baseScore 现算衰减后的当前值 —— 上游会恢复, 分数不该被永久钉住。
	// 发布出去的 JSON 带的是"已经衰减到发布时刻的值"与它的计时起点 ScoreAt(前端据此把剩余档位走完)。
	Scores map[int]int `json:"scores"`
	// ScoreAt 是 Scores 里每个成员的计时起点(Unix 毫秒): 从该时刻起每过 routeScoreStepTTL 就再少一档。
	// 没有条目表示该分数不衰减(手工构造的状态或旧数据), 界面上直接按原值显示。
	ScoreAt map[int]int64 `json:"score_at"`
	// Probes 是成员 ID 对应的最近一次人工测活结论, 供界面展示"体检结果"。
	// 与 Scores 分开保存: Scores 还会被真实调用结果升降, 而 Probes 只由人工测活写入,
	// 界面据此区分"这条结论来自我的体检"还是"来自线上调用"。结论只有 probeResultTTL 的有效期,
	// 过期即视为没有结论(既不展示也不参与选路), 要看就重新测活。
	Probes map[int]ProbeResult `json:"probes"`

	affinityArmed bool        // 当前路由下一次成功后是否开始亲和, 仅故障切换后为真。
	successes     map[int]int // 各成员当前的连续成功轮数, 满升档阈值后清零; 内部计数, 不出 JSON。
	// pinnedFailures 是强制优先的成员当前的连续失败次数, 跨请求累计。常规成员在一个请求内失败到总尝试次数就让位,
	// 强制成员改用这份计数: 连续失败超过阈值才冷却降档, 任意一次调通即清零。内部计数, 不出 JSON。
	pinnedFailures map[int]int
}

// ProbeResult 是一次人工测活的结论。测活不经过客户端请求, 故不写请求日志也不计入调用统计,
// 只把结论落在路由状态里, 由选路与界面各自消费。
// 结论形状与状态同处本包定义: 它是路由算法的一个输入, 分组的持久化配置不含它。
type ProbeResult struct {
	GroupID   int    `json:"group_id"`   // 结论所属分组。
	ItemID    int    `json:"item_id"`    // 被探测的成员 ID。
	OK        bool   `json:"ok"`         // 本轮是否调通。
	LatencyMS int64  `json:"latency_ms"` // 从发起到收到有效响应的耗时毫秒数; 失败时为耗时直到报错。
	Message   string `json:"message"`    // 成功时为空, 失败时为上游错误正文或本地配置错误。
	ProbedAt  int64  `json:"probed_at"`  // 结论产生时间, Unix 毫秒。
}

// routeScoreMax 是健康分相对配置优先级的最大偏移, 每一档相当于越过一个成员的位置。
// 设上限而非任其累加: 长期顺风的成员不该把人工排定的顺序彻底压住, 一时的故障也总有翻回来的余量。
const routeScoreMax = 3

// routeSuccessStreak 是升一档所需的连续成功轮数。
// 降档不需要另一个阈值: 触发降档的进入冷却本就已经是多次失败的结果。
const routeSuccessStreak = 3

// routeScoreStepTTL 是一档健康分的寿命: 从该成员最近一次分数变动算起, 每过这段时间就有一档向 0 走。
// 健康分要回答的是"最近这段时间谁可靠", 不是给成员下判决: 上游的限流、余额、网络都会恢复,
// 而负数一旦被永久钉住, 成员会一直排在后面 —— 排后面就拿不到成功, 也就永远抵消不掉那一档(自锁)。
// 因此每一档都有寿命: 最重的 -3 最多 3 个周期(默认 15 分钟)回到中性, 之后重新按配置优先级排队。
// 反过来说, 靠连续成功挣来的正分同样会过期: 一直顺畅的成员会被新的成功不断续期, 闲下来就自然回落到配置顺序。
// 与前端 SCORE_STEP_TTL_MS(web/src/api/group.ts) 必须一致(前端据此把界面上那一档按同一个节奏走完)。
const routeScoreStepTTL = 5 * time.Minute

// routePinnedFailureThreshold 是强制优先的成员进入冷却前可容忍的连续失败次数。
// 常规成员失败到配置的总尝试次数(默认 2)就让位, 强制成员要连续失败超过该次数才冷却降档:
// 人工钉住就是"宁可多试也用它", 阈值太低等于指定没生效, 太高则会把请求长时间耗在一个确实不可用的成员上。
const routePinnedFailureThreshold = 5

const routeStreamBuffer = 16 // 单个路由流连接的非阻塞消息缓冲容量。

var (
	routeMu      sync.Mutex                           // routeMu 保护全部分组路由状态。
	routes       = make(map[int]*RouteState)          // routes 按分组 ID 保存路由状态。
	routeStreams = make(map[chan RouteState]struct{}) // 全部路由 SSE 连接。
)

// RouteStateOf 返回分组当前的实时路由状态, 供读取接口随分组一并返回。
// 手动模式没有进程内路由: 当前成员即人工指定的成员, 冷却与亲和均不适用, 故直接由分组配置得出。
func RouteStateOf(group model.Group) RouteState {
	if group.Mode == model.GroupModeManual {
		// 手动模式没有选路队列, 但体检结论照存: "这个成员此刻通不通"与路由模式无关, 界面上两种模式都要看到结论。
		// 取的是同一份 Probes, 故切模式不会出现两套结论; 同样只给有效期内的那些。
		routeMu.Lock()
		probes := map[int]ProbeResult{}
		if route := routes[group.ID]; route != nil {
			probes = freshProbes(route, time.Now().UnixMilli())
		}
		routeMu.Unlock()

		return RouteState{
			GroupID:       group.ID,
			CurrentItemID: group.ActiveItemID,
			Cooldowns:     map[int]int64{},
			Scores:        map[int]int{},
			ScoreAt:       map[int]int64{},
			Probes:        probes,
		}
	}

	routeMu.Lock()
	defer routeMu.Unlock()

	route := routes[group.ID]
	if route == nil {
		return RouteState{GroupID: group.ID, Cooldowns: map[int]int64{}, Scores: map[int]int{}, ScoreAt: map[int]int64{}, Probes: map[int]ProbeResult{}}
	}
	state := *route
	now := time.Now().UnixMilli()
	state.Cooldowns = maps.Clone(route.Cooldowns)
	state.Scores, state.ScoreAt = baseScoresAt(route, now)
	state.Probes = freshProbes(route, now)
	return state
}

// ResetRouteState 丢弃分组的进程内路由状态, 用于分组切换选择模式或被删除。
// 不丢弃的话冷却与亲和会在 failover 切到 manual 再切回来之后复活并继续影响选路, 分组删除后其状态也会永久残留。
func ResetRouteState(groupID int) {
	routeMu.Lock()
	defer routeMu.Unlock()

	delete(routes, groupID)
}

// pickGroupItem 按分组模式选择本轮目标成员, 没有可用成员时返回零值; group.Items 已按 Priority 升序排列。
// 故障转移模式的遍历顺序还要叠上健康分: 连续成功的成员上浮, 进过冷却的成员下沉, 由此把调用顺畅的成员稳定排在前面。
// 渠道是否可用不在此判断: 渠道禁用或缺少密钥由调用方发现并作为一轮失败上报, 该成员随即进入冷却而在后续轮次被跳过。
func pickGroupItem(group model.Group) model.GroupItem {
	if group.Mode == model.GroupModeManual {
		for _, item := range group.Items {
			if item.ID == group.ActiveItemID {
				return item
			}
		}
		return model.GroupItem{}
	}

	routeMu.Lock()
	defer routeMu.Unlock()

	route := groupRouteLocked(group)
	now := time.Now().UnixMilli()
	if route.AffinityUntil <= now {
		route.AffinityUntil = 0
	}

	// 强制成员优先于优先级顺序与亲和: 只要它不在冷却中就一直选它。
	// 冷却中则照常按优先级选路, 使强制不至于把请求一直压在一个已经不可用的成员上;
	// 冷却到期后它重新胜出, 由此无需额外的切回逻辑。
	// 它进入冷却的门槛比常规成员高得多: 要连续失败超过 routePinnedFailureThreshold 次(见 recordRouteFailure)。
	if group.PinnedItemID != 0 {
		if item := itemOf(group, group.PinnedItemID); item.ID != 0 {
			deadline, cooling := route.Cooldowns[group.PinnedItemID]
			if !cooling || deadline <= now {
				changed := false
				// 冷却已到期的强制成员直接恢复使用, 不占用探测名额: 强制的语义就是尽快回到该成员。
				// 到期条目须显式删除: recordRouteSuccess 只为探测成员解除冷却, 不删则残留在冷却表里。
				if cooling {
					delete(route.Cooldowns, group.PinnedItemID)
					changed = true
				}
				if route.CurrentItemID != group.PinnedItemID {
					route.CurrentItemID = group.PinnedItemID
					route.AffinityUntil = 0
					changed = true
				}
				if changed {
					publishRouteLocked(route)
				}
				return item
			}
		}
	}

	// 亲和期内沿用当前成员, 不提前探测已恢复的高优先级成员。
	if route.CurrentItemID != 0 && route.AffinityUntil > now {
		return itemOf(group, route.CurrentItemID)
	}

	for _, item := range orderGroupItems(group, route) {
		// 遍历到当前成员说明排在它前面的成员都不可选, 沿用当前成员。
		if item.ID == route.CurrentItemID {
			break
		}
		deadline, cooling := route.Cooldowns[item.ID]
		if cooling && deadline > now {
			continue
		}
		// 冷却已到期的成员只放行一个探测请求, 避免全部请求同时涌向尚未恢复的成员。
		if cooling {
			if route.ProbeItemID != 0 {
				continue
			}
			route.ProbeItemID = item.ID
			publishRouteLocked(route)
			return item
		}
		route.CurrentItemID = item.ID
		publishRouteLocked(route)
		return item
	}
	if route.CurrentItemID != 0 {
		return itemOf(group, route.CurrentItemID)
	}
	return model.GroupItem{}
}

// recordRouteSuccess 上报一轮成功: 结束该成员的冷却与探测占用, 累计健康分, 并在故障切换后按配置开始亲和。
func recordRouteSuccess(group model.Group, itemID int) {
	if group.Mode == model.GroupModeManual {
		return
	}

	routeMu.Lock()
	defer routeMu.Unlock()

	route := routes[group.ID]
	if route == nil {
		return
	}
	now := time.Now().UnixMilli()
	changed := false

	// 探测成功说明该成员已恢复, 解除冷却; 若当前路由不在亲和期内则立即切回该成员。
	if route.ProbeItemID == itemID {
		route.ProbeItemID = 0
		delete(route.Cooldowns, itemID)
		if route.CurrentItemID == 0 || route.AffinityUntil <= now {
			route.CurrentItemID = itemID
			route.AffinityUntil = 0
		}
		changed = true
	}
	// 强制优先的成员只要调通一轮就算恢复: 连续失败清零, 之前因冷却扣掉的健康分抬回中性。
	// 只抬负分不动正分: 它此前靠连续成功挣来的位次不该被这一次抹掉, 本功能带来的降档才是要取消的。
	if group.PinnedItemID != 0 && itemID == group.PinnedItemID {
		if route.pinnedFailures[itemID] != 0 {
			delete(route.pinnedFailures, itemID)
			changed = true
		}
		if base := baseScore(route, itemID, now); base < 0 {
			markScore(route, itemID, now, -base)
			changed = true
		}
	}
	// 一次成功先抵掉一档负分: 恢复了就该立刻被认可, 不必再等连续三次。
	// 负分成员会被排到后面, 排后面就拿不到成功 —— 要求"连续成功三次"来抵消, 等于让它永远抵消不掉。
	// 判断看的是衰减后的当前值: 靠闲置已经熬回中性的成员, 这一步不该再额外送它一档正分。
	if baseScore(route, itemID, now) < 0 {
		markScore(route, itemID, now, 1)
		changed = true
	} else if route.Scores[itemID] < 0 {
		// 调通一轮就该把"惯犯"记录一并清掉: 负分已经衰减到 0 却还在表里留着旧值, 下一次失败会被当作
		// 重复失败而多压一档(见 recordRouteFailure), 那是拿一次成功之后的失败去惩罚已经作废的旧账。
		delete(route.Scores, itemID)
		delete(route.ScoreAt, itemID)
		changed = true
	}
	// 连续成功累计到阈值即升一档, 让稳定可用的成员逐渐排到配置顺序之前。
	route.successes[itemID]++
	if route.successes[itemID] >= routeSuccessStreak {
		route.successes[itemID] = 0
		if route.Scores[itemID] < routeScoreMax {
			markScore(route, itemID, now, 1)
			changed = true
		}
	}
	// 亲和只在故障切换后的首次成功时开始, 使请求在一段时间内稳定留在备用成员上。
	if route.CurrentItemID == itemID && route.affinityArmed {
		route.affinityArmed = false
		if group.RelayConfig.MemberAffinitySeconds > 0 {
			route.AffinityUntil = now + int64(group.RelayConfig.MemberAffinitySeconds)*1000
			changed = true
		}
	}
	if changed {
		publishRouteLocked(route)
	}
}

// recordRouteFailure 上报一轮失败: 达到配置的总尝试次数后将该成员打入冷却并让出当前路由, 返回是否已冷却。
// failures 为该成员在本请求内包含首次请求的连续失败次数, 由调用方累计。
func recordRouteFailure(group model.Group, itemID, failures int) bool {
	if group.Mode == model.GroupModeManual {
		return false
	}

	routeMu.Lock()
	defer routeMu.Unlock()

	route := routes[group.ID]
	if route == nil {
		return false
	}
	// 任意真实失败都会打断连续成功; 是否进入冷却仍由本请求内的失败次数决定。
	route.successes[itemID] = 0
	// 强制优先的成员不看本请求内的失败次数: 它的连续失败跨请求累计, 超过阈值才冷却降档。
	// 用累计而非单请求计数, 是因为"连续错误超过 5 次"说的是这个成员的健康状况, 不是某一次请求的重试次数;
	// 逐请求计数会在每个请求开头归零, 那样它永远攒不够阈值, 冷却与降档也就无从谈起。
	if group.PinnedItemID != 0 && itemID == group.PinnedItemID {
		route.pinnedFailures[itemID]++
		if route.pinnedFailures[itemID] <= routePinnedFailureThreshold {
			return false
		}
	} else if route.ProbeItemID != itemID && failures < group.RelayConfig.MemberMaxAttempts {
		// 探测请求只有一次机会, 常规成员达到配置的总尝试次数后进入冷却。
		return false
	}

	now := time.Now().UnixMilli()
	route.Cooldowns[itemID] = now + int64(group.RelayConfig.MemberCooldownSeconds)*1000
	// 需要冷却说明该成员已经连续失败到不值得再用, 顺手降一档: 冷却到期后它会带着这一档偏移重新排队,
	// 排到原本不如它的成员之后; 测活通过、任意一次真实成功(抵一档)与连续成功(升一档)再把它抬回来。
	// 这一档同样有寿命(routeScoreStepTTL): 一直失败的成员会被反复续期, 而不再失败的成员会自己走回中性。
	//
	// 惯犯加深: 表里的负分若已经过了寿命(此刻衰减到 0), 说明上次挨罚之后它又拿到过机会, 这次仍失败就不是偶发。
	// 一次只扣一档的话它会每过一个档位周期就回来撞一次, 长期坏掉的成员于是成了固定的空转开销;
	// 每重复一次多扣一档(-1 → -2 → -3), 再试一次的间隔随之变成 5 → 10 → 15 分钟。
	// 上限仍由 routeScoreMax 兜住: 无论多坏都要自己走回 0, 不允许变成永久判决。
	// 作为代价, 一个"挨罚后一直没流量(放够久)又失败"的成员也会被算作惯犯而多压一档 —— 二者在表里无法区分,
	// 但都确实"罚过又失败", 且任何一次成功或测活通过都会清掉这份记录(见 recordRouteSuccess / recordProbe)。
	delta := -1
	if recorded := route.Scores[itemID]; recorded < 0 && baseScore(route, itemID, now) == 0 {
		delta = recorded - 1
		if delta < -routeScoreMax {
			delta = -routeScoreMax
		}
	}
	markScore(route, itemID, now, delta)
	if route.ProbeItemID == itemID {
		route.ProbeItemID = 0
	}
	// 当前路由失败才需要下一个成员开始亲和; 独立探测失败不影响当前路由。
	if route.CurrentItemID == itemID {
		route.CurrentItemID = 0
		route.AffinityUntil = 0
		route.affinityArmed = true
	}
	publishRouteLocked(route)
	return true
}

// ReleaseItemCooldown 解除指定成员的冷却, 用于人工把它指定为强制成员。
// 人工指定是明确的"现在就用它"的意图, 若还被残留的冷却挡在门外, 这次指定就落不了地。
// 连续失败计数一并清零: 那是它上一轮被强制的旧账, 重新指定后不该让它更快让位。
// 已经指定但正在冷却的成员不在此列: 那种情况下的让位是刻意的, 见 pickGroupItem。
func ReleaseItemCooldown(groupID, itemID int) {
	if itemID == 0 {
		return
	}

	routeMu.Lock()
	defer routeMu.Unlock()

	route := routes[groupID]
	if route == nil {
		return
	}
	changed := false
	if route.pinnedFailures[itemID] != 0 {
		delete(route.pinnedFailures, itemID)
		changed = true
	}
	if _, cooling := route.Cooldowns[itemID]; cooling {
		delete(route.Cooldowns, itemID)
		changed = true
	}
	if changed {
		publishRouteLocked(route)
	}
}

// releaseRouteProbe 归还未产生成败结论的探测占用, 用于请求被人工中止或客户端断开。
func releaseRouteProbe(group model.Group, itemID int) {
	routeMu.Lock()
	defer routeMu.Unlock()

	if route := routes[group.ID]; route != nil && route.ProbeItemID == itemID {
		route.ProbeItemID = 0
		publishRouteLocked(route)
	}
}

// groupRouteLocked 取出分组路由状态并清理已删除成员的残留; 调用方必须持有锁。
func groupRouteLocked(group model.Group) *RouteState {
	route := routes[group.ID]
	if route == nil {
		route = newRouteState(group.ID)
		routes[group.ID] = route
	}
	items := make(map[int]bool, len(group.Items))
	for _, item := range group.Items {
		items[item.ID] = true
	}
	for itemID := range route.Cooldowns {
		if !items[itemID] {
			delete(route.Cooldowns, itemID)
		}
	}
	// 健康分与成功计数同冷却一样按成员索引, 成员被移除后留下的条目只会白占内存。
	for itemID := range route.Scores {
		if !items[itemID] {
			delete(route.Scores, itemID)
			delete(route.ScoreAt, itemID)
		}
	}
	for itemID := range route.Probes {
		if !items[itemID] {
			delete(route.Probes, itemID)
		}
	}
	for itemID := range route.successes {
		if !items[itemID] {
			delete(route.successes, itemID)
		}
	}
	for itemID := range route.pinnedFailures {
		if !items[itemID] {
			delete(route.pinnedFailures, itemID)
		}
	}
	if route.ProbeItemID != 0 && !items[route.ProbeItemID] {
		route.ProbeItemID = 0
	}
	if route.CurrentItemID != 0 && !items[route.CurrentItemID] {
		route.CurrentItemID = 0
		route.AffinityUntil = 0
		route.affinityArmed = false
	}
	return route
}

// newRouteState 建立分组的进程内路由状态, 各按成员索引的 map 一并就位, 写入侧无需再判空。
func newRouteState(groupID int) *RouteState {
	return &RouteState{
		GroupID:        groupID,
		Cooldowns:      make(map[int]int64),
		Scores:         make(map[int]int),
		ScoreAt:        make(map[int]int64),
		Probes:         make(map[int]ProbeResult),
		successes:      make(map[int]int),
		pinnedFailures: make(map[int]int),
	}
}

// 测活加权与健康分衰减都在下面几个函数里现算, 不在表里预先把结果写死。

// markScore 记录一次基础健康分变动: 先把"已经走过的档位"落定, 再叠上这一档, 并把计时起点重置为现在。
// 先落定是关键 —— 若直接拿表里的原始值相加, 一个闲置够久、实际已经回到中性的成员, 下一次失败会被
// 打回最重的那一档(原始的 -3 再减一), 等于它的时效白熬了。
// 每次变动都重置起点也是有意为之: 还在失败的成员应当继续被压着, 一直顺畅的成员也一直被新成功续期;
// 而一旦不再有新的结果, 这份分数就按 routeScoreStepTTL 一档一档走向 0, 不会永远钉住谁。
func markScore(route *RouteState, itemID int, now int64, delta int) {
	if delta == 0 {
		return
	}
	base, _ := baseScoreAt(route, itemID, now)
	next := base + delta
	if next > routeScoreMax {
		next = routeScoreMax
	}
	if next < -routeScoreMax {
		next = -routeScoreMax
	}
	if next == 0 {
		delete(route.Scores, itemID)
		delete(route.ScoreAt, itemID)
		return
	}
	route.Scores[itemID] = next
	route.ScoreAt[itemID] = now
}

// baseScoreAt 返回该成员基础分"此刻的值"以及这份值的计时起点(Unix 毫秒)。
// 从计时起点起再走满一个 routeScoreStepTTL 才会掉到下一档, 所以起点是"当前这一档的开始时刻",
// 而不是最初那次变动的时刻 —— 前端拿这两个数就能接着把剩下的档位按同一个节奏走完。
// 没有计时起点的条目(手工构造的状态或旧数据)按原值返回, 不衰减。
func baseScoreAt(route *RouteState, itemID int, now int64) (int, int64) {
	score := route.Scores[itemID]
	at := route.ScoreAt[itemID]
	if score == 0 || at == 0 {
		return score, 0
	}
	steps := int((now - at) / routeScoreStepTTL.Milliseconds())
	if steps <= 0 {
		return score, at
	}
	// 过期的档位一次走完, 但不越过 0: 分只会回到中性, 不会因为放置太久而反过来变成反向的分。
	if score > 0 {
		score -= steps
		if score < 0 {
			score = 0
		}
	} else {
		score += steps
		if score > 0 {
			score = 0
		}
	}
	return score, at + int64(steps)*routeScoreStepTTL.Milliseconds()
}

// baseScore 是成员基础健康分此刻的值, 供选路排序使用。
func baseScore(route *RouteState, itemID int, now int64) int {
	score, _ := baseScoreAt(route, itemID, now)
	return score
}

// baseScoresAt 导出两张表: 已按时间衰减到此刻的分数, 以及各自这一档的计时起点。
// 两者一起给出去, 前端才能在页面内把剩下的档位走完(只看分数、没有起点就只能等下一次刷新)。
func baseScoresAt(route *RouteState, now int64) (map[int]int, map[int]int64) {
	scores := make(map[int]int, len(route.Scores))
	scoreAt := make(map[int]int64, len(route.ScoreAt))
	for itemID := range route.Scores {
		score, at := baseScoreAt(route, itemID, now)
		if score == 0 {
			continue
		}
		scores[itemID] = score
		if at != 0 {
			scoreAt[itemID] = at
		}
	}
	return scores, scoreAt
}

// probeVote 是最近一次仍在有效期内的测活结论给选路的加权:
// 通过按满档上浮(与连续成功顶格同效), 失败沉一档, 没有结论或结论已过期都是 0。
//
// 现算而不写进 Scores 是这套语义的关键: 结论有 probeResultTTL 的有效期, 写进表里到期就得回滚,
// 而回滚分不清"这一档是测活加的"还是"真实调用加的", 必然误伤; 现算则到期自然归零, 也无需定时清理。
// 前端(PROBE_SCORE_MAX/PROBE_VOTE_DOWN, web/src/api/group.ts)按同一条规则算展示用的分,
// 两侧的档位常数必须一起改, 否则界面上的排名标记会与真实选路顺序不一致。
func probeVote(route *RouteState, itemID int, now int64) int {
	result, ok := route.Probes[itemID]
	if !ok || !probeFresh(result, now) {
		return 0
	}
	if result.OK {
		return routeScoreMax
	}
	return probeVoteDown
}

// effectiveScore 是成员当前真正用于选路与展示的健康分:
// 调用结果累积的基础分(已按时效衰减) 加上有效期内的测活加权。
func effectiveScore(route *RouteState, itemID int, now int64) int {
	return baseScore(route, itemID, now) + probeVote(route, itemID, now)
}

// freshProbes 只保留有效期内的测活结论: 过期的结论读出来就等于不存在, 想看就重新测活。
func freshProbes(route *RouteState, now int64) map[int]ProbeResult {
	probes := make(map[int]ProbeResult, len(route.Probes))
	for itemID, result := range route.Probes {
		if probeFresh(result, now) {
			probes[itemID] = result
		}
	}
	return probes
}

// orderGroupItems 按健康分与配置优先级共同排出选路顺序: 先比健康分降序, 同分再按 Priority 升序。
// 健康分的升降既来自真实调用结果, 也来自人工测活 —— 这是"健康的优先级更高"的落点:
// 测活通过的成员在结论有效期内被抬到满档, 于是体检合格的成员稳定排在未测活或测活失败的成员之前,
// 而全员的相对次序仍由 Priority 在同分时兜底, 人工排定的顺序不会被彻底推翻。
// 结论过期后加权自动归零, 顺序随之回到配置与调用结果决定的样子。
// 全部成员都无偏移时直接返回原序列: group.Items 已按 Priority 定序, 多排一次只会白花一次分配。
// 用稳定排序而非显式比较 Priority: 原序列本身即是 Priority 顺序, 稳定排序天然把它作为同分时的次序。
func orderGroupItems(group model.Group, route *RouteState) []model.GroupItem {
	now := time.Now().UnixMilli()
	ranked := false
	for _, item := range group.Items {
		if effectiveScore(route, item.ID, now) != 0 {
			ranked = true
			break
		}
	}
	if !ranked {
		return group.Items
	}
	ordered := make([]model.GroupItem, len(group.Items))
	copy(ordered, group.Items)
	sort.SliceStable(ordered, func(i, j int) bool {
		return effectiveScore(route, ordered[i].ID, now) > effectiveScore(route, ordered[j].ID, now)
	})
	return ordered
}

// itemOf 返回分组内指定 ID 的成员, 不存在时返回零值。
func itemOf(group model.Group, itemID int) model.GroupItem {
	for _, item := range group.Items {
		if item.ID == itemID {
			return item
		}
	}
	return model.GroupItem{}
}

// publishRouteLocked 非阻塞发布路由状态, 连接拥塞时关闭它并交给客户端重连获取全量快照; 冷却表, 健康分与体检结论按值复制以免前端读到后续变更; 调用方必须持有锁。
// 健康分发布"已经衰减到此刻的值 + 该档的计时起点", 体检结论只发有效期内的; 前端据此在页面内把剩余档位走完, 不必等下一次推送。
func publishRouteLocked(route *RouteState) {
	now := time.Now().UnixMilli()
	message := *route
	message.Cooldowns = maps.Clone(route.Cooldowns)
	message.Scores, message.ScoreAt = baseScoresAt(route, now)
	message.Probes = freshProbes(route, now)
	for stream := range routeStreams {
		select {
		case stream <- message:
		default:
			delete(routeStreams, stream)
			close(stream)
		}
	}
}

// OpenRouteStream 注册路由流连接, 返回后续增量通道。
// 不再返回快照: 分组读取接口已随分组带回当前路由状态, 前端由此拿到的初始值即全量, 连接只负责增量。
func OpenRouteStream() chan RouteState {
	routeMu.Lock()
	defer routeMu.Unlock()

	stream := make(chan RouteState, routeStreamBuffer)
	routeStreams[stream] = struct{}{}
	return stream
}

// CloseRouteStream 注销并关闭指定路由流连接。
func CloseRouteStream(stream chan RouteState) {
	routeMu.Lock()
	defer routeMu.Unlock()

	if _, exists := routeStreams[stream]; exists {
		delete(routeStreams, stream)
		close(stream)
	}
}
