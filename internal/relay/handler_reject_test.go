package relay

import (
	"strings"
	"testing"
	"time"

	"github.com/bestruirui/octopus/internal/model"
)

// TestNoAvailableMemberReason 报文要如实说明"为什么一个成员都选不出来":
// 分开报"没有启用的成员""手动模式没指定成员""成员在冷却及其恢复时间"三种成因,
// 客户端才能判断这是该重试的临时状态还是该去改配置的错。
func TestNoAvailableMemberReason(t *testing.T) {
	resetRoutes()
	defer resetRoutes()

	// 分组里没有被启用的成员: 手动禁用掉的成员不进 Items, 故这里是空的。
	empty := model.Group{ID: 900, Name: "empty", Mode: model.GroupModeFailover, RelayConfig: model.DefaultGroupRelayConfig()}
	if reason := noAvailableMemberReason(empty); !strings.Contains(reason, "no enabled member") {
		t.Fatalf("没有启用成员时该如实说明, 却得到 %q", reason)
	}

	// 手动模式: 成员都在, 只是人工没指定用哪个。
	manual := model.Group{
		ID: 900, Name: "manual", Mode: model.GroupModeManual, RelayConfig: model.DefaultGroupRelayConfig(),
		Items: []model.GroupItem{{ID: 1}, {ID: 2}},
	}
	if reason := noAvailableMemberReason(manual); !strings.Contains(reason, "no active member in manual mode") {
		t.Fatalf("手动模式未指定成员时该如实说明, 却得到 %q", reason)
	}

	// 3 个成员里 1 个在冷却: 报出冷却个数与最早恢复时间, 且向上取整不出现 "0s"。
	pickGroupItem(failoverGroup(0)) // 先建好路由状态。
	routeMu.Lock()
	routes[900].Cooldowns[2] = time.Now().UnixMilli() + 2400
	routeMu.Unlock()

	reason := noAvailableMemberReason(failoverGroup(0))
	if !strings.Contains(reason, "1 of 3 member(s) cooling down") {
		t.Fatalf("应报出冷却成员个数, 却得到 %q", reason)
	}
	if !strings.Contains(reason, "earliest recovers in 3s") {
		t.Fatalf("2.4 秒应向上取整为 3 秒, 却得到 %q", reason)
	}

	// 冷却中的成员正被另一个请求占着探测名额: 选不出目标但没人"在冷却"的错觉要避免。
	routeMu.Lock()
	routes[900].Cooldowns = map[int]int64{}
	routes[900].ProbeItemID = 2
	routeMu.Unlock()
	if reason := noAvailableMemberReason(failoverGroup(0)); !strings.Contains(reason, "being probed by another request") {
		t.Fatalf("探测名额被占用时该如实说明, 却得到 %q", reason)
	}
}
