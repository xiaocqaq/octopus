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

// seedSharedKeyCreditCaches 造一个渠道、两个模型、两条共用凭据构成的四条授权。
// 不建库只灌缓存: 这一组用例问的是"顺序怎么排", 走库只会让测试变慢而不多验证任何东西。
func seedSharedKeyCreditCaches(t *testing.T) {
	t.Helper()
	clear := func() {
		channelCache.Clear()
		channelKeyCache.Clear()
		channelModelCache.Clear()
		channelGrantCache.Clear()
	}
	t.Cleanup(clear)
	clear()

	channelCache.Set(2, model.Channel{ID: 2, ChannelConfig: model.ChannelConfig{Name: "Demo", Enabled: true}})
	for index, name := range []string{"k-1", "k-2"} {
		keyID := 30 + index
		channelKeyCache.Set(keyID, model.ChannelKey{
			ID:               keyID,
			ChannelID:        2,
			ChannelKeyConfig: model.ChannelKeyConfig{Name: name, Enabled: true},
		})
	}
	channelModelCache.Set(20, model.ChannelModel{ID: 20, ChannelID: 2, Name: "m-a"})
	channelModelCache.Set(21, model.ChannelModel{ID: 21, ChannelID: 2, Name: "m-b"})
	channelGrantCache.Set(40, model.ChannelGrant{ID: 40, ChannelModelID: 20, ChannelKeyID: 30})
	channelGrantCache.Set(41, model.ChannelGrant{ID: 41, ChannelModelID: 20, ChannelKeyID: 31})
	channelGrantCache.Set(42, model.ChannelGrant{ID: 42, ChannelModelID: 21, ChannelKeyID: 30})
	channelGrantCache.Set(43, model.ChannelGrant{ID: 43, ChannelModelID: 21, ChannelKeyID: 31})
}

// creditGrantIDsOf 把凭据列表压成授权主键序列, 便于用一行断言写出期望顺序。
func creditGrantIDsOf(credits []ScheduledProbeCredit) []int {
	grantIDs := make([]int, 0, len(credits))
	for _, credit := range credits {
		grantIDs = append(grantIDs, credit.GrantID)
	}
	return grantIDs
}

func sameInts(left, right []int) bool {
	if len(left) != len(right) {
		return false
	}
	for index := range left {
		if left[index] != right[index] {
			return false
		}
	}
	return true
}

// twoModelProbe 造一条挂了 m-a、m-b 两个目标的任务。
func twoModelProbe() model.ScheduledProbe {
	return model.ScheduledProbe{Targets: []model.ScheduledProbeTarget{
		{ChannelID: 2, ModelName: "m-a"},
		{ChannelID: 2, ModelName: "m-b"},
	}}
}

// 默认顺序是"先按目标, 目标内按凭据名"。这份顺序同时是界面上的行顺序与轮转顺序,
// 所以它必须确定且可复现 —— 两个入口各排一次, 迟早会分岔成两份不一样的顺序。
func TestScheduledProbeCreditsFlattensTargetsThenKeys(t *testing.T) {
	seedSharedKeyCreditCaches(t)

	actual := creditGrantIDsOf(ScheduledProbeCredits(twoModelProbe()))

	if expected := []int{40, 41, 42, 43}; !sameInts(expected, actual) {
		t.Fatalf("expected %v, actual %v", expected, actual)
	}
}

// 拖到最前的那条要真的排到最前: 它就是下一拍会被测的那条。
func TestScheduledProbeCreditsAppliesUserOrder(t *testing.T) {
	seedSharedKeyCreditCaches(t)
	probe := twoModelProbe()
	probe.CreditOrder = []string{CreditKey(2, "m-b", "k-2")}

	actual := creditGrantIDsOf(ScheduledProbeCredits(probe))

	// 列过的排最前, 其余保持默认顺序跟在后面。
	if expected := []int{43, 40, 41, 42}; !sameInts(expected, actual) {
		t.Fatalf("expected %v, actual %v", expected, actual)
	}
}

// 用户后来新增的凭据不在顺序表里, 应当接在末尾而不是消失或插到最前。
func TestScheduledProbeCreditsAppendsUnlistedAtTheEnd(t *testing.T) {
	seedSharedKeyCreditCaches(t)
	probe := twoModelProbe()
	probe.CreditOrder = []string{CreditKey(2, "m-b", "k-1")}

	actual := creditGrantIDsOf(ScheduledProbeCredits(probe))

	if expected := []int{42, 40, 41, 43}; !sameInts(expected, actual) {
		t.Fatalf("expected %v, actual %v", expected, actual)
	}
}

// 顺序表里的项已经消失(凭据被删、渠道被改)时不该影响出结果 ——
// 那些记录留着不清理是有意为之, 读的时候只认此刻还存在的凭据。
func TestScheduledProbeCreditsIgnoresStaleOrderEntries(t *testing.T) {
	seedSharedKeyCreditCaches(t)
	probe := twoModelProbe()
	probe.CreditOrder = []string{
		CreditKey(2, "已经不存在的模型", "k-9"),
		CreditKey(2, "m-b", "k-2"),
		CreditKey(999, "m-a", "k-1"),
	}

	actual := creditGrantIDsOf(ScheduledProbeCredits(probe))

	if expected := []int{43, 40, 41, 42}; !sameInts(expected, actual) {
		t.Fatalf("expected %v, actual %v", expected, actual)
	}
}

// 被逐行删掉的凭据先按排除项出局, 顺序表里就算列着它也不该把它带回来。
func TestScheduledProbeCreditsDropsExcludedEvenIfListedInOrder(t *testing.T) {
	seedSharedKeyCreditCaches(t)
	probe := model.ScheduledProbe{
		Targets: []model.ScheduledProbeTarget{
			{ChannelID: 2, ModelName: "m-a", ExcludedKeys: []string{"k-1"}},
			{ChannelID: 2, ModelName: "m-b"},
		},
		CreditOrder: []string{CreditKey(2, "m-a", "k-1"), CreditKey(2, "m-b", "k-2")},
	}

	actual := creditGrantIDsOf(ScheduledProbeCredits(probe))

	if expected := []int{43, 41, 42}; !sameInts(expected, actual) {
		t.Fatalf("expected %v, actual %v", expected, actual)
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
		t.Fatalf("expected 名称不匹配时 3 条授权都保留, actual %v", len(remaining))
	}
}

// 三条凭据删到只剩一条时目标必须留着: 早删一步就会把用户还没删的凭据一起丢掉。
func TestTargetFullyExcludedKeepsTargetWhileKeysRemain(t *testing.T) {
	seedProbeGrantCaches(t)
	target := model.ScheduledProbeTarget{
		ChannelID:    2,
		ModelName:    "deepseek-chat",
		ExcludedKeys: []string{"a", "b"},
	}

	if targetFullyExcluded(target) {
		t.Fatalf("expected 还剩 c 未排除时不删目标, actual 判定为已删空")
	}
}

// 三条凭据全删光才算删空 —— 这是界面点掉最后一行后目标该消失的判据。
func TestTargetFullyExcludedWhenEveryKeyExcluded(t *testing.T) {
	seedProbeGrantCaches(t)
	target := model.ScheduledProbeTarget{
		ChannelID:    2,
		ModelName:    "deepseek-chat",
		ExcludedKeys: []string{"a", "b", "c"},
	}

	if !targetFullyExcluded(target) {
		t.Fatalf("expected 三条凭据全排除时判定为已删空, actual 判定为未删空")
	}
}

// 目标下一个凭据都没有时不能判定为删空: 那是渠道配置变了(模型被删、凭据被删),
// 不是用户逐行删的; 此时把目标删掉等于替用户改了配置。
func TestTargetFullyExcludedIgnoresTargetWithoutKeys(t *testing.T) {
	seedProbeGrantCaches(t)
	target := model.ScheduledProbeTarget{ChannelID: 2, ModelName: "已经没有的模型"}

	if targetFullyExcluded(target) {
		t.Fatalf("expected 目标下无凭据时不判定为删空, actual 判定为已删空")
	}
}

// 停用不是删除: 渠道或凭据临时关掉时, 用户并没有表达"不要这个目标"。
func TestTargetFullyExcludedIgnoresDisabledKeys(t *testing.T) {
	seedProbeGrantCaches(t)
	channelKeyCache.Set(30, model.ChannelKey{
		ID:               30,
		ChannelID:        2,
		ChannelKeyConfig: model.ChannelKeyConfig{Name: "a", Enabled: false},
	})
	target := model.ScheduledProbeTarget{
		ChannelID:    2,
		ModelName:    "deepseek-chat",
		ExcludedKeys: []string{"b", "c"},
	}

	if targetFullyExcluded(target) {
		t.Fatalf("expected 仅 a 停用时不算删空, actual 判定为已删空")
	}
}

// 删目标要按渠道+模型精确剔除顺序项, 不能连别的目标一起清掉。
func TestDropCreditOrderOfRemovesOnlyMatchingTarget(t *testing.T) {
	order := []string{
		"2\x00m-a\x00k-1",
		"2\x00m-b\x00k-1",
		"2\x00m-a\x00k-2",
		"3\x00m-a\x00k-1",
	}

	kept := dropCreditOrderOf(order, 2, "m-a")

	expected := []string{"2\x00m-b\x00k-1", "3\x00m-a\x00k-1"}
	if len(kept) != len(expected) {
		t.Fatalf("expected 只剩 %d 项, actual %d 项: %v", len(expected), len(kept), kept)
	}
	for index := range expected {
		if kept[index] != expected[index] {
			t.Fatalf("expected 第 %d 项为 %q, actual %q", index, expected[index], kept[index])
		}
	}
}

// 模型名可能互为前缀, 剔除必须整段匹配: 删 "m-a" 不该顺手带走 "m-ab"。
func TestDropCreditOrderOfDoesNotMatchModelNamePrefix(t *testing.T) {
	order := []string{"2\x00m-a\x00k-1", "2\x00m-ab\x00k-1"}

	kept := dropCreditOrderOf(order, 2, "m-a")

	expected := []string{"2\x00m-ab\x00k-1"}
	if len(kept) != len(expected) || kept[0] != expected[0] {
		t.Fatalf("expected 只剩 %v, actual %v", expected, kept)
	}
}
