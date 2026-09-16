package relay

import (
	"testing"

	"github.com/bestruirui/octopus/internal/model"
)

// pinnedFailCount 读出强制成员的连续失败计数, 供断言使用。
func pinnedFailCount(groupID, itemID int) int {
	routeMu.Lock()
	defer routeMu.Unlock()
	route := routes[groupID]
	if route == nil {
		return 0
	}
	return route.pinnedFailures[itemID]
}

// TestPinnedToleratesFailuresBelowThreshold 强制成员连续失败未超过阈值时应继续被选中, 不冷却也不降档。
func TestPinnedToleratesFailuresBelowThreshold(t *testing.T) {
	resetRoutes()
	group := failoverGroup(3)
	pickGroupItem(group) // 先建好路由状态。

	// 阈值内的每一轮失败都不该让它让位: 单请求内的失败次数按 1 计, 由累计计数决定是否冷却。
	for i := 1; i <= routePinnedFailureThreshold; i++ {
		if got := pickGroupItem(group); got.ID != 3 {
			t.Fatalf("第 %d 次失败前仍应选中强制成员 3, 却选中 %d", i, got.ID)
		}
		if cooling := recordRouteFailure(group, 3, 1); cooling {
			t.Fatalf("连续失败 %d 次尚未超过阈值 %d, 不应冷却", i, routePinnedFailureThreshold)
		}
	}

	if n := pinnedFailCount(900, 3); n != routePinnedFailureThreshold {
		t.Fatalf("连续失败计数应为 %d, 实际 %d", routePinnedFailureThreshold, n)
	}
	routeMu.Lock()
	score := routes[900].Scores[3]
	routeMu.Unlock()
	if score != 0 {
		t.Fatalf("未进入冷却就不应降档, 健康分却为 %d", score)
	}
	if got := pickGroupItem(group); got.ID != 3 {
		t.Fatalf("仍在阈值内应继续选中强制成员 3, 却选中 %d", got.ID)
	}
}

// TestPinnedCoolsDownAfterThreshold 强制成员连续失败超过阈值后应进入冷却并降一档, 让位给其他成员。
func TestPinnedCoolsDownAfterThreshold(t *testing.T) {
	resetRoutes()
	group := failoverGroup(3)
	pickGroupItem(group)

	for i := 0; i <= routePinnedFailureThreshold; i++ {
		recordRouteFailure(group, 3, 1) // 第 routePinnedFailureThreshold+1 次才越线。
	}

	routeMu.Lock()
	_, cooling := routes[900].Cooldowns[3]
	score := routes[900].Scores[3]
	routeMu.Unlock()
	if !cooling {
		t.Fatalf("连续失败超过 %d 次后应进入冷却", routePinnedFailureThreshold)
	}
	if score != -1 {
		t.Fatalf("进入冷却时应降一档, 健康分应为 -1, 实际 %d", score)
	}
	if got := pickGroupItem(group); got.ID == 3 {
		t.Fatal("强制成员冷却中不应再被选中")
	}
}

// TestPinnedSuccessClearsPenalty 强制成员任意一次调通即清除负面效果: 连续失败清零, 降档取消。
func TestPinnedSuccessClearsPenalty(t *testing.T) {
	resetRoutes()
	group := failoverGroup(3)
	pickGroupItem(group)

	// 先失败到进入冷却并降档, 再把冷却拨到已过期, 模拟冷却期结束后的第一次重试。
	for i := 0; i <= routePinnedFailureThreshold; i++ {
		recordRouteFailure(group, 3, 1)
	}
	routeMu.Lock()
	routes[900].Cooldowns[3] = 0 // 已过期, 下一次选路会放行并清掉该条目。
	routeMu.Unlock()

	recordRouteSuccess(group, 3)

	if n := pinnedFailCount(900, 3); n != 0 {
		t.Fatalf("调通后连续失败应清零, 实际 %d", n)
	}
	routeMu.Lock()
	score := routes[900].Scores[3]
	routeMu.Unlock()
	if score != 0 {
		t.Fatalf("调通后降档应取消, 健康分应回到 0, 实际 %d", score)
	}
}

// TestPinnedDoesNotKeepPositiveScoreOnSuccess 调通只取消降档, 不该抹掉此前连续成功挣来的加分。
func TestPinnedDoesNotKeepPositiveScoreOnSuccess(t *testing.T) {
	resetRoutes()
	group := failoverGroup(3)
	pickGroupItem(group)

	routeMu.Lock()
	routes[900].Scores[3] = 2
	routeMu.Unlock()

	recordRouteSuccess(group, 3)

	routeMu.Lock()
	score := routes[900].Scores[3]
	routeMu.Unlock()
	if score != 2 {
		t.Fatalf("调通只应取消降档, 正分 2 应保留, 实际 %d", score)
	}
}

// TestRegularItemKeepsAttemptBasedCooldown 常规成员仍按总尝试次数冷却, 不受强制阈值影响。
func TestRegularItemKeepsAttemptBasedCooldown(t *testing.T) {
	resetRoutes()
	group := failoverGroup(0) // 不强制, 全员走常规路径。
	pickGroupItem(group)

	// 未达总尝试次数不冷却。
	if cooling := recordRouteFailure(group, 1, group.RelayConfig.MemberMaxAttempts-1); cooling {
		t.Fatal("常规成员未达总尝试次数不应冷却")
	}
	// 达到总尝试次数即冷却, 不必等到强制阈值。
	if cooling := recordRouteFailure(group, 1, group.RelayConfig.MemberMaxAttempts); !cooling {
		t.Fatalf("常规成员达到总尝试次数 %d 应冷却", group.RelayConfig.MemberMaxAttempts)
	}
	if n := pinnedFailCount(900, 1); n != 0 {
		t.Fatalf("常规成员不应累计强制失败计数, 实际 %d", n)
	}
}

// TestRepinResetsFailureCount 人工重新指定强制成员时应清掉旧账, 让它不因上一轮的历史更快让位。
func TestRepinResetsFailureCount(t *testing.T) {
	resetRoutes()
	group := failoverGroup(3)
	pickGroupItem(group)

	recordRouteFailure(group, 3, 1)
	recordRouteFailure(group, 3, 1)
	if n := pinnedFailCount(900, 3); n != 2 {
		t.Fatalf("两次失败后计数应为 2, 实际 %d", n)
	}

	ReleaseItemCooldown(group.ID, 3)

	if n := pinnedFailCount(900, 3); n != 0 {
		t.Fatalf("重新指定后连续失败计数应清零, 实际 %d", n)
	}
}

// TestPinnedThresholdExceedsAttempts 强制阈值必须高于常规总尝试次数, 否则"连续错误超过 5 次"就落不到实处。
func TestPinnedThresholdExceedsAttempts(t *testing.T) {
	attempts := model.DefaultGroupRelayConfig().MemberMaxAttempts
	if routePinnedFailureThreshold <= attempts {
		t.Fatalf("强制阈值 %d 应大于常规总尝试次数 %d", routePinnedFailureThreshold, attempts)
	}
}
