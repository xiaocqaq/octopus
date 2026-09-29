package op

import (
	"testing"

	"github.com/bestruirui/octopus/internal/model"
)

// 这一组测试盯的是"排除项在编辑任务时会不会被悄悄清空"。
// 更新走的是先清空目标再重建, 请求里带不带 excluded_keys 决定了用户删过的凭据会不会复活,
// 而 nil 与空切片在 Go 里长得几乎一样, 正是最容易写错的一处。

func TestTargetRowsOfKeepsExclusionsWhenRequestOmitsThem(t *testing.T) {
	existing := []model.ScheduledProbeTarget{
		{ChannelID: 2, ModelName: "deepseek-chat", ExcludedKeys: []string{"z"}},
	}
	requested := []model.ScheduledProbeTargetRequest{
		{ChannelID: 2, ModelName: "deepseek-chat"},
	}

	targets := targetRowsOf(requested, existing)

	expected := []string{"z"}
	if len(targets) != 1 || len(targets[0].ExcludedKeys) != 1 || targets[0].ExcludedKeys[0] != expected[0] {
		t.Fatalf("expected 排除项 %v 被沿用, actual %+v", expected, targets)
	}
}

func TestTargetRowsOfClearsExclusionsWhenRequestSendsEmptySlice(t *testing.T) {
	existing := []model.ScheduledProbeTarget{
		{ChannelID: 2, ModelName: "deepseek-chat", ExcludedKeys: []string{"z"}},
	}
	requested := []model.ScheduledProbeTargetRequest{
		{ChannelID: 2, ModelName: "deepseek-chat", ExcludedKeys: []string{}},
	}

	targets := targetRowsOf(requested, existing)

	if len(targets) != 1 || targets[0].ExcludedKeys == nil {
		t.Fatalf("expected 排除项被清成空切片, actual %+v", targets)
	}
	if len(targets[0].ExcludedKeys) != 0 {
		t.Fatalf("expected 排除项为空, actual %v", targets[0].ExcludedKeys)
	}
}

func TestTargetRowsOfAppliesExplicitExclusions(t *testing.T) {
	existing := []model.ScheduledProbeTarget{
		{ChannelID: 2, ModelName: "deepseek-chat", ExcludedKeys: []string{"旧"}},
	}
	requested := []model.ScheduledProbeTargetRequest{
		{ChannelID: 2, ModelName: "deepseek-chat", ExcludedKeys: []string{"新"}},
	}

	targets := targetRowsOf(requested, existing)

	if len(targets) != 1 || len(targets[0].ExcludedKeys) != 1 || targets[0].ExcludedKeys[0] != "新" {
		t.Fatalf("expected 显式排除项 [新] 覆盖旧值, actual %+v", targets)
	}
}

// 新建任务没有既有目标, 排除项本来就是空的; 这里确认传 nil 的 existing 不会 panic。
func TestTargetRowsOfHandlesMissingExistingTargets(t *testing.T) {
	requested := []model.ScheduledProbeTargetRequest{
		{ChannelID: 3, ModelName: "grok-fail"},
	}

	targets := targetRowsOf(requested, nil)

	if len(targets) != 1 || targets[0].ExcludedKeys != nil {
		t.Fatalf("expected 单个目标且无排除项, actual %+v", targets)
	}
}

// 界面上多选两个渠道的并集很容易点出重复的 (渠道, 模型), 去重是既有行为, 改这里时不能被弄丢。
func TestTargetRowsOfDedupesRepeatedTargets(t *testing.T) {
	requested := []model.ScheduledProbeTargetRequest{
		{ChannelID: 2, ModelName: "grok-4.7"},
		{ChannelID: 2, ModelName: "grok-4.7"},
	}

	targets := targetRowsOf(requested, nil)

	if len(targets) != 1 {
		t.Fatalf("expected 去重后 1 个目标, actual %d 个: %+v", len(targets), targets)
	}
}

func TestExcludeKeyAddsRemovesAndKeepsInputIntact(t *testing.T) {
	original := []string{"a", "b"}

	added := excludeKey(original, "c", true)
	if len(added) != 3 || added[2] != "c" {
		t.Fatalf("expected 追加后为 [a b c], actual %v", added)
	}
	if len(original) != 2 {
		t.Fatalf("expected 入参不被改动, actual %v", original)
	}

	removed := excludeKey([]string{"a", "b", "c"}, "b", false)
	if len(removed) != 2 || removed[0] != "a" || removed[1] != "c" {
		t.Fatalf("expected 移除后为 [a c], actual %v", removed)
	}
}

// 同一行被连点两次不该在库里堆出重复项, 否则"恢复"要按次数点很多下才清得干净。
func TestExcludeKeyDoesNotDuplicateRepeatedAdds(t *testing.T) {
	once := excludeKey(nil, "z", true)
	twice := excludeKey(once, "z", true)

	if len(twice) != 1 || twice[0] != "z" {
		t.Fatalf("expected 重复隐藏后仍为 [z], actual %v", twice)
	}
}

// 空名称是合法凭据名(界面上显示成 #<授权ID>), 隐藏它不能变成"什么都不做"。
func TestExcludeKeyTreatsEmptyNameAsARealKey(t *testing.T) {
	keys := excludeKey(nil, "", true)

	if len(keys) != 1 || keys[0] != "" {
		t.Fatalf("expected 空名称被记入排除项, actual %v", keys)
	}
}

// seedProbeGrantCaches 造一份最小渠道配置: 一个渠道、一个模型、三条凭据及其授权。
// 直接灌缓存而不建库: ScheduledProbeGrantIDs 只读缓存, 走库只会让测试变慢, 不会多验证任何东西。
func seedProbeGrantCaches(t *testing.T) {
	t.Helper()
	clearProbeGrantCaches := func() {
		channelCache.Clear()
		channelKeyCache.Clear()
		channelModelCache.Clear()
		channelGrantCache.Clear()
	}
	t.Cleanup(clearProbeGrantCaches)
	clearProbeGrantCaches()

	channelCache.Set(2, model.Channel{ID: 2, ChannelConfig: model.ChannelConfig{Name: "Demo", Enabled: true}})
	channelModelCache.Set(20, model.ChannelModel{ID: 20, ChannelID: 2, Name: "deepseek-chat"})
	for index, name := range []string{"a", "b", "c"} {
		keyID := 30 + index
		channelKeyCache.Set(keyID, model.ChannelKey{
			ID:               keyID,
			ChannelID:        2,
			ChannelKeyConfig: model.ChannelKeyConfig{Name: name, Enabled: true},
		})
		channelGrantCache.Set(40+index, model.ChannelGrant{ID: 40 + index, ChannelModelID: 20, ChannelKeyID: keyID})
	}
}

// 轮转与界面出行读的是同一个函数, 故这条断言同时守住两件事:
// 被删掉的凭据不再被探测, 也不再出现在卡片上。
func TestScheduledProbeGrantIDsSkipsExcludedKeys(t *testing.T) {
	seedProbeGrantCaches(t)
	target := model.ScheduledProbeTarget{ChannelID: 2, ModelName: "deepseek-chat"}

	all := ScheduledProbeGrantIDs(target)
	if len(all) != 3 {
		t.Fatalf("expected 未排除时 3 条授权, actual %v", all)
	}

	target.ExcludedKeys = []string{"b"}
	remaining := ScheduledProbeGrantIDs(target)
	expected := []int{40, 42}
	if len(remaining) != len(expected) || remaining[0] != expected[0] || remaining[1] != expected[1] {
		t.Fatalf("expected 排除 b 后剩 %v, actual %v", expected, remaining)
	}
}

// 把一个目标的凭据全部删光是允许的: 调度器遇到空集合会推后而不落失败结论,
// 这里确认函数确实返回空, 而不是回退成"没有排除项"再把它测一遍。
func TestScheduledProbeGrantIDsReturnsEmptyWhenAllExcluded(t *testing.T) {
	seedProbeGrantCaches(t)
	target := model.ScheduledProbeTarget{
		ChannelID:    2,
		ModelName:    "deepseek-chat",
		ExcludedKeys: []string{"a", "b", "c"},
	}

	remaining := ScheduledProbeGrantIDs(target)

	if len(remaining) != 0 {
		t.Fatalf("expected 全部排除后无授权可测, actual %v", remaining)
	}
}

// 排除项是按名称匹配的, 不该牵连同名凭据之外的任何一条。
func TestScheduledProbeGrantIDsOnlyDropsMatchingName(t *testing.T) {
	seedProbeGrantCaches(t)
	target := model.ScheduledProbeTarget{
		ChannelID:    2,
		ModelName:    "deepseek-chat",
		ExcludedKeys: []string{"不存在的凭据名"},
	}

	remaining := ScheduledProbeGrantIDs(target)

	if len(remaining) != 3 {
		t.Fatalf("expected 名称不匹配时 3 条授权都保留, actual %v", remaining)
	}
}
