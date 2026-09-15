package op

import (
	"testing"
	"time"

	"github.com/bestruirui/octopus/internal/model"
)

// 跨日翻转把旧一天放进待写表而非就地落库: 调用方(请求定稿)可能正持有其他锁,
// 同步写库会让慢数据库把整条转发链路堵住; 放进表里则由周期落库统一写出并负责失败重试。
func TestStatsDailyUpdateRolloverParksPreviousDay(t *testing.T) {
	statsDailyCacheLock.Lock()
	prev := model.StatsDaily{Date: "20000101"}
	prev.InputToken = 5
	statsDailyCache = prev
	clear(statsDailyPending)
	statsDailyCacheLock.Unlock()
	defer func() {
		statsDailyCacheLock.Lock()
		clear(statsDailyPending)
		statsDailyCache = model.StatsDaily{}
		statsDailyCacheLock.Unlock()
	}()

	metrics := model.StatsMetrics{InputToken: 7}
	if err := StatsDailyUpdate(metrics); err != nil {
		t.Fatalf("StatsDailyUpdate: %v", err)
	}

	today := time.Now().Format("20060102")
	statsDailyCacheLock.RLock()
	defer statsDailyCacheLock.RUnlock()
	if statsDailyCache.Date != today {
		t.Fatalf("cache date = %s, want %s", statsDailyCache.Date, today)
	}
	if statsDailyCache.InputToken != 7 {
		t.Fatalf("today input = %d, want 7", statsDailyCache.InputToken)
	}
	parked, ok := statsDailyPending["20000101"]
	if !ok || parked.InputToken != 5 {
		t.Fatalf("pending = %+v, want 20000101 with input 5", statsDailyPending)
	}
}

// drain 交接全部待写行并清空; 落库失败时 restore 原样放回, 下个周期重试, 一行都不丢。
func TestDrainRestoreStatsDailyPending(t *testing.T) {
	statsDailyCacheLock.Lock()
	clear(statsDailyPending)
	newer := model.StatsDaily{Date: "20000102"}
	newer.InputToken = 2
	older := model.StatsDaily{Date: "20000101"}
	older.InputToken = 1
	statsDailyPending[newer.Date] = newer
	statsDailyPending[older.Date] = older
	statsDailyCacheLock.Unlock()
	defer func() {
		statsDailyCacheLock.Lock()
		clear(statsDailyPending)
		statsDailyCacheLock.Unlock()
	}()

	rows := drainStatsDailyPending()
	if len(rows) != 2 {
		t.Fatalf("drained %d rows, want 2", len(rows))
	}
	if rows[0].Date != "20000101" || rows[1].Date != "20000102" {
		t.Fatalf("rows not ordered by date: %s, %s", rows[0].Date, rows[1].Date)
	}
	statsDailyCacheLock.RLock()
	pendingLen := len(statsDailyPending)
	statsDailyCacheLock.RUnlock()
	if pendingLen != 0 {
		t.Fatalf("pending not drained: %d entries left", pendingLen)
	}

	restoreStatsDailyPending(rows[:1])
	statsDailyCacheLock.RLock()
	restored, ok := statsDailyPending["20000101"]
	_, alsoRestored := statsDailyPending["20000102"]
	statsDailyCacheLock.RUnlock()
	if !ok || restored.InputToken != 1 || alsoRestored {
		t.Fatalf("restore mismatch: ok=%v input=%d alsoRestored=%v", ok, restored.InputToken, alsoRestored)
	}
}
