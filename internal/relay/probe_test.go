package relay

import (
	"testing"

	"github.com/bestruirui/octopus/internal/model"
)

// routeScore 加锁读取指定成员当前的健康分, 供断言使用。
func routeScore(itemID int) int {
	routeMu.Lock()
	defer routeMu.Unlock()

	route := routes[900]
	if route == nil {
		return 0
	}
	return route.Scores[itemID]
}

// TestRecordProbeSuccessPromotesToTop 测活通过的成员健康分应抬到满档, 从而越过配置优先级排到最前。
// 这是"健康的优先级更高"的核心断言: 测活是人工体检, 结论要立刻反映在选路顺序上。
func TestRecordProbeSuccessPromotesToTop(t *testing.T) {
	resetRoutes()
	pickGroupItem(failoverGroup(0)) // 先建好路由状态。

	// 3 号优先级最低, 测活通过后应排到 1 号之前。
	result := recordProbe(failoverGroup(0), 3, true, "", 42)
	if !result.OK {
		t.Fatalf("测活结论应为通过")
	}
	if got := routeScore(3); got != routeScoreMax {
		t.Fatalf("测活通过后健康分应为 %d, 却得到 %d", routeScoreMax, got)
	}

	ordered := orderGroupItems(failoverGroup(0), routes[900])
	if len(ordered) != 3 || ordered[0].ID != 3 {
		t.Fatalf("3 号测活通过后应排在最前, 却得到 %v", itemIDs(ordered))
	}
	if got := pickGroupItem(failoverGroup(0)); got.ID != 3 {
		t.Fatalf("3 号测活通过后应被选中, 却选中 %d 号", got.ID)
	}
}

// TestRecordProbeFailureDemotesBelowUntested 测活失败的成员应下沉, 让位给未测活的成员。
// 失败不直接冷却: 一次"此刻不通"不该把成员关进小黑屋, 只该让它在顺序上让位。
func TestRecordProbeFailureDemotesBelowUntested(t *testing.T) {
	resetRoutes()
	pickGroupItem(failoverGroup(0))

	recordProbe(failoverGroup(0), 1, false, "boom", 7)
	if got := routeScore(1); got != -1 {
		t.Fatalf("测活失败后健康分应为 -1, 却得到 %d", got)
	}
	if _, cooling := routes[900].Cooldowns[1]; cooling {
		t.Fatalf("测活失败不应直接让成员进入冷却")
	}
	if got := pickGroupItem(failoverGroup(0)); got.ID != 2 {
		t.Fatalf("1 号测活失败后应让位给 2 号, 却选中 %d 号", got.ID)
	}
}

// TestRecordProbeSuccessClearsCooldown 测活调通应解除成员既有的冷却与强制失败旧账。
// 这与 recordRouteSuccess 对探测成员的处理一致: 调通即证明它已恢复。
func TestRecordProbeSuccessClearsCooldown(t *testing.T) {
	resetRoutes()
	group := failoverGroup(0)
	pickGroupItem(group)

	routes[900].Cooldowns[3] = 1 << 62 // 远未到期的冷却。
	routes[900].pinnedFailures[3] = 99

	recordProbe(group, 3, true, "", 5)

	if _, cooling := routes[900].Cooldowns[3]; cooling {
		t.Fatalf("测活通过应解除冷却")
	}
	if got := routes[900].pinnedFailures[3]; got != 0 {
		t.Fatalf("测活通过应清掉强制失败计数, 却残留 %d", got)
	}
}

// TestRecordProbeKeepsCurrentRoute 测活不应改动当前路由与亲和。
// 测活是人工发起的独立尝试, 不代表"正在承载客户端请求的那条路由"调通了, 越权改动会让亲和失效。
func TestRecordProbeKeepsCurrentRoute(t *testing.T) {
	resetRoutes()
	group := failoverGroup(0)
	first := pickGroupItem(group) // 当前路由为 1 号。
	if first.ID != 1 {
		t.Fatalf("前置条件不成立: 当前成员应为 1 号, 却是 %d 号", first.ID)
	}

	recordProbe(group, 3, true, "", 5)

	state := routes[900]
	if state.CurrentItemID != 1 {
		t.Fatalf("测活不该改动当前路由, 期望 1 号, 却变成 %d 号", state.CurrentItemID)
	}
	if state.AffinityUntil != 0 {
		t.Fatalf("测活不该开始亲和, 却把亲和截止设为 %d", state.AffinityUntil)
	}
}

// TestRecordProbeStoresResult 测活结论要落进路由状态, 供界面展示体检结果。
func TestRecordProbeStoresResult(t *testing.T) {
	resetRoutes()
	pickGroupItem(failoverGroup(0))

	recordProbe(failoverGroup(0), 2, false, "upstream 500", 1234)

	stored, ok := routes[900].Probes[2]
	if !ok {
		t.Fatalf("测活结论未落进路由状态")
	}
	if stored.OK || stored.Message != "upstream 500" || stored.LatencyMS != 1234 {
		t.Fatalf("落库的结论与预期不符: %+v", stored)
	}
	if stored.ProbedAt == 0 {
		t.Fatalf("结论应带上产生时间")
	}
}

// TestProbeSuccessOutranksRealCallScore 测活通过(满档)应压过真实调用累计出的较低正分。
// 满档是"人工体检合格"的最强信号, 高于靠连续成功一轮轮挣来的中间档。
func TestProbeSuccessOutranksRealCallScore(t *testing.T) {
	resetRoutes()
	group := failoverGroup(0)
	pickGroupItem(group)

	// 让 1 号靠连续成功挣到 1 档, 随后测活 3 号通过。
	for range routeSuccessStreak {
		recordRouteSuccess(group, 1)
	}
	if got := routeScore(1); got != 1 {
		t.Fatalf("前置条件不成立: 1 号健康分应为 1, 却是 %d", got)
	}
	recordProbe(group, 3, true, "", 5)

	if got := pickGroupItem(group); got.ID != 3 {
		t.Fatalf("测活满档的 3 号应压过 1 号, 却选中 %d 号", got.ID)
	}
}

// TestProbeManualModeStoresOnlyResult 手动模式没有选路队列, 测活不应改动健康分与冷却, 但结论要落下来。
// 结论落下来是给界面看的: 手动模式同样需要"这条此刻通不通"; 而改动健康分会污染切回故障转移后的选路。
func TestProbeManualModeStoresOnlyResult(t *testing.T) {
	resetRoutes()
	group := failoverGroup(0)
	group.Mode = model.GroupModeManual

	result := recordProbe(group, 3, true, "", 5)

	if result.OK != true {
		t.Fatalf("手动模式下测活结论本身仍应返回给界面")
	}
	route := routes[900]
	if route == nil {
		t.Fatalf("手动模式也应留下结论")
	}
	if len(route.Scores) != 0 || len(route.Cooldowns) != 0 {
		t.Fatalf("手动模式不该产生健康分与冷却, 却得到 scores=%v cooldowns=%v", route.Scores, route.Cooldowns)
	}
	if stored, ok := route.Probes[3]; !ok || !stored.OK {
		t.Fatalf("手动模式的测活结论未落进路由状态: %+v", route.Probes)
	}
	// 手动模式读到的结论要能从状态接口拿到, 否则界面永远看不到这次体检结果。
	state := RouteStateOf(group)
	if stored, ok := state.Probes[3]; !ok || !stored.OK {
		t.Fatalf("手动模式的状态接口未带出测活结论: %+v", state.Probes)
	}
}

// TestProbeResultClearedWithRemovedItem 成员被移除后, 它的测活结论应随之清理, 不残留占内存。
func TestProbeResultClearedWithRemovedItem(t *testing.T) {
	resetRoutes()
	pickGroupItem(failoverGroup(0))
	recordProbe(failoverGroup(0), 3, true, "", 5)

	// 只剩 1 号和 2 号时再取一次状态, 3 号的结论应被清掉。
	shrunk := failoverGroup(0)
	shrunk.Items = shrunk.Items[:2]
	groupRouteLocked(shrunk)

	if _, exists := routes[900].Probes[3]; exists {
		t.Fatalf("3 号已被移除, 其测活结论应被清理")
	}
}
