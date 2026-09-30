package relay

import (
	"context"
	"fmt"
	"sync"
	"sync/atomic"
	"time"

	"github.com/bestruirui/octopus/internal/model"
	"github.com/bestruirui/octopus/internal/op"
	"github.com/charmbracelet/log"
)

// ScheduledProbeTickInterval 是调度器的基础节拍。
// 它不等于任务的探测间隔: 间隔记在任务里(分钟级), 每一拍只决定"现在该不该测"。
// 节拍取 10 秒, 是为了让 1 分钟的最小间隔与 ±30 秒的抖动都能落到秒级精度上;
// 每一拍只做几次内存判断, 一天 8640 次唤醒的代价可以忽略。
const ScheduledProbeTickInterval = 10 * time.Second

// scheduledProbeJitterMillis 是探测时刻的抖动幅度上限(±30 秒), 固定不可配。
// 抖动是为了避免多条任务长期挤在同一秒打上游 —— 默认间隔都是 10 分钟, 创建时间接近的任务会一直同步。
// 幅度固定而不做成配置项: 用户要的只是"别挤在一起", 一个常量就够, 多一个配置就多一处要解释的地方。
const scheduledProbeJitterMillis = 30_000

// scheduledProbeJitterPeriodDivisor 是抖动相对周期的上限倍数: 实际抖动不超过 周期 ÷ 4。
// 均摊之后周期可能只有几十秒, 此时固定的 ±30 秒足以盖过周期本身, 故按周期收敛。
const scheduledProbeJitterPeriodDivisor = 4

// scheduledProbeCycleUnit 是轮转周期与结论有效期的取值粒度: 掐到秒, 抹掉均摊除法的亚微秒余数。
const scheduledProbeCycleUnit = time.Second

// 抖动散列的两个乘数。
const (
	scheduledProbeHashID    = 2654435761 // 黄金比例散列乘数, 把相邻的任务主键打散到不同档位。
	scheduledProbeHashRound = 40503      // 轮数的乘数, 让同一条任务每一轮的偏移都换个位置。
)

// scheduledProbeState 是单条任务的调度进度, 只存在于进程内。
// 不落库: 进度是"下一次什么时候测"的运行期推算, 重启后从当前时刻重新起算即可;
// 落库反而要面对"停机期间积欠的探测要不要补"这种没有正确答案的问题。
type scheduledProbeState struct {
	nextDueAt    int64  // 允许探测的最早时刻, Unix 毫秒。
	creditCursor int    // 该任务内部轮转到的凭据下标(扁平凭据列表上的位置)。
	round        uint64 // 已完成轮数, 参与抖动计算, 使每一轮的偏移都不同。
}

var (
	scheduledProbeMu     sync.Mutex
	scheduledProbeStates = make(map[int]*scheduledProbeState)
	// lastProbedID 是 FIFO 轮转的游标: 下一拍从它之后的第一条任务开始找。
	// 记主键而不是下标: 任务的增删会让下标整体位移, 记主键则轮转顺序不因增删而错乱。
	lastProbedID int
	// scheduledProbeRunning 保证同一时刻只有一拍在跑: 单次探测最长可达分钟级, 而节拍只有 10 秒,
	// 少了这道闸, 上一拍还没测完下一拍就会挤进来并发探测, "一拍只测一条"也就不成立了。
	scheduledProbeRunning atomic.Bool
)

// ScheduledProbeTick 是调度器的一拍: 按 FIFO 轮转挑出下一条到点的任务, 只测它的一条授权。
//
// 一拍只测一条授权而不是把到点的任务全测一遍: 测活打的是真实上游, 一次唤醒里并发探测会在上游留下突发流量,
// 结论也不再是同一时刻的快照。轮转保证每条任务都会轮到, 只是到点后可能晚几拍才被执行。
func ScheduledProbeTick() {
	if !scheduledProbeRunning.CompareAndSwap(false, true) {
		return
	}
	defer scheduledProbeRunning.Store(false)

	now := time.Now()
	targets := op.ScheduledProbeTargets()
	pruneScheduledProbeStates(targets)

	target, ok := nextScheduledProbe(targets, now)
	if !ok {
		return
	}
	runScheduledProbe(target, now)
}

// ResetScheduledProbe 丢掉一条任务的调度进度, 让它的新配置在下一拍立即生效。
// 由增删改接口在写库成功后调用: 间隔被改小或从停用改回启用时, 若不重置, 任务会带着旧配置算出的
// 下一次探测时刻继续等下去 —— 用户看到的就是"改了配置但半天没反应"。
func ResetScheduledProbe(id int) {
	scheduledProbeMu.Lock()
	defer scheduledProbeMu.Unlock()

	delete(scheduledProbeStates, id)
}

// pruneScheduledProbeStates 丢掉已被删除的任务留下的进度, 否则任务反复增删会让这张表只增不减。
func pruneScheduledProbeStates(targets []model.ScheduledProbe) {
	scheduledProbeMu.Lock()
	defer scheduledProbeMu.Unlock()

	alive := make(map[int]bool, len(targets))
	for _, target := range targets {
		alive[target.ID] = true
	}
	for id := range scheduledProbeStates {
		if !alive[id] {
			delete(scheduledProbeStates, id)
		}
	}
}

// nextScheduledProbe 从上次测过的那条之后开始轮转, 返回第一条"已启用、在时间窗内且已到点"的任务。
// 跳过不等于放弃: 窗口外的任务只是这一拍不测, 窗口一开它仍是轮转里的候选, 且因为早已到点会被立即选中。
func nextScheduledProbe(targets []model.ScheduledProbe, now time.Time) (model.ScheduledProbe, bool) {
	start := scheduledProbeStartIndex(targets)
	for offset := range targets {
		target := targets[(start+offset)%len(targets)]
		if !target.Enabled || !target.WindowOpen(now) {
			continue
		}
		if scheduledProbeDue(target.ID, now) {
			return target, true
		}
	}
	return model.ScheduledProbe{}, false
}

// scheduledProbeStartIndex 返回轮转的起点下标: 第一条主键大于游标的任务; 没有则回到开头。
func scheduledProbeStartIndex(targets []model.ScheduledProbe) int {
	scheduledProbeMu.Lock()
	defer scheduledProbeMu.Unlock()

	for i, target := range targets {
		if target.ID > lastProbedID {
			return i
		}
	}
	return 0
}

// scheduledProbeDue 判断该任务这一拍是否到点。没有进度记录就算到点: 新建与刚重启的任务应当尽快测第一次,
// 而不是先等满一个间隔 —— 那会让"新建任务"看起来像没生效。
func scheduledProbeDue(id int, now time.Time) bool {
	scheduledProbeMu.Lock()
	defer scheduledProbeMu.Unlock()

	state, ok := scheduledProbeStates[id]
	return !ok || now.UnixMilli() >= state.nextDueAt
}

// runScheduledProbe 探测这条任务的下一条凭据, 并推进它的调度进度。
//
// 轮转是扁平的: 把"目标 → 凭据"两层摊平成一圈, 每一拍走一格(见 op.ScheduledProbeCredits)。
// 之所以不再按目标分两级走, 是因为配置的间隔要表达"每条凭据各测一次"这个周期:
// 两级取模会让凭据数不同的目标拿到相差数倍的周期(同一个任务里 3 条凭据的目标 90 分钟一轮、
// 1 条凭据的 30 分钟一轮), 用户配的那个数字就谁也对应不上。摊平之后每条凭据的周期
// 一律等于 间隔 ÷ 凭据数, 整齐且可预期, 界面上拖出来的顺序也才真的等于轮转顺序。
func runScheduledProbe(target model.ScheduledProbe, now time.Time) {
	credits := op.ScheduledProbeCredits(target)
	if len(credits) == 0 {
		// 没有可测凭据(理论上创建时已拦下, 但渠道改动、凭据被停用、或被逐行删空后可能落到这里):
		// 这不是"通道不通", 不该落失败结论, 只把下一次探测推后, 等用户把配置改回来。
		advanceScheduledProbe(target, now, false)
		return
	}

	credit := credits[scheduledProbeCreditCursor(target.ID)%len(credits)]
	// 非流式: 定时测活只问"这条通道此刻能不能出结果", 非流式响应体最短, 也不会占着上游连接等首字节;
	// 界面上那个徽标只看成败, 与流式与否无关。
	result := ProbeScheduledGrant(context.Background(), credit.GrantID, false)
	if result.OK {
		log.Debugf("scheduled probe ok: task=%d channel=%d model=%s key=%s grant=%d latency=%dms",
			target.ID, credit.Target.ChannelID, credit.Target.ModelName, credit.KeyName, credit.GrantID, result.LatencyMS)
	} else {
		log.Warnf("scheduled probe failed: task=%d channel=%d model=%s key=%s grant=%d latency=%dms message=%s",
			target.ID, credit.Target.ChannelID, credit.Target.ModelName, credit.KeyName, credit.GrantID, result.LatencyMS, result.Message)
	}
	advanceScheduledProbe(target, now, true)
}

// scheduledProbeCreditCursor 返回该任务轮转到的凭据下标; 无记录时为 0。
func scheduledProbeCreditCursor(id int) int {
	scheduledProbeMu.Lock()
	defer scheduledProbeMu.Unlock()

	if state := scheduledProbeStates[id]; state != nil {
		return state.creditCursor
	}
	return 0
}

// advanceScheduledProbe 推进任务的调度进度: 记下下一次允许探测的时刻, 并把轮转游标移到本条任务上。
//
// 以这一拍开始的时刻为基准, 而不是探测结束的时刻: 单次探测可能耗时数十秒, 按结束时刻累加会让
// 配置的 10 分钟被实际耗时拖成 11 分钟, 间隔配置也就不再等于真实节奏。
func advanceScheduledProbe(target model.ScheduledProbe, now time.Time, creditAdvanced bool) {
	scheduledProbeMu.Lock()
	defer scheduledProbeMu.Unlock()

	state := scheduledProbeStates[target.ID]
	if state == nil {
		state = &scheduledProbeState{}
		scheduledProbeStates[target.ID] = state
	}
	if creditAdvanced {
		state.creditCursor++
	}
	state.round++
	period := scheduledProbePeriod(target)
	state.nextDueAt = now.UnixMilli() + (period + scheduledProbeJitter(period, target.ID, state.round)).Milliseconds()
	lastProbedID = target.ID
}

// scheduledProbePeriod 返回这条任务两次探测之间该隔多久。
//
// 用户配的「间隔」是"每条凭据各测一次"的周期, 而每一拍只打一条上游, 于是拍的间隔要按凭据数均摊:
// 不摊的话, 6 条凭据的任务配 10 分钟, 每一条要等 60 分钟才轮到一次 ——
// 配置上的数字与用户的理解差出一个凭据数的倍数, 这正是"为什么不是每个模型按设定时间测一次"的由来。
//
// 摊到比调度节拍还短时按节拍兜底: 再密也不可能比调度器跑得更快, 写一个更小的值只会让
// nextDueAt 永远落在过去、每一拍都"到点", 白白多算一轮。
func scheduledProbePeriod(target model.ScheduledProbe) time.Duration {
	interval := time.Duration(target.IntervalMinutes) * time.Minute
	return scheduledProbeAmortizedPeriod(interval, len(op.ScheduledProbeCredits(target)))
}

// scheduledProbeAmortizedPeriod 把"每轮"的间隔均摊到 total 条凭据上。
// 单列成纯函数是为了能直接验证这段除法与兜底, 不必先搭出一整套渠道缓存。
func scheduledProbeAmortizedPeriod(interval time.Duration, total int) time.Duration {
	if total <= 1 {
		return interval
	}

	period := interval / time.Duration(total)
	if period < ScheduledProbeTickInterval {
		return ScheduledProbeTickInterval
	}
	return period
}

// scheduledProbeCycle 返回轮转一圈的时间, 也就是同一条凭据两次复测之间该隔多久。
//
// 一拍只测一条凭据, 一圈要走完凭据数十拍, 故它等于"均摊周期 × 凭据数"; 在周期没被节拍兜底时
// 恰好等于用户配置的间隔 —— 用户配的那个数字表达的就是这个意思(见 scheduledProbePeriod)。
//
// 掐到秒: 均摊的整数除法会留下亚微秒的余数(30 分钟 ÷ 7 再 × 7 差 6 纳秒), 而有效期本就是秒级的
// 粗量。抹掉这点余数让"轮转一圈 == 配置的间隔"这条等式真的成立, 免得有效期里出现一个差几纳秒的怪值。
// 四舍五入而不是截断: 截断会把那点余数放大成整整一秒(29m59s), 舍入则回到用户配的那个数。
func scheduledProbeCycle(interval time.Duration, total int) time.Duration {
	if total <= 0 {
		return 0
	}
	cycle := scheduledProbeAmortizedPeriod(interval, total) * time.Duration(total)
	return (cycle + scheduledProbeCycleUnit/2) / scheduledProbeCycleUnit * scheduledProbeCycleUnit
}

// scheduledProbeValidity 把一圈的时间换算成"结论该挂多久": 在周期之外再加一段余量。
//
// 余量补两件事: 每一拍的抖动会沿着一圈累计(单拍不超过周期的四分之一, 走完一圈合计不超过 cycle/4),
// 以及探测本身最长要 probeTimeout 才出结论。少了这段余量, 徽标会在下一次复测落下来之前先空掉 ——
// 那正是"测出来的结果丢了"的成因。
//
// 单列成纯函数是为了能直接验证这段加法, 不必先搭出一整套渠道缓存。
func scheduledProbeValidity(cycle time.Duration) time.Duration {
	if cycle <= 0 {
		return 0
	}
	return cycle + cycle/scheduledProbeJitterPeriodDivisor + probeTimeout
}

// scheduledProbeGrantValidity 返回一条结论该挂多久: 监控该授权的任务里最长的那个轮转周期加余量。
// 没有任何**启用中的**任务监控它时返回 0, 由读侧回落到默认有效期。
//
// 取最长的那个周期: 同一条授权可能被多条任务监控, 结论却只有一条 —— 按最短的那条算,
// 长间隔任务的结论会在它自己的下一轮到来之前先消失, 这正是"结果丢了"的成因。
//
// 只看启用中的任务: 停用的任务不会再复测, 也就不会有"下一次覆盖"。为它续期等于让一条
// 永远等不到替换的结论一直挂着, 那比提前消失更容易误导。
func scheduledProbeGrantValidity(grantID int) time.Duration {
	longest := time.Duration(0)
	for _, probe := range op.ScheduledProbeList() {
		if !probe.Enabled {
			continue
		}
		// 走与调度、界面同一个凭据列表(见 op.ScheduledProbeCredits): 被用户逐行删掉的凭据不在里面,
		// 它也就不该再为这条授权上的结论续期。
		credits := op.ScheduledProbeCredits(probe.ScheduledProbe)
		if !creditsCoverGrant(credits, grantID) {
			continue
		}
		cycle := scheduledProbeCycle(time.Duration(probe.IntervalMinutes)*time.Minute, len(credits))
		if validity := scheduledProbeValidity(cycle); validity > longest {
			longest = validity
		}
	}
	return longest
}

// creditsCoverGrant 判断这批凭据里是否有这条授权。
func creditsCoverGrant(credits []op.ScheduledProbeCredit, grantID int) bool {
	for _, credit := range credits {
		if credit.GrantID == grantID {
			return true
		}
	}
	return false
}

// withProbeExpiry 给一条结论定下失效时刻: 有任务在监控这条授权就按任务的轮转周期算,
// 让它正好挂到下一轮复测(或下一次手动测试)把它覆盖之前; 没有任务监控就留 0, 由读侧兜底。
//
// 手动测试与定时测活共用这一条规则: 用户手动测出来的结论同样代表"这条通道此刻通不通",
// 它该活多久取决于有没有人接着测, 而不取决于这次是谁点的按钮 ——
// 若手动测试只按一个写死的短时限算, 用户自己点的结果反而比定时测的更早消失。
func withProbeExpiry(result ProbeResult, grantID int) ProbeResult {
	if result.ExpiresAt > 0 {
		return result
	}
	if validity := scheduledProbeGrantValidity(grantID); validity > 0 {
		result.ExpiresAt = result.ProbedAt + validity.Milliseconds()
	}
	return result
}

// scheduledProbeJitter 给出该任务这一轮的抖动偏移, 落在 ±30 秒之间, 但不大于周期的四分之一。
//
// 用任务主键与轮数做散列而不是取全局随机数: 抖动只需要"每条任务、每一轮都不同"来错开上游流量,
// 而确定性让同一条任务的节奏可复现, 排查"它为什么总在这个点测"时不必再去猜随机数。
//
// 按周期收敛: 固定 ±30 秒在 10 分钟一拍时只占 5%, 而周期被均摊到几十秒之后就成了能盖过周期本身的
// 噪声 —— 抖动是为了把各条任务错开, 不是为了把它们各自的节奏打乱。
func scheduledProbeJitter(period time.Duration, id int, round uint64) time.Duration {
	span := uint64(2*scheduledProbeJitterMillis + 1)
	hashed := uint64(id)*scheduledProbeHashID + round*scheduledProbeHashRound
	offset := int64(hashed%span) - scheduledProbeJitterMillis
	jitter := time.Duration(offset) * time.Millisecond

	if limit := period / scheduledProbeJitterPeriodDivisor; jitter > limit {
		return limit
	} else if jitter < -limit {
		return -limit
	}
	return jitter
}

// probeLandedHook 是"测活结论已落点"的通知钩子, 由处理器包在 init 时注册。
// 方向这样定是为了不引入循环依赖: 推送事件要读分组配置并序列化整个分组响应, 那属于处理器;
// 而处理器本来就依赖本包, 本包再反过来依赖处理器就成环了, 故只留一个函数变量。
var probeLandedHook func(groupIDs []int)

// SetProbeLandedHook 注册测活落点通知; 传 nil 可注销(测试用)。
func SetProbeLandedHook(hook func(groupIDs []int)) {
	probeLandedHook = hook
}

// notifyProbeLanded 通知落点结果。无分组受影响时不必打扰处理器: 定时测活的任务可能还没被任何分组引用。
func notifyProbeLanded(groupIDs []int) {
	if hook := probeLandedHook; hook != nil && len(groupIDs) > 0 {
		hook(groupIDs)
	}
}

// ProbeScheduledGrant 按定时测活任务的要求探测一条渠道授权, 并把结论落到引用该授权的全部成员上。
//
// 与 ProbeChannelGrant 的差别只在落点: 那条路是模型页的一次性测活, 测完即弃;
// 定时测活是持续监控, 结论必须落进分组路由状态, 否则界面上的体检徽标不会有任何变化,
// 用户也就无从知道这条任务到底测出了什么 —— 监控的可见形态就是这个徽标。
func ProbeScheduledGrant(ctx context.Context, grantID int, streaming bool) ProbeResult {
	result := withProbeExpiry(ProbeChannelGrant(ctx, grantID, streaming), grantID)
	// 先按凭据记一份: 任务测的是 (渠道, 模型), 它可能压根没被任何分组引用,
	// 那份结论只落进分组就等于没记, 界面上也就看不到这条任务到底测出了什么。
	recordScheduledProbeResult(grantID, result)
	notifyProbeLanded(landScheduledProbe(grantID, result))
	return result
}

// ProbeScheduledNow 手动测完整任务；定时调度仍然每拍只测一条。
// 与界面、定时轮转共用凭据顺序；同渠道串行，跨渠道有限并发。
func ProbeScheduledNow(ctx context.Context, probe model.ScheduledProbe) ([]ProbeResult, error) {
	credits := op.ScheduledProbeCredits(probe)
	if len(credits) == 0 {
		return nil, fmt.Errorf("no available credential for this probe")
	}
	channelIDs := make([]int, len(credits))
	for index, credit := range credits {
		channelIDs[index] = credit.Target.ChannelID
	}
	return probeByChannel(channelIDs, func(index int) ProbeResult {
		return ProbeScheduledGrant(ctx, credits[index].GrantID, false)
	}), nil
}

// ProbeGrantNow 立即探测单条渠道凭据, 供界面上一行末尾的闪电按钮使用。
// 与按任务触发分开: 那一行代表的就是这一条凭据, 点它却把整批都测一遍,
// 既多打了上游, 也让"我点的是这一行"这个意图落空。
func ProbeGrantNow(ctx context.Context, grantID int) ProbeResult {
	return ProbeScheduledGrant(ctx, grantID, false)
}

// landScheduledProbe 把结论落到引用该授权的每个分组成员上, 返回需要处理器补推事件的分组 ID。
//
// 按授权而不是按分组查找: 定时测活任务记的是 (渠道, 模型), 而真实转发与体检徽标都按授权(模型 + 凭据)组织。
// 一条授权可能同时被多个分组引用, 每个引用处都要看到同一个结论, 否则"同一份凭据在两个分组里一个绿一个红"。
//
// 返回值只含手动模式的分组: 故障转移模式的路由增量已由 publishRouteLocked 推走(含结论),
// 再让处理器推一次完整分组就是同一件事推两遍。
func landScheduledProbe(grantID int, result ProbeResult) []int {
	groupIDs := make([]int, 0, 4)
	for _, group := range op.GroupList() {
		itemIDs := itemIDsOfGrant(group, grantID)
		if len(itemIDs) == 0 {
			continue
		}

		routeMu.Lock()
		route := groupRouteLocked(group)
		for _, itemID := range itemIDs {
			landed := result
			landed.GroupID = group.ID
			landed.ItemID = itemID
			// 失败即冷却: 定时测活是持续监控, 结论就是"这一刻它不通", 继续把它排在选路前面没有依据。
			applyProbeLocked(route, group, itemID, landed, true)
			route.Probes[itemID] = landed
		}
		// 手动模式不推路由增量: 那条增量会带出 current_item_id=0, 把界面上"正在使用哪个成员"冲掉。
		// 手动模式的结论改由处理器随完整分组广播(changed 事件里的 runtime 已含 Probes), 见 notifyProbeLanded。
		if group.Mode == model.GroupModeManual {
			groupIDs = append(groupIDs, group.ID)
		} else {
			publishRouteLocked(route)
		}
		routeMu.Unlock()
	}
	return groupIDs
}

// itemIDsOfGrant 返回分组内引用该授权的全部成员 ID。
// 返回切片而不是单个 ID: 同一条授权可以被同一个分组以不同优先级加进来两次, 两处都要落点。
func itemIDsOfGrant(group model.Group, grantID int) []int {
	itemIDs := make([]int, 0, 1)
	for _, item := range group.Items {
		if item.ChannelGrantID == grantID {
			itemIDs = append(itemIDs, item.ID)
		}
	}
	return itemIDs
}
