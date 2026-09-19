package op

import (
	"context"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/bestruirui/octopus/internal/db"
	"github.com/bestruirui/octopus/internal/model"
)

func TestGroupStatsPersistenceAndPeriods(t *testing.T) {
	ctx := context.Background()
	if err := db.InitDB("sqlite", filepath.Join(t.TempDir(), "groups.db"), false); err != nil { t.Fatal(err) }
	t.Cleanup(func() { _ = db.Close(); groupCache.Clear(); groupNameIndex.Clear() })
	groupCache.Clear()
	groupNameIndex.Clear()
	if err := statsGroupRefreshCache(ctx); err != nil { t.Fatal(err) }
	a, err := GroupCreate(&model.GroupCreateRequest{Name: "A"}, ctx)
	if err != nil { t.Fatal(err) }
	b, err := GroupCreate(&model.GroupCreateRequest{Name: "B"}, ctx)
	if err != nil { t.Fatal(err) }
	zero, err := GroupCreate(&model.GroupCreateRequest{Name: "unused"}, ctx)
	if err != nil { t.Fatal(err) }
	first := model.StatsMetrics{InputToken: 100, CachedToken: 25, CacheWriteToken: 10, OutputToken: 20, InputCost: 0.5, OutputCost: 0.25, RequestSuccess: 1}
	StatsGroupUpdate(a.ID, first)
	StatsGroupUpdate(b.ID, model.StatsMetrics{InputToken: 7, RequestFailed: 1})
	check := func(days int, aTokens, bTokens int64) {
		t.Helper()
		rows, err := StatsGroupList(ctx, days)
		if err != nil { t.Fatal(err) }
		if len(rows) != 3 { t.Fatalf("rows=%+v", rows) }
		byID := make(map[int]model.StatsMetrics)
		for _, row := range rows { byID[row.GroupID] = row.StatsMetrics }
		if byID[a.ID].InputToken != aTokens || byID[b.ID].InputToken != bTokens || byID[zero.ID] != (model.StatsMetrics{}) { t.Fatalf("days=%d stats=%+v", days, byID) }
	}
	for _, days := range []int{0, 1, 7, 30} { check(days, 100, 7) }
	if err := persistGroupStats(ctx); err != nil { t.Fatal(err) }
	// 落库缓存中的绝对值不能再与库内副本相加。
	check(1, 100, 7)
	StatsGroupUpdate(a.ID, first)
	check(1, 200, 7)
	for i := 0; i < 2; i++ { if err := persistGroupStats(ctx); err != nil { t.Fatal(err) } }
	if err := statsGroupRefreshCache(ctx); err != nil { t.Fatal(err) }
	check(0, 200, 7)
	check(1, 200, 7)
	// 重启恢复以后继续累加, 不覆盖启动前的当日用量。
	StatsGroupUpdate(a.ID, first)
	if err := persistGroupStats(ctx); err != nil { t.Fatal(err) }
	check(1, 300, 7)

	now := time.Now()
	for _, age := range []int{6, 7, 29, 30, 50} {
		row := model.StatsGroupDaily{GroupID: a.ID, Date: now.AddDate(0, 0, -age).Format("20060102"), StatsMetrics: model.StatsMetrics{InputToken: 10}}
		if err := db.GetDB().Create(&row).Error; err != nil { t.Fatal(err) }
	}
	check(7, 310, 7)
	check(30, 330, 7)
	// 全时段读独立累计, 不依赖保留窗口中的按日明细。
	check(0, 300, 7)
	if err := persistGroupStats(ctx); err != nil { t.Fatal(err) }
	var oldCount int64
	db.GetDB().Model(&model.StatsGroupDaily{}).Where("date < ?", now.AddDate(0, 0, -dailyStatsRetentionDays).Format("20060102")).Count(&oldCount)
	if oldCount != 0 { t.Fatalf("expired rows=%d", oldCount) }

	// 改名和成员移除不会重新归属已发生的请求。
	name := "A-renamed"
	empty := []model.GroupItemInput{}
	if _, err := GroupUpdate(a.ID, &model.GroupUpdateRequest{Name: &name, Items: &empty}, ctx); err != nil { t.Fatal(err) }
	check(0, 300, 7)
	rows, _ := StatsGroupList(ctx, 0)
	for _, row := range rows { if row.GroupID == a.ID && row.GroupName != name { t.Fatalf("stale name: %+v", row) } }

	// 多请求与周期写入同时发生也不丢增量。
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() { defer wg.Done(); for j := 0; j < 25; j++ { StatsGroupUpdate(b.ID, model.StatsMetrics{InputToken: 1, RequestSuccess: 1}) } }()
	}
	for i := 0; i < 3; i++ { if err := persistGroupStats(ctx); err != nil { t.Fatal(err) } }
	wg.Wait()
	if err := persistGroupStats(ctx); err != nil { t.Fatal(err) }
	if err := statsGroupRefreshCache(ctx); err != nil { t.Fatal(err) }
	check(0, 300, 207)

	// 两张表事务内写入: daily 失败后 total 不可单独提交, 下一轮应完整重试。
	StatsGroupUpdate(b.ID, model.StatsMetrics{InputToken: 1})
	if err := db.GetDB().Migrator().DropTable(&model.StatsGroupDaily{}); err != nil { t.Fatal(err) }
	if err := persistGroupStats(ctx); err == nil { t.Fatal("expected persistence failure") }
	var saved model.StatsGroup
	if err := db.GetDB().First(&saved, "group_id = ?", b.ID).Error; err != nil { t.Fatal(err) }
	if saved.InputToken != 207 { t.Fatalf("partial commit: %+v", saved) }
	if err := db.GetDB().AutoMigrate(&model.StatsGroupDaily{}); err != nil { t.Fatal(err) }
	if err := persistGroupStats(ctx); err != nil { t.Fatal(err) }
	if err := statsGroupRefreshCache(ctx); err != nil { t.Fatal(err) }
	check(0, 300, 208)

	// 新字段往返备份, 重复导入不重复计数; 旧备份缺失字段也能导入。
	dump, err := DBExportAll(ctx)
	if err != nil { t.Fatal(err) }
	if len(dump.StatsGroup) != 2 { t.Fatalf("missing group totals in dump: %+v", dump.StatsGroup) }
	for i := 0; i < 2; i++ { if _, err := DBImportIncremental(ctx, dump); err != nil { t.Fatal(err) } }
	if err := statsGroupRefreshCache(ctx); err != nil { t.Fatal(err) }
	check(0, 300, 208)
	if _, err := DBImportIncremental(ctx, &model.DBDump{Version: dbDumpVersion}); err != nil { t.Fatal(err) }
}
