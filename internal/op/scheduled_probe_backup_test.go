package op

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/bestruirui/octopus/internal/db"
	"github.com/bestruirui/octopus/internal/model"
)

// 定时测活任务随备份导出、导入: 任务与目标分两张表走, 重复导入不翻倍,
// 嵌在任务里的目标以独立数组为准(不清掉 GORM 会连带再建一次)。
func TestScheduledProbeBackupRoundTrip(t *testing.T) {
	ctx := context.Background()
	if err := db.InitDB("sqlite", filepath.Join(t.TempDir(), "probe_backup.db"), false); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close(); scheduledProbeCache.Clear() })

	probe := model.ScheduledProbe{
		Name: "probe-1", IntervalMinutes: 10, Enabled: true,
		Weekdays: model.WeekdayMonday | model.WeekdayFriday, StartHour: 9, EndHour: 18,
		CreditOrder: []string{"1\x00m\x00k"},
	}
	if err := db.GetDB().Create(&probe).Error; err != nil {
		t.Fatal(err)
	}
	target := model.ScheduledProbeTarget{
		ProbeID: probe.ID, ChannelID: 7, ModelName: "gpt", ExcludedKeys: []string{"k2"},
	}
	if err := db.GetDB().Create(&target).Error; err != nil {
		t.Fatal(err)
	}

	dump, err := DBExportAll(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(dump.ScheduledProbes) != 1 || len(dump.ScheduledProbeTargets) != 1 {
		t.Fatalf("probe missing from dump: %+v", dump.ScheduledProbes)
	}
	if got := dump.ScheduledProbes[0]; got.Name != "probe-1" || got.IntervalMinutes != 10 ||
		got.Weekdays != model.WeekdayMonday|model.WeekdayFriday || len(got.CreditOrder) != 1 {
		t.Fatalf("probe mangled by export: %+v", got)
	}

	count := func(table string) int64 {
		var n int64
		if err := db.GetDB().Table(table).Count(&n).Error; err != nil {
			t.Fatal(err)
		}
		return n
	}
	wipe := func() {
		db.GetDB().Exec("DELETE FROM scheduled_probe_targets")
		db.GetDB().Exec("DELETE FROM scheduled_probes")
	}
	wipe()

	// 正常导入: 任务与目标各回来一行。
	if _, err := DBImportIncremental(ctx, dump); err != nil {
		t.Fatal(err)
	}
	if n := count("scheduled_probes"); n != 1 {
		t.Fatalf("probes=%d, want 1", n)
	}
	if n := count("scheduled_probe_targets"); n != 1 {
		t.Fatalf("targets=%d, want 1", n)
	}

	// 重复导入: 主键冲突跳过, 不翻倍。
	if _, err := DBImportIncremental(ctx, dump); err != nil {
		t.Fatal(err)
	}
	if n := count("scheduled_probes"); n != 1 {
		t.Fatalf("probes=%d after reimport, want 1", n)
	}
	if n := count("scheduled_probe_targets"); n != 1 {
		t.Fatalf("targets=%d after reimport, want 1", n)
	}

	// 嵌在任务里的目标不能再建一次: 导入以独立数组为准。
	wipe()
	nested := *dump
	nested.ScheduledProbes = []model.ScheduledProbe{dump.ScheduledProbes[0]}
	nested.ScheduledProbes[0].Targets = []model.ScheduledProbeTarget{{
		ChannelID: 7, ModelName: "gpt-nested",
	}}
	if _, err := DBImportIncremental(ctx, &nested); err != nil {
		t.Fatal(err)
	}
	if n := count("scheduled_probe_targets"); n != 1 {
		t.Fatalf("targets=%d with nested targets, want 1", n)
	}
	var names []string
	if err := db.GetDB().Table("scheduled_probe_targets").Pluck("model_name", &names).Error; err != nil {
		t.Fatal(err)
	}
	if len(names) != 1 || names[0] != "gpt" {
		t.Fatalf("nested target leaked into import: %v", names)
	}

	// 旧备份没有这两个字段也能导入。
	if _, err := DBImportIncremental(ctx, &model.DBDump{Version: dbDumpVersion}); err != nil {
		t.Fatal(err)
	}
}