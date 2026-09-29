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

// TestScheduledProbeResultExpiry 验证过期结论读出来就等于不存在, 与分组侧 freshProbes 同一口径。
// 结论有效期只有 10 分钟, 拿几小时前的快照当现状看比没有结论更糟。
func TestScheduledProbeResultExpiry(t *testing.T) {
	scheduledProbeResultMu.Lock()
	scheduledProbeResults = make(map[int]ProbeResult)
	scheduledProbeResultMu.Unlock()

	expired := time.Now().Add(-2 * probeResultTTL).UnixMilli()
	recordScheduledProbeResult(21, ProbeResult{ItemID: 21, OK: true, ProbedAt: expired})

	if results := ScheduledProbeResults([]int{21}); len(results) != 0 {
		t.Fatalf("expected expired result to be hidden, actual %+v", results)
	}
}

// TestScheduledProbeResultPrunesExpired 验证写入路径顺手清理旧结论, 使这张表收敛在有效期内的条数。
func TestScheduledProbeResultPrunesExpired(t *testing.T) {
	scheduledProbeResultMu.Lock()
	scheduledProbeResults = make(map[int]ProbeResult)
	scheduledProbeResultMu.Unlock()

	expired := time.Now().Add(-2 * probeResultTTL).UnixMilli()
	recordScheduledProbeResult(31, ProbeResult{ItemID: 31, OK: true, ProbedAt: expired})
	recordScheduledProbeResult(32, ProbeResult{ItemID: 32, OK: true, ProbedAt: time.Now().UnixMilli()})

	scheduledProbeResultMu.Lock()
	defer scheduledProbeResultMu.Unlock()
	if _, ok := scheduledProbeResults[31]; ok {
		t.Fatal("expected the expired entry to be pruned on write")
	}
	if _, ok := scheduledProbeResults[32]; !ok {
		t.Fatal("expected the fresh entry to survive")
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
