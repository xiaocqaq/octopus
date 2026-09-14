package relay

import (
	"testing"

	"github.com/bestruirui/octopus/internal/model"
)

// TestOrderGroupItemsByScore 健康分高的成员应排到配置优先级之前, 同分才看优先级。
func TestOrderGroupItemsByScore(t *testing.T) {
	group := failoverGroup(0)
	route := &RouteState{Scores: map[int]int{1: -1, 3: 1}}

	got := orderGroupItems(group, route)
	want := []int{3, 2, 1}
	if len(got) != len(want) {
		t.Fatalf("成员数不对: 得到 %d, 期望 %d", len(got), len(want))
	}
	for i, id := range want {
		if got[i].ID != id {
			t.Fatalf("第 %d 位应为 %d 号, 却得到 %d 号 (完整顺序 %v)", i+1, id, got[i].ID, itemIDs(got))
		}
	}
}

// TestOrderGroupItemsKeepsPriorityWhenFlat 全部成员都无偏移时应原样返回, 不改变优先级顺序。
func TestOrderGroupItemsKeepsPriorityWhenFlat(t *testing.T) {
	group := failoverGroup(0)
	got := orderGroupItems(group, &RouteState{Scores: map[int]int{}})

	if len(got) != 3 || got[0].ID != 1 || got[1].ID != 2 || got[2].ID != 3 {
		t.Fatalf("无偏移时应按优先级排列, 却得到 %v", itemIDs(got))
	}
}

// TestSuccessStreakPromotesItem 连续成功到阈值后, 低优先级成员应越过前面的成员被选中。
func TestSuccessStreakPromotesItem(t *testing.T) {
	resetRoutes()
	group := failoverGroup(0)
	pickGroupItem(group) // 先建好路由状态, 记成功时才有的可写。

	for range routeSuccessStreak {
		recordRouteSuccess(group, 3)
	}

	if got := pickGroupItem(group); got.ID != 3 {
		t.Fatalf("3 号连续成功 %d 次后应被选中, 却选中 %d 号", routeSuccessStreak, got.ID)
	}
}

// TestFailuresKeepScoreInRange 反复进入冷却不应让健康分越过下限。
func TestFailuresKeepScoreInRange(t *testing.T) {
	resetRoutes()
	group := failoverGroup(0)
	pickGroupItem(group)

	for range routeScoreMax * 2 {
		recordRouteFailure(group, 1, group.RelayConfig.MemberMaxAttempts)
	}

	routeMu.Lock()
	score := routes[900].Scores[1]
	routeMu.Unlock()
	if score != -routeScoreMax {
		t.Fatalf("健康分应停在下限 %d, 却是 %d", -routeScoreMax, score)
	}
}

// TestSuccessesKeepScoreInRange 反复成功不应让健康分越过上限。
func TestSuccessesKeepScoreInRange(t *testing.T) {
	resetRoutes()
	group := failoverGroup(0)
	pickGroupItem(group)

	for range routeScoreMax * 2 * routeSuccessStreak {
		recordRouteSuccess(group, 1)
	}

	routeMu.Lock()
	score := routes[900].Scores[1]
	routeMu.Unlock()
	if score != routeScoreMax {
		t.Fatalf("健康分应停在上限 %d, 却是 %d", routeScoreMax, score)
	}
}

// itemIDs 抽出成员顺序, 供失败信息展示。
func itemIDs(items []model.GroupItem) []int {
	ids := make([]int, len(items))
	for i, item := range items {
		ids[i] = item.ID
	}
	return ids
}
