package op

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/bestruirui/octopus/internal/db"
	"github.com/bestruirui/octopus/internal/model"
	"github.com/charmbracelet/log"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// 分组统计与渠道按日明细分开持锁: 请求终态会同时写全局统计和分组统计,
// 但分组落库失败不该把渠道待写标记一起回滚。
var (
	groupStatsMu      sync.Mutex
	groupStatsCache   = make(map[int]model.StatsMetrics)
	groupStatsDirty   = make(map[int]struct{})
	groupDailyCache   = make(map[dailyKey]model.StatsMetrics)
	groupDailyDirty   = make(map[dailyKey]struct{})
	groupPersistMu    sync.Mutex // 序列化落库与启动恢复, 避免恢复把未落库增量清掉。
)

// StatsGroupUpdate 把一次已定稿请求记到接收它的分组上。
// 只按 GroupID 累加一次: 成员里有几把 Key、上游模型被几个分组引用, 都不在这里复制。
func StatsGroupUpdate(groupID int, metrics model.StatsMetrics) {
	if groupID <= 0 {
		return
	}
	today := time.Now().Format("20060102")
	groupStatsMu.Lock()
	defer groupStatsMu.Unlock()

	total := groupStatsCache[groupID]
	total.Add(metrics)
	groupStatsCache[groupID] = total
	groupStatsDirty[groupID] = struct{}{}

	key := dailyKey{ID: groupID, Date: today}
	daily := groupDailyCache[key]
	daily.Add(metrics)
	groupDailyCache[key] = daily
	groupDailyDirty[key] = struct{}{}
}

// StatsGroupList 返回当前全部分组在窗口内的统计。days<=0 读累计; 正数按日历日窗口。
// 无流量分组也列出, 各项为零。
func StatsGroupList(ctx context.Context, days int) ([]model.StatsGroupRow, error) {
	groups := GroupList()
	totals := make(map[int]model.StatsMetrics, len(groups))
	if days <= 0 {
		groupStatsMu.Lock()
		for id, metrics := range groupStatsCache {
			totals[id] = metrics
		}
		groupStatsMu.Unlock()
	} else {
		since := time.Now().AddDate(0, 0, -(days - 1)).Format("20060102")
		ranged, err := statsGroupDailyRange(ctx, since)
		if err != nil {
			return nil, err
		}
		totals = ranged
	}

	rows := make([]model.StatsGroupRow, 0, len(groups))
	for _, group := range groups {
		rows = append(rows, model.StatsGroupRow{
			GroupID:      group.ID,
			GroupName:    group.Name,
			StatsMetrics: totals[group.ID],
		})
	}
	return rows, nil
}

// statsGroupDailyRange 合并库内与缓存的按日明细。缓存存的是当日累计值, 同一 (分组, 日期) 以缓存为准, 不能再与库内旧行相加。
func statsGroupDailyRange(ctx context.Context, since string) (map[int]model.StatsMetrics, error) {
	cached := snapshotDailyCache(&groupStatsMu, groupDailyCache, since)

	var dbRows []model.StatsGroupDaily
	if err := db.GetDB().WithContext(ctx).Where("date >= ?", since).Find(&dbRows).Error; err != nil {
		return nil, err
	}

	totals := make(map[int]model.StatsMetrics)
	for _, row := range dbRows {
		if _, fresher := cached[dailyKey{ID: row.GroupID, Date: row.Date}]; fresher {
			continue
		}
		total := totals[row.GroupID]
		total.Add(row.StatsMetrics)
		totals[row.GroupID] = total
	}
	for key, metrics := range cached {
		total := totals[key.ID]
		total.Add(metrics)
		totals[key.ID] = total
	}
	return totals, nil
}

// persistGroupStats 把分组累计与按日明细放进同一事务写出。
// 两张表必须一起成功: 只写累计会让周期查询丢当天, 只写按日会让全时段与窗口对不上。
func persistGroupStats(ctx context.Context) error {
	groupPersistMu.Lock()
	defer groupPersistMu.Unlock()

	today := time.Now().Format("20060102")
	groupIDs := drainDirtySet(&groupStatsMu, groupStatsDirty)
	dailyKeys, dailyValues := drainDailyDirty(&groupStatsMu, groupDailyCache, groupDailyDirty, today)

	totals := make([]model.StatsGroup, 0, len(groupIDs))
	groupStatsMu.Lock()
	for _, id := range groupIDs {
		totals = append(totals, model.StatsGroup{GroupID: id, StatsMetrics: groupStatsCache[id]})
	}
	groupStatsMu.Unlock()

	dailies := make([]model.StatsGroupDaily, 0, len(dailyKeys))
	for _, key := range dailyKeys {
		dailies = append(dailies, model.StatsGroupDaily{GroupID: key.ID, Date: key.Date, StatsMetrics: dailyValues[key]})
	}

	if len(totals) == 0 && len(dailies) == 0 {
		return pruneGroupDaily(ctx)
	}

	err := db.GetDB().WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if len(totals) > 0 {
			if err := tx.Clauses(clause.OnConflict{
				Columns:   []clause.Column{{Name: "group_id"}},
				UpdateAll: true,
			}).Create(&totals).Error; err != nil {
				return err
			}
		}
		if len(dailies) > 0 {
			if err := tx.Clauses(clause.OnConflict{
				Columns:   []clause.Column{{Name: "group_id"}, {Name: "date"}},
				UpdateAll: true,
			}).Create(&dailies).Error; err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		restoreDirtySet(&groupStatsMu, groupStatsDirty, groupIDs)
		restoreDailyDirty(&groupStatsMu, groupDailyCache, groupDailyDirty, dailyKeys, dailyValues)
		return err
	}
	return pruneGroupDaily(ctx)
}

func pruneGroupDaily(ctx context.Context) error {
	expired := time.Now().AddDate(0, 0, -dailyStatsRetentionDays).Format("20060102")
	if err := db.GetDB().WithContext(ctx).Where("date < ?", expired).Delete(&model.StatsGroupDaily{}).Error; err != nil {
		log.Warnf("failed to prune group daily stats: %v", err)
	}
	return nil
}

func statsGroupRefreshCache(ctx context.Context) error {
	groupPersistMu.Lock()
	defer groupPersistMu.Unlock()

	dbConn := db.GetDB().WithContext(ctx)
	today := time.Now().Format("20060102")

	var totals []model.StatsGroup
	if err := dbConn.Find(&totals).Error; err != nil {
		return fmt.Errorf("failed to get group stats: %v", err)
	}
	var dailies []model.StatsGroupDaily
	if err := dbConn.Where("date = ?", today).Find(&dailies).Error; err != nil {
		return fmt.Errorf("failed to get group daily stats: %v", err)
	}

	groupStatsMu.Lock()
	clear(groupStatsCache)
	clear(groupStatsDirty)
	clear(groupDailyCache)
	clear(groupDailyDirty)
	for _, row := range totals {
		groupStatsCache[row.GroupID] = row.StatsMetrics
	}
	for _, row := range dailies {
		groupDailyCache[dailyKey{ID: row.GroupID, Date: row.Date}] = row.StatsMetrics
	}
	groupStatsMu.Unlock()
	return nil
}
