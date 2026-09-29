package relay

import (
	"testing"
	"time"

	"github.com/bestruirui/octopus/internal/model"
)

// TestScheduledProbeResultRecordedByGrant 验证结论按凭据记一份: 任务测的 (渠道, 模型) 可能没被任何分组引用,
// 那份结论只落进分组就等于没记, 界面上看不到这条任务测出了什么。
func TestScheduledProbeResultRecordedByGrant(t *testing.T) {
	resetScheduledProbeScheduler()
	scheduledProbeResultMu.Lock()
	scheduledProbeResults = make(map[int]ProbeResult)
	scheduledProbeResultMu.Unlock()

	now := time.Now().UnixMilli()
	recordScheduledProbeResult(11, ProbeResult{ItemID: 11, OK: true, LatencyMS: 42, ProbedAt: now})
	recordScheduledProbeResult(12, ProbeResult{ItemID: 12, OK: false, Message: "upstream down", ProbedAt: now})

	results := ScheduledProbeResults([]int{11, 12, 13})
	if len(results) != 2 {
		t.Fatalf("expected 2 results, actual %d: %+v", len(results), results)
	}
	if result := results[11]; !result.OK || result.LatencyMS != 42 {
		t.Fatalf("expected grant 11 to be ok with 42ms, actual %+v", result)
	}
	if result := results[12]; result.OK || result.Message != "upstream down" {
		t.Fatalf("expected grant 12 to be failed, actual %+v", result)
	}
	if _, ok := results[13]; ok {
		t.Fatal("expected no result for grant 13")
	}
}

// TestScheduledProbeResultSurvivesUntilNextProbe 结论不按时效消失, 只被下一次结论覆盖。
//
// 这条契约改过一次: 原先跟分组侧一样只有 10 分钟有效期, 但定时测活是"一拍只测一条凭据",
// 6 条凭据的任务配 10 分钟间隔, 每条要等 60 分钟才轮到一次 —— 徽标有 50 分钟是空白,
// 看起来像监控根本没在跑。界面照实显示结论产生的时刻, 该不该采信由看的人判断。
func TestScheduledProbeResultSurvivesUntilNextProbe(t *testing.T) {
	scheduledProbeResultMu.Lock()
	scheduledProbeResults = make(map[int]ProbeResult)
	scheduledProbeResultMu.Unlock()

	stale := time.Now().Add(-24 * time.Hour).UnixMilli()
	recordScheduledProbeResult(21, ProbeResult{ItemID: 21, OK: true, ProbedAt: stale})

	results := ScheduledProbeResults([]int{21})
	if len(results) != 1 || results[21].ProbedAt != stale {
		t.Fatalf("expected 一天前的结论照旧可见, actual %+v", results)
	}
}

// TestScheduledProbeResultOverwrittenByNext 下一次结论要顶掉上一次, 包括由通变不通。
// 只留旧结论同样不行: 监控的意义就在于把"现在不通了"及时摆在脸上。
func TestScheduledProbeResultOverwrittenByNext(t *testing.T) {
	scheduledProbeResultMu.Lock()
	scheduledProbeResults = make(map[int]ProbeResult)
	scheduledProbeResultMu.Unlock()

	recordScheduledProbeResult(31, ProbeResult{ItemID: 31, OK: true, LatencyMS: 12, ProbedAt: time.Now().UnixMilli()})
	recordScheduledProbeResult(31, ProbeResult{ItemID: 31, OK: false, Message: "upstream down", ProbedAt: time.Now().UnixMilli()})

	results := ScheduledProbeResults([]int{31})
	if len(results) != 1 {
		t.Fatalf("expected 一条凭据只留一份结论, actual %+v", results)
	}
	if results[31].OK || results[31].Message != "upstream down" {
		t.Fatalf("expected 后一次结论覆盖前一次, actual %+v", results[31])
	}
}

// TestForgetScheduledProbeResults 任务被删掉时要主动丢掉这些结论。
// 结论不设有效期之后, 没人再测的凭据会永远挂着最后一次的绿或红,
// 而"这条通道还通不通"已经没有人负责回答了 —— 留着比空着更容易误导。
func TestForgetScheduledProbeResults(t *testing.T) {
	scheduledProbeResultMu.Lock()
	scheduledProbeResults = make(map[int]ProbeResult)
	scheduledProbeResultMu.Unlock()

	now := time.Now().UnixMilli()
	recordScheduledProbeResult(51, ProbeResult{ItemID: 51, OK: true, ProbedAt: now})
	recordScheduledProbeResult(52, ProbeResult{ItemID: 52, OK: true, ProbedAt: now})

	ForgetScheduledProbeResults([]int{51})

	if results := ScheduledProbeResults([]int{51}); len(results) != 0 {
		t.Fatalf("expected 被遗忘的凭据不再有结论, actual %+v", results)
	}
	if results := ScheduledProbeResults([]int{52}); len(results) != 1 {
		t.Fatalf("expected 别的凭据不受牵连, actual %+v", results)
	}
}

// TestProbeScheduledNowWithoutGrant 验证没有可测凭据时明确报错而不是静默返回空结果。
// 静默成功会让界面显示"测活完成"却一条结论都没有, 用户根本不知道是渠道没配好。
func TestProbeScheduledNowWithoutGrant(t *testing.T) {
	probe := dueProbeWithTargets(1, model.ScheduledProbeTarget{ID: 1, ProbeID: 1, ChannelID: 9999, ModelName: "no-such-model"})
	if _, err := ProbeScheduledNow(t.Context(), probe); err == nil {
		t.Fatal("expected an error when the channel model has no available credential")
	}
}

// TestProbeScheduledNowWithoutTargets 验证一条连目标都没有的任务同样明确报错。
// 目标的空值检查在提交时已拦, 但渠道改动后任务可能落到这个状态, 手动触发不该静默成功。
func TestProbeScheduledNowWithoutTargets(t *testing.T) {
	probe := dueProbeWithTargets(1)
	if _, err := ProbeScheduledNow(t.Context(), probe); err == nil {
		t.Fatal("expected an error when the probe has no target")
	}
}

// TestScheduledProbeResultsIgnoreUnknownGrant 验证查询只返回请求范围内的结论, 不泄漏别的凭据的结果。
func TestScheduledProbeResultsIgnoreUnknownGrant(t *testing.T) {
	scheduledProbeResultMu.Lock()
	scheduledProbeResults = make(map[int]ProbeResult)
	scheduledProbeResultMu.Unlock()

	now := time.Now().UnixMilli()
	recordScheduledProbeResult(41, ProbeResult{ItemID: 41, OK: true, ProbedAt: now})
	recordScheduledProbeResult(42, ProbeResult{ItemID: 42, OK: true, ProbedAt: now})

	if results := ScheduledProbeResults([]int{41}); len(results) != 1 {
		t.Fatalf("expected only the requested grant, actual %+v", results)
	}

	// 确认存量不受影响: 查询不该顺手删掉别处的结论。
	if results := ScheduledProbeResults([]int{42}); len(results) != 1 {
		t.Fatalf("expected the untouched grant to still have its result, actual %+v", results)
	}
}
