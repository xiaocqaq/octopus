package relay

import (
	"testing"
	"time"
)

// ageScore 把某成员"这一档"的计时起点往前拨 n 个档位周期, 用于断言衰减而不必真等 15 分钟。
func ageScore(itemID, steps int) {
	routeMu.Lock()
	defer routeMu.Unlock()

	if route := routes[900]; route != nil {
		route.ScoreAt[itemID] -= int64(steps) * routeScoreStepTTL.Milliseconds()
	}
}

// coolDown 让某成员失败到进入冷却并降档, 返回它失败后的分数。
// 失败次数给到总尝试次数(默认 2), 才会触发冷却与降档。
func coolDown(itemID int) int {
	group := failoverGroup(0)
	recordRouteFailure(group, itemID, group.RelayConfig.MemberMaxAttempts)
	return routeScore(itemID)
}

// TestNegativeScoreDecaysToNeutral 负分不会被永久钉住: 每过一个档位周期就向 0 走一档, 最重 3 档共 3 个周期归零。
// 这是"上游会恢复, 分数不该是判决"的落点 —— 没有它, 沉底的成员永远排在最后, 也就永远拿不到成功来抵消。
func TestNegativeScoreDecaysToNeutral(t *testing.T) {
	resetRoutes()
	group := failoverGroup(0)
	pickGroupItem(group)

	for i := 0; i < 3; i++ {
		if got := coolDown(1); got != -(i + 1) {
			t.Fatalf("第 %d 次冷却后应降到 %d, 却得到 %d", i+1, -(i + 1), got)
		}
	}
	if got := routeScore(1); got != -3 {
		t.Fatalf("未经衰减时 1 号应为 -3, 却得到 %d", got)
	}

	ageScore(1, 1)
	if got := routeScore(1); got != -2 {
		t.Fatalf("再过一档周期应衰减到 -2, 却得到 %d", got)
	}
	ageScore(1, 1)
	if got := routeScore(1); got != -1 {
		t.Fatalf("再过一档周期应衰减到 -1, 却得到 %d", got)
	}
	ageScore(1, 1)
	if got := routeScore(1); got != 0 {
		t.Fatalf("第 3 个周期后应回到中性 0, 却得到 %d", got)
	}
	// 归零后再怎么放置都不会变成反向的分。
	ageScore(1, 5)
	if got := routeScore(1); got != 0 {
		t.Fatalf("放置再久也只回到 0, 不该变成 %d", got)
	}
}

// TestStuckNegativeMemberIsRetriedAfterDecay 关键的那个自锁: 沉底的成员在衰减回中性后必须重新被选中。
// 旧行为下它排最后且没人会让位, 20 轮一次都轮不到(实测), 也就永远抬不起头。
func TestStuckNegativeMemberIsRetriedAfterDecay(t *testing.T) {
	resetRoutes()
	group := failoverGroup(0)
	pickGroupItem(group)

	if got := coolDown(1); got != -1 {
		t.Fatalf("前置条件不成立: 1 号冷却后应为 -1, 却是 %d", got)
	}
	// 冷却到期(上游已恢复), 但分还是负的: 先按旧行为确认它被排在后面。
	routeMu.Lock()
	routes[900].Cooldowns[1] = time.Now().UnixMilli() - 1000
	routeMu.Unlock()
	if ordered := orderGroupItems(group, routes[900]); ordered[0].ID == 1 {
		t.Fatalf("前置条件不成立: 带负分的 1 号不该排在首位, 却得到 %v", itemIDs(ordered))
	}

	// 一个档位周期内没有新的失败(上游确实恢复了): 分数自己走回中性, 它随之回到配置首位。
	ageScore(1, 1)
	if got := routeScore(1); got != 0 {
		t.Fatalf("一档周期后 1 号应回到中性, 却得到 %d", got)
	}
	if got := pickGroupItem(group); got.ID != 1 {
		t.Fatalf("衰减归零后 1 号应重新被选中, 却选中 %d 号", got.ID)
	}
}

// TestPositiveScoreDecaysToo 正分同样有寿命: 靠连续成功挣来的位次在闲下来之后回落到配置顺序。
// 否则"最近可靠"会变成永久特权, 与负分一样是拿旧证据做长期决策。
func TestPositiveScoreDecaysToo(t *testing.T) {
	resetRoutes()
	group := failoverGroup(0)
	pickGroupItem(group)

	for i := 0; i < 3; i++ {
		recordRouteSuccess(group, 3) // 3 连成功升一档。
	}
	if got := routeScore(3); got != 1 {
		t.Fatalf("3 连成功后 3 号应为 1, 却得到 %d", got)
	}
	ageScore(3, 1)
	if got := routeScore(3); got != 0 {
		t.Fatalf("一档周期后正分也应归零, 却得到 %d", got)
	}
	// 归零后同分, 回到配置优先级顺序。
	if ordered := orderGroupItems(group, routes[900]); ordered[0].ID != 1 {
		t.Fatalf("正分过期后应按配置顺序以 1 号打头, 却得到 %v", itemIDs(ordered))
	}
}

// TestSuccessUndoesOneNegativeStep 一次真实成功抵掉一档负分(不是清零, 也不是要连续三次)。
// 负分成员排后面、拿不到流量, 要求"连续成功三次"等于让它永远抵消不掉。
func TestSuccessUndoesOneNegativeStep(t *testing.T) {
	resetRoutes()
	group := failoverGroup(0)
	pickGroupItem(group)

	coolDown(1)
	coolDown(1)
	if got := routeScore(1); got != -2 {
		t.Fatalf("前置条件不成立: 1 号应为 -2, 却是 %d", got)
	}

	recordRouteSuccess(group, 1)
	if got := routeScore(1); got != -1 {
		t.Fatalf("一次成功应抵掉一档, 期望 -1, 却得到 %d", got)
	}
	recordRouteSuccess(group, 1)
	if got := routeScore(1); got != 0 {
		t.Fatalf("再一次成功应回到中性, 期望 0, 却得到 %d", got)
	}
	// 回到中性后不再额外加分: 正分仍要连续三次成功才升档。
	if got := routeScore(1); got != 0 {
		t.Fatalf("第二次成功不该顺手加正分, 却得到 %d", got)
	}
}

// TestProbeSuccessUndoesOneNegativeStep 测活通过也抵掉一档负分。
// 否则测活只能给 5 分钟加权, 那段加权一过期, 底下压着的负分就又露出来了。
func TestProbeSuccessUndoesOneNegativeStep(t *testing.T) {
	resetRoutes()
	group := failoverGroup(0)
	pickGroupItem(group)

	coolDown(1)
	coolDown(1)
	if got := routeScore(1); got != -2 {
		t.Fatalf("前置条件不成立: 1 号应为 -2, 却是 %d", got)
	}

	recordProbe(group, 1, true, "", 12)
	if got := routeScore(1); got != probeVoteNewScore() {
		t.Fatalf("测活通过后应抵一档负分并加上体检加权, 期望 %d, 却得到 %d",
			probeVoteNewScore(), got)
	}
	// 体检加权过期(把结论时间拨旧)之后, 底下应只剩 -1, 而不是原来的 -2。
	expireProbes(1)
	if got := routeScore(1); got != -1 {
		t.Fatalf("体检加权过期后应剩 -1(负分已被抵掉一档), 却得到 %d", got)
	}
}

// probeVoteNewScore 是"负分抵一档 + 体检通过加权"叠加后的期望值。
func probeVoteNewScore() int { return -1 + routeScoreMax }

// expireProbes 把某成员的测活结论时间拨到有效期之外。
func expireProbes(itemID int) {
	routeMu.Lock()
	defer routeMu.Unlock()

	if route := routes[900]; route != nil {
		route.Probes[itemID] = expireProbe(route.Probes[itemID])
	}
}

// TestRouteStateCarriesScoreOrigin 发布出去的分数带计时起点, 前端才能把剩下的档位在页面内走完。
func TestRouteStateCarriesScoreOrigin(t *testing.T) {
	resetRoutes()
	group := failoverGroup(0)
	pickGroupItem(group)

	coolDown(1)
	ageScore(1, 1)

	state := RouteStateOf(group)
	if got := state.Scores[1]; got != 0 {
		t.Fatalf("已过一档周期, 状态接口该给衰减后的 0, 却得到 %d", got)
	}
	// 未衰减的成员同时带上起点: 起点 + 周期 == 下一档的时刻。
	coolDown(2)
	state = RouteStateOf(group)
	if got := state.Scores[2]; got != -1 {
		t.Fatalf("2 号应为 -1, 却得到 %d", got)
	}
	at, ok := state.ScoreAt[2]
	if !ok {
		t.Fatalf("状态接口应带上分数计时起点: %+v", state.ScoreAt)
	}
	if until := at + routeScoreStepTTL.Milliseconds() - time.Now().UnixMilli(); until <= 0 || until > routeScoreStepTTL.Milliseconds() {
		t.Fatalf("计时起点应指向本档的开始时刻(剩余 %d ms 不在 (0, %d] 内)",
			until, routeScoreStepTTL.Milliseconds())
	}
}

// TestScoreDecayKeepsStoredValueIntact 衰减是"现算"的: 表里存的仍是变动当时的加减值, 不写回。
// 写回会丢掉原始幅度, 反复读写之后分档会越漂越浅。
func TestScoreDecayKeepsStoredValueIntact(t *testing.T) {
	resetRoutes()
	group := failoverGroup(0)
	pickGroupItem(group)

	coolDown(1)
	ageScore(1, 2)

	if got := baseScore(routes[900], 1, time.Now().UnixMilli()); got != 0 {
		t.Fatalf("衰减后有效值应为 0, 却得到 %d", got)
	}
	routeMu.Lock()
	stored := routes[900].Scores[1]
	routeMu.Unlock()
	if stored != -1 {
		t.Fatalf("表里该保留原始的 -1, 却变成 %d", stored)
	}
}

// expireCooldown 把某成员的冷却截止时间挪到过去, 等价于"冷却已到期", 不必真等 60 秒。
func expireCooldown(itemID int) {
	routeMu.Lock()
	defer routeMu.Unlock()

	if route := routes[900]; route != nil {
		if _, ok := route.Cooldowns[itemID]; ok {
			route.Cooldowns[itemID] = time.Now().UnixMilli() - 1
		}
	}
}

// expireAffinity 结束当前亲和, 等价于"亲和时间用完"。
func expireAffinity() {
	routeMu.Lock()
	defer routeMu.Unlock()

	if route := routes[900]; route != nil {
		route.AffinityUntil = 0
	}
}

// TestChronicDeadMemberGetsOneChancePerStep 长期不可用的成员不会被"一直检测"。
// 降档后被负分排到最后、冷却期内直接被跳过、备用成员成功后的亲和期内更是完全不碰它;
// 要等冷却、亲和与这一档寿命都到期, 下一次请求才会再给它一次机会 —— 一次, 不是反复。
// 这条不变式定下"坏成员的额外开销上界": 每个档位周期最多一次真实调用, 而不是每个请求都撞上去。
func TestChronicDeadMemberGetsOneChancePerStep(t *testing.T) {
	resetRoutes()
	group := failoverGroup(0)

	// 第一个请求撞上 1 号: 两次尝试用尽 -> 冷却 + 降一档。
	if got := pickGroupItem(group).ID; got != 1 {
		t.Fatalf("首个请求应选中 1 号, 却选到 %d", got)
	}
	recordRouteFailure(group, 1, 1) // 第一次尝试: 未达总尝试次数, 不冷却
	recordRouteFailure(group, 1, 2) // 达到总尝试次数: 冷却 + 降档
	if got := routeScore(1); got != -1 {
		t.Fatalf("进入冷却应降一档, 1 号却还是 %d", got)
	}

	// 冷却期内它不会再被选中: 下一个请求直接落到 2 号。
	if got := pickGroupItem(group).ID; got != 2 {
		t.Fatalf("1 号冷却中应由 2 号接管, 却选到 %d", got)
	}
	recordRouteSuccess(group, 2) // 2 号成功 -> 开始亲和

	// 亲和期内连来 10 个请求, 全落在 2 号: 坏成员一次都不被试。
	for i := 0; i < 10; i++ {
		if got := pickGroupItem(group).ID; got != 2 {
			t.Fatalf("亲和期内第 %d 个请求应落在 2 号, 却选到 %d", i+1, got)
		}
	}
	if got := routeScore(1); got != -1 {
		t.Fatalf("这段时间 1 号没被调用, 分数不该变, 却成了 %d", got)
	}
	// 也没被负分喂住: 1 号不做事, 分仍按寿命自己往 0 走。
	ageScore(1, 1)
	if got := routeScore(1); got != 0 {
		t.Fatalf("放置一档周期后 1 号应回到中性, 却还是 %d", got)
	}

	// 冷却到期 + 亲和到期: 1 号重新获得一次机会 —— 恰好一次。
	expireCooldown(1)
	expireAffinity()
	if got := pickGroupItem(group).ID; got != 1 {
		t.Fatalf("各项时效都到期后应重新给 1 号一次机会, 却选到 %d", got)
	}
	// 它依然坏: 于是被当作惯犯多压一档(-1 -> -2), 重新被冷却与负分挡住。
	// 于是下一次机会从 5 分钟后变成 10 分钟后 —— 长期坏掉的成员不会每 5 分钟就回来撞一次。
	recordRouteFailure(group, 1, 2)
	if got := routeScore(1); got != -2 {
		t.Fatalf("重复失败应加深到 -2, 却得到 %d", got)
	}
	if got := pickGroupItem(group).ID; got != 2 {
		t.Fatalf("再次变坏后应由 2 号接管, 却选到 %d", got)
	}
}

// rawScore 读表里存着的原始值(不衰减), 用于断言"旧账有没有被销掉"。
func rawScore(itemID int) (int, bool) {
	routeMu.Lock()
	defer routeMu.Unlock()

	route := routes[900]
	if route == nil {
		return 0, false
	}
	score, ok := route.Scores[itemID]
	return score, ok
}

// baseRouteScore 读基础分(不含测活加权), 用于在"刚测活通过"之后断言基础分本身。
func baseRouteScore(itemID int) int {
	routeMu.Lock()
	defer routeMu.Unlock()

	if route := routes[900]; route != nil {
		return baseScore(route, itemID, time.Now().UnixMilli())
	}
	return 0
}

// TestRepeatOffenderBacksOffFurther 反复失败不退让的成员, 每次再失败都多压一档, 退避时间随之翻倍:
// -1 五分钟一次机会, -2 十分钟, -3 十五分钟封顶 —— 这就是"长期坏掉的成员不构成固定空转开销"的落点。
func TestRepeatOffenderBacksOffFurther(t *testing.T) {
	resetRoutes()
	group := failoverGroup(0)
	pickGroupItem(group) // 先建好路由状态。

	// 第一次失败: 偶发, 只压一档, 5 分钟后就能再试。
	if got := coolDown(1); got != -1 {
		t.Fatalf("首次失败应压一档到 -1, 却得到 %d", got)
	}
	ageScore(1, 1)
	if got := routeScore(1); got != 0 {
		t.Fatalf("一档周期后应回到中性, 却得到 %d", got)
	}

	// 熬回中性后又失败: 不再是偶发, 压两档, 要 10 分钟才回来。
	if got := coolDown(1); got != -2 {
		t.Fatalf("重复失败应加深到 -2, 却得到 %d", got)
	}
	ageScore(1, 1)
	if got := routeScore(1); got != -1 {
		t.Fatalf("负两档走一个周期应变成 -1, 却得到 %d", got)
	}
	ageScore(1, 1)
	if got := routeScore(1); got != 0 {
		t.Fatalf("负两档走满两个周期应回到中性, 却得到 %d", got)
	}

	// 第三次: 压满三档, 最久 15 分钟。
	if got := coolDown(1); got != -3 {
		t.Fatalf("第三次失败应压满到 -3, 却得到 %d", got)
	}
	ageScore(1, 3)
	if got := routeScore(1); got != 0 {
		t.Fatalf("负三档走满三个周期应回到中性, 却得到 %d", got)
	}

	// 封顶: 再坏也不会压到 -4, 它始终得自己走回中性。
	if got := coolDown(1); got != -3 {
		t.Fatalf("再失败也只到 -3(上限), 却得到 %d", got)
	}
	ageScore(1, 3)
	if got := routeScore(1); got != 0 {
		t.Fatalf("封顶后仍应能走回中性, 却得到 %d", got)
	}
}

// TestSuccessClearsRepeatRecord 调通一轮就把惯犯记录销掉: 之后偶发的一次失败只压一档, 不该拿旧账加重。
func TestSuccessClearsRepeatRecord(t *testing.T) {
	resetRoutes()
	group := failoverGroup(0)
	pickGroupItem(group)

	coolDown(1)
	ageScore(1, 1)
	if got := coolDown(1); got != -2 {
		t.Fatalf("前置条件不成立: 重复失败应为 -2, 却是 %d", got)
	}
	ageScore(1, 2) // 这份惩罚已经作废(衰减到 0), 但表里还留着旧值。
	if got := routeScore(1); got != 0 {
		t.Fatalf("前置条件不成立: 应已衰减到 0, 却是 %d", got)
	}

	recordRouteSuccess(group, 1)
	if score, ok := rawScore(1); ok {
		t.Fatalf("调通一轮后表里不该再留负分旧账, 却留着 %d", score)
	}

	// 记录已清: 下一次失败重新从一档起算(5 分钟), 而不是 -3。
	if got := coolDown(1); got != -1 {
		t.Fatalf("成功后再失败应重新从 -1 起算, 却得到 %d", got)
	}
}

// TestProbeSuccessClearsRepeatRecord 测活通过同真实成功一样销掉惯犯记录: 它也是对"这个成员现在能用"的确认。
func TestProbeSuccessClearsRepeatRecord(t *testing.T) {
	resetRoutes()
	group := failoverGroup(0)
	pickGroupItem(group)

	coolDown(1)
	ageScore(1, 1)
	if got := coolDown(1); got != -2 {
		t.Fatalf("前置条件不成立: 重复失败应为 -2, 却是 %d", got)
	}
	ageScore(1, 2)

	recordProbe(group, 1, true, "", 12)
	if score, ok := rawScore(1); ok {
		t.Fatalf("体检通过后表里不该再留负分旧账, 却留着 %d", score)
	}

	// 这里看基础分: 测活通过给的 +3 加权还在有效期内, 有效分此刻是正数, 但它不是"健康分账本"里的余额。
	coolDown(1)
	if got := baseRouteScore(1); got != -1 {
		t.Fatalf("体检通过后再失败应重新从 -1 起算, 却得到 %d", got)
	}
}
