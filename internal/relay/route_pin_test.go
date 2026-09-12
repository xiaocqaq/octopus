package relay

import (
	"testing"
	"time"

	"github.com/bestruirui/octopus/internal/model"
)

// failoverGroup 构造一个含三个成员的故障转移分组, 成员按优先级升序。
func failoverGroup(pinned int) model.Group {
	return model.Group{
		ID:           900,
		Mode:         model.GroupModeFailover,
		PinnedItemID: pinned,
		RelayConfig:  model.DefaultGroupRelayConfig(),
		Items: []model.GroupItem{
			{ID: 1, Priority: 1},
			{ID: 2, Priority: 2},
			{ID: 3, Priority: 3},
		},
	}
}

// resetRoutes 清掉被测分组的进程内路由状态, 使各用例互不影响。
func resetRoutes() { ResetRouteState(900) }

// TestPinnedItemWins 钉住的成员应越过优先级顺序被选中。
func TestPinnedItemWins(t *testing.T) {
	resetRoutes()
	if got := pickGroupItem(failoverGroup(3)); got.ID != 3 {
		t.Fatalf("钉住 3 号, 却选中 %d", got.ID)
	}
}

// TestNoPinFollowsPriority 未钉住时仍按优先级选第一个。
func TestNoPinFollowsPriority(t *testing.T) {
	resetRoutes()
	if got := pickGroupItem(failoverGroup(0)); got.ID != 1 {
		t.Fatalf("未钉住应选 1 号, 却选中 %d", got.ID)
	}
}

// TestPinnedInCooldownFallsOver 钉住的成员处于冷却时应让位, 而不是把请求压在它上面。
func TestPinnedInCooldownFallsOver(t *testing.T) {
	resetRoutes()
	group := failoverGroup(3)
	// 让 3 号连续失败到达总尝试次数, 使其进入冷却。
	pickGroupItem(group)
	recordRouteFailure(group, 3, group.RelayConfig.MemberMaxAttempts)

	got := pickGroupItem(group)
	if got.ID == 3 {
		t.Fatalf("3 号在冷却中, 不应再被选中")
	}
	if got.ID == 0 {
		t.Fatal("应退回按优先级选路, 却没选出成员")
	}
}

// TestPinnedRecoversAfterCooldown 冷却到期后钉住的成员应重新胜出, 且冷却条目被清掉。
func TestPinnedRecoversAfterCooldown(t *testing.T) {
	resetRoutes()
	group := failoverGroup(3)
	pickGroupItem(group)

	// 直接写入一个已过期的冷却截止时间, 模拟冷却到期。
	routeMu.Lock()
	route := groupRouteLocked(group)
	route.Cooldowns[3] = time.Now().UnixMilli() - 1000
	routeMu.Unlock()

	if got := pickGroupItem(group); got.ID != 3 {
		t.Fatalf("冷却到期后应切回 3 号, 却选中 %d", got.ID)
	}
	routeMu.Lock()
	_, still := routes[900].Cooldowns[3]
	routeMu.Unlock()
	if still {
		t.Error("到期的冷却条目应被清除")
	}
}

// TestPinnedMissingItemIgnored 钉住一个已不存在的成员时应退回正常选路。
func TestPinnedMissingItemIgnored(t *testing.T) {
	resetRoutes()
	if got := pickGroupItem(failoverGroup(999)); got.ID != 1 {
		t.Fatalf("钉住的成员不存在时应选 1 号, 却选中 %d", got.ID)
	}
}

// TestManualModeIgnoresPin 手动模式不受钉住影响, 仍只认人工指定的成员。
func TestManualModeIgnoresPin(t *testing.T) {
	resetRoutes()
	group := failoverGroup(3)
	group.Mode = model.GroupModeManual
	group.ActiveItemID = 2
	if got := pickGroupItem(group); got.ID != 2 {
		t.Fatalf("手动模式应选人工指定的 2 号, 却选中 %d", got.ID)
	}
}
