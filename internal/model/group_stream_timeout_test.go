package model

import "testing"

// TestStreamTotalTimeoutDefault 新分组的默认总时长上限为 180 秒。
func TestStreamTotalTimeoutDefault(t *testing.T) {
	if got := DefaultGroupRelayConfig().MemberStreamTotalTimeoutSeconds; got != 180 {
		t.Fatalf("默认流式总超时应为 180 秒, 实际 %d", got)
	}
}

// TestNormalizeFillsStreamTotalTimeout 老分组的配置 JSON 里没有该键, 反序列化后为 0, 归一化时应补成默认值。
// 这条是该功能不写数据迁移的依据: 存量分组读出来必须自动带上预算, 否则计时器会以 0 秒立即到期而掐断每一轮流式响应。
func TestNormalizeFillsStreamTotalTimeout(t *testing.T) {
	// 只缺总时长这一个键, 其余字段照旧: 整体为零值会走 normalize 里的"全量替换"分支, 测不到单键补齐。
	config := GroupRelayConfig{
		MemberMaxAttempts:                     2,
		MemberRetryIntervalSeconds:            3,
		MemberNonStreamResponseTimeoutSeconds: 120,
		MemberStreamFirstEventTimeoutSeconds:  30,
		MemberCooldownSeconds:                 60,
		MemberAffinitySeconds:                 300,
	}
	NormalizeGroupRelayConfig(&config)

	if config.MemberStreamTotalTimeoutSeconds != 180 {
		t.Fatalf("缺失的总超时应补成 180 秒, 实际 %d", config.MemberStreamTotalTimeoutSeconds)
	}
	// 补齐不得顺手改动其他已配置的字段。
	if config.MemberStreamFirstEventTimeoutSeconds != 30 || config.MemberCooldownSeconds != 60 {
		t.Fatalf("补齐总超时不应改动其他字段: 首事件 %d, 冷却 %d",
			config.MemberStreamFirstEventTimeoutSeconds, config.MemberCooldownSeconds)
	}
}

// TestNormalizeKeepsCustomStreamTotalTimeout 已经配过的值不能被默认值覆盖。
func TestNormalizeKeepsCustomStreamTotalTimeout(t *testing.T) {
	config := DefaultGroupRelayConfig()
	config.MemberStreamTotalTimeoutSeconds = 600
	NormalizeGroupRelayConfig(&config)

	if config.MemberStreamTotalTimeoutSeconds != 600 {
		t.Fatalf("已配置的 600 秒不应被覆盖, 实际 %d", config.MemberStreamTotalTimeoutSeconds)
	}
}
