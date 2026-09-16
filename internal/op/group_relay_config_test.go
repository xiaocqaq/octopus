package op

import (
	"testing"

	"github.com/bestruirui/octopus/internal/model"
)

// TestGroupSnapshotFillsRelayConfigDefaults 读取路径必须补齐 Relay 配置的空值。
// 存量的分组记录里没有后来新增的键(如流式总时长预算), 反序列化后是 0;
// 转发侧把 0 当成"预算 0 秒"会让总时长闸门在请求发起瞬间到期, 掐断每一轮流式响应。
// 这个断言锁的是读取出口: 列表/详情/按名称取都走 groupSnapshot, 补在这里就全覆盖。
func TestGroupSnapshotFillsRelayConfigDefaults(t *testing.T) {
	// 模拟一条老记录: 配置里只有当年就有的那几项。
	legacy := model.Group{ID: 901, Name: "legacy", Mode: model.GroupModeFailover, RelayConfig: model.GroupRelayConfig{
		MemberMaxAttempts:                     2,
		MemberRetryIntervalSeconds:            3,
		MemberNonStreamResponseTimeoutSeconds: 120,
		MemberStreamFirstEventTimeoutSeconds:  30,
		MemberCooldownSeconds:                 60,
		MemberAffinitySeconds:                 300,
	}}

	snapshot := groupSnapshot(legacy)
	if snapshot.RelayConfig.MemberStreamTotalTimeoutSeconds != 180 {
		t.Fatalf("老记录的流式总时长预算应补成 180, 却得到 %d",
			snapshot.RelayConfig.MemberStreamTotalTimeoutSeconds)
	}
	// 显式配过的值不能被覆盖。
	legacy.RelayConfig.MemberStreamTotalTimeoutSeconds = 42
	if got := groupSnapshot(legacy).RelayConfig.MemberStreamTotalTimeoutSeconds; got != 42 {
		t.Fatalf("已配置的预算应保持 42, 却得到 %d", got)
	}
	// 零值配置整体补默认, 一个都不能漏。
	empty := groupSnapshot(model.Group{ID: 902, Name: "empty"})
	if empty.RelayConfig != model.DefaultGroupRelayConfig() {
		t.Fatalf("空配置应补成默认值, 却得到 %+v", empty.RelayConfig)
	}
}
