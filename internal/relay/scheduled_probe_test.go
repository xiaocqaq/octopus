package relay

import (
	"testing"
	"time"

	"github.com/bestruirui/octopus/internal/model"
	"github.com/bestruirui/octopus/internal/op"
)

// resetScheduledProbeScheduler 清掉调度进度与轮转游标, 使各用例互不影响。
// 这两份都是包级状态, 不清会让用例之间通过"上一拍测过谁"串起来。
func resetScheduledProbeScheduler() {
	scheduledProbeMu.Lock()
	defer scheduledProbeMu.Unlock()

	scheduledProbeStates = make(map[int]*scheduledProbeState)
	lastProbedID = 0
}

// dueProbe 造一条已到点(无进度记录即视为到点)的任务, 带一个目标。
func dueProbe(id int) model.ScheduledProbe {
	return model.ScheduledProbe{
		ID:              id,
		Name:            "probe",
		IntervalMinutes: 10,
		Enabled:         true,
		Targets:         []model.ScheduledProbeTarget{{ID: id, ProbeID: id, ChannelID: 1, ModelName: "m"}},
	}
}

// dueProbeWithTargets 造一条挂了多个目标的任务, 用于验证两级轮转。
func dueProbeWithTargets(id int, targets ...model.ScheduledProbeTarget) model.ScheduledProbe {
	return model.ScheduledProbe{ID: id, Name: "probe", IntervalMinutes: 10, Enabled: true, Targets: targets}
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
			jitter := scheduledProbeJitter(probeInterval, id, round)
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

// TestScheduledProbeJitterShrinksWithPeriod 周期被均摊到几十秒之后, 抖动不该大到盖过周期本身。
// 固定 ±30 秒在 10 分钟一拍时只占 5%, 而在 40 秒一拍时能到 75% —— 那就不是错开流量而是打乱节奏了。
func TestScheduledProbeJitterShrinksWithPeriod(t *testing.T) {
	period := 40 * time.Second
	limit := period / scheduledProbeJitterPeriodDivisor

	for id := 1; id <= 64; id++ {
		for round := uint64(0); round < 8; round++ {
			jitter := scheduledProbeJitter(period, id, round)
			if jitter > limit || jitter < -limit {
				t.Fatalf("周期 %v 时任务 %d 第 %d 轮的抖动 %v 超出 ±%v", period, id, round, jitter, limit)
			}
		}
	}
}

// TestScheduledProbeAmortizesIntervalAcrossCredits 配的间隔是"每条凭据各测一次"的周期, 要按凭据数均摊。
// 不均摊的话, 6 条凭据配 10 分钟会让每条凭据 60 分钟才轮到一次 —— 配置上的数字与用户的理解差一个倍数。
func TestScheduledProbeAmortizesIntervalAcrossCredits(t *testing.T) {
	cases := []struct {
		name     string
		interval time.Duration
		total    int
		want     time.Duration
	}{
		{"单条凭据不摊", 10 * time.Minute, 1, 10 * time.Minute},
		{"没有凭据时按原间隔推后", 10 * time.Minute, 0, 10 * time.Minute},
		{"6 条凭据摊成 100 秒", 10 * time.Minute, 6, 100 * time.Second},
		{"摊到比节拍还短时按节拍兜底", time.Minute, 30, ScheduledProbeTickInterval},
		{"恰好等于节拍时不兜底", 100 * time.Second, 10, ScheduledProbeTickInterval},
	}

	for _, item := range cases {
		if got := scheduledProbeAmortizedPeriod(item.interval, item.total); got != item.want {
			t.Fatalf("%s: expected %v, actual %v", item.name, item.want, got)
		}
	}
}

// TestScheduledProbeCycleEqualsConfiguredInterval 轮转一圈的时间就是用户配的间隔。
//
// 这条等式是"有效期的口径"能否成立的前提: 结论按轮转周期续期, 而用户配的那个数字
// 表达的正是"每条凭据各测一次"的周期。两者一旦不等, 要么徽标提前空窗, 要么旧结论挂过头。
func TestScheduledProbeCycleEqualsConfiguredInterval(t *testing.T) {
	cases := []struct {
		name     string
		interval time.Duration
		total    int
		want     time.Duration
	}{
		{"单条凭据时一圈就是间隔", 10 * time.Minute, 1, 10 * time.Minute},
		{"30 分钟配 7 条凭据仍是一圈 30 分钟", 30 * time.Minute, 7, 30 * time.Minute},
		{"30 分钟配 6 条凭据仍是一圈 30 分钟", 30 * time.Minute, 6, 30 * time.Minute},
		{"没有凭据时没有周期", 10 * time.Minute, 0, 0},
		// 周期被节拍兜底后, 一圈会比配置的间隔更长: 此时按配置的间隔算就会提前空窗。
		{"摊到比节拍还短时一圈按节拍算", time.Minute, 30, 30 * ScheduledProbeTickInterval},
	}

	for _, item := range cases {
		if got := scheduledProbeCycle(item.interval, item.total); got != item.want {
			t.Fatalf("%s: expected %v, actual %v", item.name, item.want, got)
		}
	}
}

// TestScheduledProbeValidityExceedsCycle 有效期必须比一圈更长, 否则徽标会在下一轮结论落下来之前先空掉。
// 余量要盖住沿圈累计的抖动与一次探测的耗时; 具体幅度不是契约, "严格更长"才是。
func TestScheduledProbeValidityExceedsCycle(t *testing.T) {
	for _, cycle := range []time.Duration{time.Minute, 10 * time.Minute, 30 * time.Minute, time.Hour} {
		validity := scheduledProbeValidity(cycle)
		if validity <= cycle {
			t.Fatalf("一圈 %v 时有效期 %v 不严格长于一圈", cycle, validity)
		}
		// 余量要有界: 不该长到让已经过时的结论继续参与选路。
		if validity > cycle+cycle/scheduledProbeJitterPeriodDivisor+probeTimeout {
			t.Fatalf("一圈 %v 时有效期 %v 超出预期余量", cycle, validity)
		}
	}
	if got := scheduledProbeValidity(0); got != 0 {
		t.Fatalf("没有周期时不该给出有效期, 实际 %v", got)
	}
}

// TestWithProbeExpiryKeepsExisting 已经带有效期的结论不再被改写。
// 手动测试与定时测活共用同一条续期规则, 谁先定稿就以谁为准, 不因调用顺序而变。
func TestWithProbeExpiryKeepsExisting(t *testing.T) {
	now := time.Now().UnixMilli()
	preset := ProbeResult{ProbedAt: now, ExpiresAt: now + 12345}
	if got := withProbeExpiry(preset, 1); got.ExpiresAt != preset.ExpiresAt {
		t.Fatalf("expected 既有有效期 %d 被保留, actual %d", preset.ExpiresAt, got.ExpiresAt)
	}
}

// TestScheduledProbeValiditiesCoverEveryGrant 有效期按凭据逐条落定, 且只覆盖被监控的那些。
// 顺带把两种口径放在一起: 有任务监控的凭据拿到任务周期, 没有的保持 0 由读侧兜底。
func TestScheduledProbeValidityCoversMonitoredGrants(t *testing.T) {
	if got := scheduledProbeGrantValidity(999999); got != 0 {
		t.Fatalf("没有任何任务覆盖的凭据不该拿到有效期, 实际 %v", got)
	}
	if creditsCoverGrant(nil, 1) {
		t.Fatal("空凭据列表不该覆盖任何授权")
	}
	if !creditsCoverGrant([]op.ScheduledProbeCredit{{GrantID: 7}}, 7) {
		t.Fatal("凭据列表里有该授权时应判为覆盖")
	}
	if creditsCoverGrant([]op.ScheduledProbeCredit{{GrantID: 7}}, 8) {
		t.Fatal("凭据列表里没有该授权时不该判为覆盖")
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

// TestScheduledProbeRotatesAcrossCredits 轮转是扁平的: 每一拍照着凭据列表往前走一格。
//
// "每一拍都往前走"是这套轮转的根 —— 不前进就会反复测同一条凭据, 其余凭据永远轮不到。
// 具体轮到哪一条由 op.ScheduledProbeCredits 定序(与界面上的行顺序同源), 那里单独测;
// 这里只守"游标步步前进、不跳格也不回头"这一件事。
func TestScheduledProbeRotatesAcrossCredits(t *testing.T) {
	resetScheduledProbeScheduler()
	const probeID = 1

	seen := make([]int, 0, 6)
	for round := 0; round < 6; round++ {
		seen = append(seen, scheduledProbeCreditCursor(probeID))
		advanceScheduledProbe(dueProbe(probeID), time.Now(), true)
	}

	want := []int{0, 1, 2, 3, 4, 5}
	for i, expected := range want {
		if seen[i] != expected {
			t.Fatalf("第 %d 拍应轮到第 %d 条凭据, 却得到 %d (序列 %v)", i+1, expected, seen[i], seen)
		}
	}
}

// TestScheduledProbeKeepsCursorWhenNothingProbed 没有可测凭据时不该推进游标。
// 那种情况什么都没测, 推进游标等于凭空跳过一条凭据 —— 用户会看到某条凭据永远不被测。
func TestScheduledProbeKeepsCursorWhenNothingProbed(t *testing.T) {
	resetScheduledProbeScheduler()
	probe := dueProbe(1)

	advanceScheduledProbe(probe, time.Now(), false)

	if cursor := scheduledProbeCreditCursor(probe.ID); cursor != 0 {
		t.Fatalf("expected 游标停在原处 0, actual %d", cursor)
	}
}

// TestScheduledProbeFallsBackWithoutTargets 没有目标的任务不该 panic, 只把进度推后。
// 创建时已拦下空目标, 但渠道改动后任务可能落到这个状态, 那时调度器仍要能安全跳过它。
func TestScheduledProbeFallsBackWithoutTargets(t *testing.T) {
	resetScheduledProbeScheduler()
	probe := dueProbeWithTargets(1)
	runScheduledProbe(probe, time.Now())

	if !scheduledProbeDue(1, time.Now()) {
		// 已推进过进度, 说明确实走完了 advance 分支而不是在半路 panic。
		return
	}
	t.Fatal("空目标任务应把下一次探测推后")
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