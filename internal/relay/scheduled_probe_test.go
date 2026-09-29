package relay

import (
	"testing"
	"time"

	"github.com/bestruirui/octopus/internal/model"
)

// resetScheduledProbeScheduler 清掉调度进度与轮转游标, 使各用例互不影响。
// 这两份都是包级状态, 不清会让用例之间通过"上一拍测过谁"串起来。
func resetScheduledProbeScheduler() {
	scheduledProbeMu.Lock()
	defer scheduledProbeMu.Unlock()

	scheduledProbeStates = make(map[int]*scheduledProbeState)
	lastProbedID = 0
}

// dueProbe 造一条已到点(无进度记录即视为到点)的任务。
func dueProbe(id int) model.ScheduledProbe {
	return model.ScheduledProbe{ID: id, ChannelID: 1, ModelName: "m", IntervalMinutes: 10, Enabled: true}
}

// probeInterval 是用例里的探测间隔; 与 dueProbe 一致, 便于按间隔推演每一拍的时刻。
const probeInterval = 10 * time.Minute

// selectAndAdvance 复刻一拍的选择与进度推进, 但不发起真实上游调用。
// 调度决策与网络调用是两件事, 分开测才不必为了断言轮转顺序去搭一个假上游。
func selectAndAdvance(targets []model.ScheduledProbe, now time.Time) (model.ScheduledProbe, bool) {
	target, ok := nextScheduledProbe(targets, now)
	if !ok {
		return model.ScheduledProbe{}, false
	}
	advanceScheduledProbe(target, now, true)
	return target, true
}

// TestScheduledProbeJitterWithinRange 抖动必须落在 ±30 秒内, 且随任务与轮数变化。
// 越界会让"间隔 10 分钟"名不副实; 恒定则等于没抖动, 创建时间接近的任务会永远挤在同一秒打上游。
func TestScheduledProbeJitterWithinRange(t *testing.T) {
	seen := make(map[time.Duration]bool)
	for id := 1; id <= 64; id++ {
		for round := uint64(0); round < 8; round++ {
			jitter := scheduledProbeJitter(id, round)
			if jitter > scheduledProbeJitterMillis*time.Millisecond || jitter < -scheduledProbeJitterMillis*time.Millisecond {
				t.Fatalf("任务 %d 第 %d 轮的抖动 %v 超出 ±30s", id, round, jitter)
			}
			seen[jitter] = true
		}
	}
	if len(seen) < 8 {
		t.Fatalf("抖动取值过于集中, 只有 %d 种", len(seen))
	}
}

// TestScheduledProbeRotationIsFIFO 一拍只测一条, 且按创建顺序轮转: 三条任务依次被选中后回到第一条。
// 时刻按间隔推进, 与真实节拍一致(节拍只决定"什么时候来看一眼", 间隔决定"到点没有")。
func TestScheduledProbeRotationIsFIFO(t *testing.T) {
	resetScheduledProbeScheduler()
	targets := []model.ScheduledProbe{dueProbe(1), dueProbe(2), dueProbe(3)}
	now := time.Now()

	want := []int{1, 2, 3, 1, 2, 3}
	for i, expected := range want {
		target, ok := selectAndAdvance(targets, now)
		if !ok {
			t.Fatalf("第 %d 拍应有任务被选中", i+1)
		}
		if target.ID != expected {
			t.Fatalf("第 %d 拍应轮转到任务 %d, 却选中 %d", i+1, expected, target.ID)
		}
		now = now.Add(probeInterval)
	}
}

// TestScheduledProbeRespectsInterval 刚测过的任务在间隔内不该被再次选中。
// 抖动只在 ±30 秒内, 故间隔 10 分钟时同一拍绝无可能又轮到它。
func TestScheduledProbeRespectsInterval(t *testing.T) {
	resetScheduledProbeScheduler()
	targets := []model.ScheduledProbe{dueProbe(1)}
	now := time.Now()

	if _, ok := selectAndAdvance(targets, now); !ok {
		t.Fatalf("首次应被选中")
	}
	if _, ok := nextScheduledProbe(targets, now); ok {
		t.Fatalf("间隔未到时不该再次选中同一条任务")
	}
	// 间隔过后(留足抖动余量)重新可测。
	if _, ok := nextScheduledProbe(targets, now.Add(11*time.Minute)); !ok {
		t.Fatalf("间隔已过应重新可测")
	}
}

// TestScheduledProbeSkipsDisabled 停用的任务不参与轮转, 且不影响其余任务的轮转顺序。
func TestScheduledProbeSkipsDisabled(t *testing.T) {
	resetScheduledProbeScheduler()
	disabled := dueProbe(2)
	disabled.Enabled = false
	targets := []model.ScheduledProbe{dueProbe(1), disabled, dueProbe(3)}
	now := time.Now()

	for i, expected := range []int{1, 3, 1, 3} {
		target, ok := selectAndAdvance(targets, now)
		if !ok {
			t.Fatalf("第 %d 拍应有任务被选中", i+1)
		}
		if target.ID != expected {
			t.Fatalf("第 %d 拍应选中 %d, 却选中 %d", i+1, expected, target.ID)
		}
		now = now.Add(probeInterval)
	}
}

// TestScheduledProbeSkipsOutsideWindow 窗口外的任务被跳过, 窗口一开立即补上。
// "跳过"不等于"作废": 它仍在轮转里, 且因早已到点会在窗口内被立刻选中。
func TestScheduledProbeSkipsOutsideWindow(t *testing.T) {
	resetScheduledProbeScheduler()
	windowed := dueProbe(1)
	windowed.Weekdays = model.WeekdayMonday
	windowed.StartHour = 9
	windowed.EndHour = 18
	targets := []model.ScheduledProbe{windowed}

	// 周一上班前: 未到窗口, 即使早已到点也不测。
	if target, ok := nextScheduledProbe(targets, time.Date(2026, 1, 5, 8, 0, 0, 0, time.UTC)); ok {
		t.Fatalf("窗口未开不该探测, 却选中了任务 %d", target.ID)
	}

	// 周一窗口内: 立刻补上。
	target, ok := selectAndAdvance(targets, time.Date(2026, 1, 5, 12, 0, 0, 0, time.UTC))
	if !ok || target.ID != 1 {
		t.Fatalf("窗口内应选中任务 1, 却得到 %v ok=%v", target.ID, ok)
	}

	// 窗口内再过半个间隔: 不该重复测同一条。
	if target, ok := nextScheduledProbe(targets, time.Date(2026, 1, 5, 12, 5, 0, 0, time.UTC)); ok {
		t.Fatalf("间隔未到不该重复探测, 却选中了任务 %d", target.ID)
	}

	// 窗口内间隔已过: 继续测。
	target, ok = selectAndAdvance(targets, time.Date(2026, 1, 5, 12, 30, 0, 0, time.UTC))
	if !ok || target.ID != 1 {
		t.Fatalf("窗口内间隔已过应继续探测, 却得到 %v ok=%v", target.ID, ok)
	}

	// 周一窗口关闭后: 即使到点也不测。
	if target, ok := nextScheduledProbe(targets, time.Date(2026, 1, 5, 22, 0, 0, 0, time.UTC)); ok {
		t.Fatalf("窗口已关闭不该探测, 却选中了任务 %d", target.ID)
	}

	// 周二: 该任务的星期未选中, 整天都不测。
	if target, ok := nextScheduledProbe(targets, time.Date(2026, 1, 6, 12, 0, 0, 0, time.UTC)); ok {
		t.Fatalf("星期二未选中时不该探测, 却选中了任务 %d", target.ID)
	}

	// 下周一窗口重开: 早已到点, 应立即补上。
	target, ok = selectAndAdvance(targets, time.Date(2026, 1, 12, 12, 0, 0, 0, time.UTC))
	if !ok || target.ID != 1 {
		t.Fatalf("窗口重开后应补上任务 1, 却得到 %v ok=%v", target.ID, ok)
	}
}

// TestScheduledProbeNoTargets 没有任何任务时这一拍直接结束, 不该 panic 也不该选中零值任务。
func TestScheduledProbeNoTargets(t *testing.T) {
	resetScheduledProbeScheduler()
	if _, ok := nextScheduledProbe(nil, time.Now()); ok {
		t.Fatalf("没有任务时不该选中任何一条")
	}
}

// TestApplyProbeCooldownOnFailure 定时测活失败即按分组配置冷却, 人工测活失败不冷却。
// 这是两种调用方唯一的差别: 定时测活是持续监控, 结论就是"这一刻它不通", 继续把它排在选路前面没有依据。
func TestApplyProbeCooldownOnFailure(t *testing.T) {
	resetRoutes()
	group := failoverGroup(0)
	pickGroupItem(group)

	manual := ProbeResult{GroupID: 900, ItemID: 2, OK: false, Message: "boom", ProbedAt: time.Now().UnixMilli()}
	applyProbeLocked(routes[900], group, 2, manual, false)
	if _, cooling := routes[900].Cooldowns[2]; cooling {
		t.Fatalf("人工测活失败不该让成员进入冷却")
	}

	scheduled := manual
	scheduled.ItemID = 3
	applyProbeLocked(routes[900], group, 3, scheduled, true)
	until, cooling := routes[900].Cooldowns[3]
	if !cooling {
		t.Fatalf("定时测活失败应让成员进入冷却")
	}
	want := scheduled.ProbedAt + int64(group.RelayConfig.MemberCooldownSeconds)*1000
	if until != want {
		t.Fatalf("冷却截止应为 %d, 却得到 %d", want, until)
	}
}

// TestApplyProbeKeepsPriority 测活无论成败都不许改动成员优先级。
// 优先级是人工排定的次序, 监控只提供健康信号; 自动改优先级会让用户排序在自己不知情时被抹掉。
func TestApplyProbeKeepsPriority(t *testing.T) {
	resetRoutes()
	group := failoverGroup(0)
	pickGroupItem(group)

	before := make([]int, 0, len(group.Items))
	for _, item := range group.Items {
		before = append(before, item.Priority)
	}

	now := time.Now().UnixMilli()
	applyProbeLocked(routes[900], group, 1, ProbeResult{GroupID: 900, ItemID: 1, OK: false, ProbedAt: now}, true)
	applyProbeLocked(routes[900], group, 2, ProbeResult{GroupID: 900, ItemID: 2, OK: true, ProbedAt: now}, true)

	for i, item := range group.Items {
		if item.Priority != before[i] {
			t.Fatalf("%d 号优先级被改动: %d -> %d", item.ID, before[i], item.Priority)
		}
	}
}

// TestApplyProbeSuccessClearsScheduledCooldown 定时测活调通要解除冷却, 让成员重新回到轮转。
// 只冷却不解除等于一次抖动就永久除名, 监控也就失去了"恢复即放行"的意义。
func TestApplyProbeSuccessClearsScheduledCooldown(t *testing.T) {
	resetRoutes()
	group := failoverGroup(0)
	pickGroupItem(group)

	now := time.Now().UnixMilli()
	applyProbeLocked(routes[900], group, 3, ProbeResult{GroupID: 900, ItemID: 3, OK: false, ProbedAt: now}, true)
	if _, cooling := routes[900].Cooldowns[3]; !cooling {
		t.Fatalf("前置条件不成立: 失败后应处于冷却中")
	}

	applyProbeLocked(routes[900], group, 3, ProbeResult{GroupID: 900, ItemID: 3, OK: true, ProbedAt: now}, true)
	if _, cooling := routes[900].Cooldowns[3]; cooling {
		t.Fatalf("定时测活调通应解除冷却")
	}
}

// TestLandScheduledProbeWithoutGroups 结论落点在没有分组引用该授权时应安静结束。
// 定时测活允许先建任务后建分组, 此时不该 panic, 也不该产生需要推送的事件。
func TestLandScheduledProbeWithoutGroups(t *testing.T) {
	resetRoutes()
	groupIDs := landScheduledProbe(1, ProbeResult{ItemID: 1, OK: true, ProbedAt: time.Now().UnixMilli()})
	if len(groupIDs) != 0 {
		t.Fatalf("没有分组引用该授权时不该产生待推送事件, 却得到 %v", groupIDs)
	}
}

// TestItemIDsOfGrantMatchesAllReferences 同一条授权被同一分组引用两次时, 两处成员都要落点。
func TestItemIDsOfGrantMatchesAllReferences(t *testing.T) {
	group := model.Group{ID: 900, Items: []model.GroupItem{
		{ID: 1, ChannelGrantID: 7},
		{ID: 2, ChannelGrantID: 8},
		{ID: 3, ChannelGrantID: 7},
	}}

	itemIDs := itemIDsOfGrant(group, 7)
	if len(itemIDs) != 2 || itemIDs[0] != 1 || itemIDs[1] != 3 {
		t.Fatalf("应返回引用授权 7 的全部成员 [1 3], 却得到 %v", itemIDs)
	}
	if got := itemIDsOfGrant(group, 9); len(got) != 0 {
		t.Fatalf("未被引用的授权不该有落点, 却得到 %v", got)
	}
}