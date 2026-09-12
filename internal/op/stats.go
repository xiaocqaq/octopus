package op

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/bestruirui/octopus/internal/db"
	"github.com/bestruirui/octopus/internal/model"
	"github.com/bestruirui/octopus/internal/utils/cache"
	"github.com/charmbracelet/log"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

var statsDailyCache model.StatsDaily
var statsDailyCacheLock sync.RWMutex

var statsTotalCache model.StatsTotal
var statsTotalCacheLock sync.RWMutex

var statsHourlyCache [24]model.StatsHourly
var statsHourlyCacheLock sync.RWMutex

var channelStatsNeedUpdate = make(map[int]struct{}) // 等待持久化的渠道 ID。
var channelStatsNeedUpdateLock sync.Mutex           // 保护渠道统计累加和待写集合。

var channelModelStatsNeedUpdate = make(map[int]struct{}) // 等待持久化的渠道模型 ID。
var channelModelStatsNeedUpdateLock sync.Mutex           // 保护渠道模型统计累加和待写集合。

var channelKeyStatsNeedUpdate = make(map[int]struct{}) // 等待持久化的渠道凭据 ID。
var channelKeyStatsNeedUpdateLock sync.Mutex           // 保护渠道凭据统计累加和待写集合。

var statsAPIKeyCache = cache.New[int, model.StatsAPIKey](16)
var statsAPIKeyCacheNeedUpdate = make(map[int]struct{})
var statsAPIKeyCacheNeedUpdateLock sync.Mutex

// dailyStatsRetentionDays 是按日明细的保留天数; 首页榜单最长看 30 天, 余量用于跨时区与补写。
const dailyStatsRetentionDays = 40

// dailyKey 定位一条按日明细: 主体主键加日期。
type dailyKey struct {
	ID   int    // 渠道或渠道模型主键。
	Date string // 统计日期, 格式 20060102。
}

var channelDailyCache = make(map[dailyKey]model.StatsMetrics)      // 尚未落库的渠道按日统计, 值为当日累计值。
var channelDailyNeedUpdate = make(map[dailyKey]struct{})           // 等待持久化的渠道按日明细。
var channelDailyLock sync.Mutex                                    // 保护渠道按日累加与待写集合。
var channelModelDailyCache = make(map[dailyKey]model.StatsMetrics) // 尚未落库的渠道模型按日统计。
var channelModelDailyNeedUpdate = make(map[dailyKey]struct{})      // 等待持久化的渠道模型按日明细。
var channelModelDailyLock sync.Mutex                               // 保护渠道模型按日累加与待写集合。

func StatsSaveDBTask() {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	log.Debugf("stats save db task started")
	startTime := time.Now()
	defer func() {
		log.Debugf("stats save db task finished, save time: %s", time.Since(startTime))
	}()
	if err := StatsSaveDB(ctx); err != nil {
		log.Errorf("stats save db error: %v", err)
		return
	}
}

func StatsSaveDB(ctx context.Context) error {
	statsTotalCacheLock.RLock()
	totalSnap := statsTotalCache
	statsTotalCacheLock.RUnlock()
	if totalSnap.ID == 0 {
		totalSnap.ID = 1
	}

	statsDailyCacheLock.RLock()
	dailySnap := statsDailyCache
	statsDailyCacheLock.RUnlock()

	statsHourlyCacheLock.RLock()
	hourlyAll := statsHourlyCache
	statsHourlyCacheLock.RUnlock()

	channelIDs := drainDirtySet(&channelStatsNeedUpdateLock, channelStatsNeedUpdate)
	modelIDs := drainDirtySet(&channelModelStatsNeedUpdateLock, channelModelStatsNeedUpdate)
	keyIDs := drainDirtySet(&channelKeyStatsNeedUpdateLock, channelKeyStatsNeedUpdate)
	apiKeyIDs := drainDirtySet(&statsAPIKeyCacheNeedUpdateLock, statsAPIKeyCacheNeedUpdate)

	if err := persistStatsSnapshots(ctx, totalSnap, dailySnap, hourlyAll, channelIDs, modelIDs, keyIDs, apiKeyIDs); err != nil {
		restoreStatsDirty(channelIDs, modelIDs, keyIDs, apiKeyIDs)
		return err
	}
	if err := persistDailyDetails(ctx); err != nil {
		return err
	}
	return nil
}

// persistDailyDetails 落库渠道与渠道模型的按日明细并清理过期数据。
// 与累计统计分开落库: 两者互不依赖, 明细失败不该让累计统计的待写标记一起回滚重来。
func persistDailyDetails(ctx context.Context) error {
	dbConn := db.GetDB().WithContext(ctx)
	today := time.Now().Format("20060102")

	channelKeys, channelValues := drainDailyDirty(&channelDailyLock, channelDailyCache, channelDailyNeedUpdate, today)
	rows := make([]model.StatsChannelDaily, 0, len(channelKeys))
	for _, key := range channelKeys {
		rows = append(rows, model.StatsChannelDaily{ChannelID: key.ID, Date: key.Date, StatsMetrics: channelValues[key]})
	}
	if len(rows) > 0 {
		if err := dbConn.Clauses(clause.OnConflict{
			Columns:   []clause.Column{{Name: "channel_id"}, {Name: "date"}},
			UpdateAll: true,
		}).Create(&rows).Error; err != nil {
			restoreDailyDirty(&channelDailyLock, channelDailyCache, channelDailyNeedUpdate, channelKeys, channelValues)
			return err
		}
	}

	modelKeys, modelValues := drainDailyDirty(&channelModelDailyLock, channelModelDailyCache, channelModelDailyNeedUpdate, today)
	modelRows := make([]model.StatsChannelModelDaily, 0, len(modelKeys))
	for _, key := range modelKeys {
		modelRows = append(modelRows, model.StatsChannelModelDaily{ChannelModelID: key.ID, Date: key.Date, StatsMetrics: modelValues[key]})
	}
	if len(modelRows) > 0 {
		if err := dbConn.Clauses(clause.OnConflict{
			Columns:   []clause.Column{{Name: "channel_model_id"}, {Name: "date"}},
			UpdateAll: true,
		}).Create(&modelRows).Error; err != nil {
			restoreDailyDirty(&channelModelDailyLock, channelModelDailyCache, channelModelDailyNeedUpdate, modelKeys, modelValues)
			return err
		}
	}

	// 过期明细在落库后清理; 删除失败只是留下多余的旧行, 不影响本轮写入, 故不作为错误上报。
	expired := time.Now().AddDate(0, 0, -dailyStatsRetentionDays).Format("20060102")
	if err := dbConn.Where("date < ?", expired).Delete(&model.StatsChannelDaily{}).Error; err != nil {
		log.Warnf("failed to prune channel daily stats: %v", err)
	}
	if err := dbConn.Where("date < ?", expired).Delete(&model.StatsChannelModelDaily{}).Error; err != nil {
		log.Warnf("failed to prune channel model daily stats: %v", err)
	}
	return nil
}

// drainDailyDirty 取出待写的按日明细及其当前累计值, 并从缓存中移除已过去日期的条目。
// 当日条目留在缓存里继续累加; 过去日期的条目已经落库, 后续请求只会写到当日, 留着只是占内存。
func drainDailyDirty(lock *sync.Mutex, values map[dailyKey]model.StatsMetrics, dirty map[dailyKey]struct{}, today string) ([]dailyKey, map[dailyKey]model.StatsMetrics) {
	lock.Lock()
	defer lock.Unlock()

	keys := make([]dailyKey, 0, len(dirty))
	snapshot := make(map[dailyKey]model.StatsMetrics, len(dirty))
	for key := range dirty {
		keys = append(keys, key)
		snapshot[key] = values[key]
		delete(dirty, key)
	}
	for key := range values {
		if key.Date != today {
			delete(values, key)
		}
	}
	return keys, snapshot
}

// restoreDailyDirty 在落库失败后重新标记这批明细待写, 并补回已被清理的过期日期取值。
// 当日取值可能已在此期间继续累加, 故只补回缓存中已不存在的条目, 不覆盖更新的值。
func restoreDailyDirty(lock *sync.Mutex, values map[dailyKey]model.StatsMetrics, dirty map[dailyKey]struct{}, keys []dailyKey, snapshot map[dailyKey]model.StatsMetrics) {
	lock.Lock()
	defer lock.Unlock()

	for _, key := range keys {
		dirty[key] = struct{}{}
		if _, exists := values[key]; !exists {
			values[key] = snapshot[key]
		}
	}
}

// drainDirtySet 取出并清空一个待写集合。
func drainDirtySet(lock *sync.Mutex, set map[int]struct{}) []int {
	lock.Lock()
	defer lock.Unlock()
	ids := make([]int, 0, len(set))
	for id := range set {
		ids = append(ids, id)
		delete(set, id)
	}
	return ids
}

// restoreDirtySet 把一批主键放回待写集合, 用于持久化失败后重试。
func restoreDirtySet(lock *sync.Mutex, set map[int]struct{}, ids []int) {
	lock.Lock()
	defer lock.Unlock()
	for _, id := range ids {
		set[id] = struct{}{}
	}
}

// restoreStatsDirty 在统计持久化失败后恢复本批待写标记。
func restoreStatsDirty(channelIDs, modelIDs, keyIDs, apiKeyIDs []int) {
	restoreDirtySet(&channelStatsNeedUpdateLock, channelStatsNeedUpdate, channelIDs)
	restoreDirtySet(&channelModelStatsNeedUpdateLock, channelModelStatsNeedUpdate, modelIDs)
	restoreDirtySet(&channelKeyStatsNeedUpdateLock, channelKeyStatsNeedUpdate, keyIDs)
	restoreDirtySet(&statsAPIKeyCacheNeedUpdateLock, statsAPIKeyCacheNeedUpdate, apiKeyIDs)
}

func persistStatsSnapshots(
	ctx context.Context,
	totalSnap model.StatsTotal,
	dailySnap model.StatsDaily,
	hourlyAll [24]model.StatsHourly,
	channelIDs []int,
	modelIDs []int,
	keyIDs []int,
	apiKeyIDs []int,
) error {
	dbConn := db.GetDB().WithContext(ctx)

	if result := dbConn.Save(&totalSnap); result.Error != nil {
		return result.Error
	}
	if result := dbConn.Save(&dailySnap); result.Error != nil {
		return result.Error
	}

	todayDate := time.Now().Format("20060102")
	hourlyStats := make([]model.StatsHourly, 0, 24)
	for hour := 0; hour < 24; hour++ {
		if hourlyAll[hour].Date == todayDate {
			hourlyStats = append(hourlyStats, hourlyAll[hour])
		}
	}
	if len(hourlyStats) > 0 {
		if result := dbConn.Clauses(clause.OnConflict{
			Columns:   []clause.Column{{Name: "hour"}},
			UpdateAll: true,
		}).Create(&hourlyStats); result.Error != nil {
			return result.Error
		}
	}

	for _, id := range channelIDs {
		channel, ok := channelCache.Get(id)
		if !ok {
			continue
		}
		if result := dbConn.Model(&model.Channel{}).
			Where("id = ?", channel.ID).
			Select("input_token", "output_token", "input_cost", "output_cost", "wait_time", "request_success", "request_failed").
			Updates(&channel); result.Error != nil {
			return result.Error
		}
	}

	for _, id := range modelIDs {
		channelModel, ok := channelModelCache.Get(id)
		if !ok {
			continue
		}
		if result := dbConn.Model(&model.ChannelModel{}).
			Where("id = ?", channelModel.ID).
			Select("input_token", "output_token", "input_cost", "output_cost", "wait_time", "request_success", "request_failed").
			Updates(&channelModel); result.Error != nil {
			return result.Error
		}
	}

	for _, id := range keyIDs {
		channelKey, ok := channelKeyCache.Get(id)
		if !ok {
			continue
		}
		if result := dbConn.Model(&model.ChannelKey{}).
			Where("id = ?", channelKey.ID).
			Select("input_token", "output_token", "input_cost", "output_cost", "wait_time", "request_success", "request_failed").
			Updates(&channelKey); result.Error != nil {
			return result.Error
		}
	}

	for _, id := range apiKeyIDs {
		ak, ok := statsAPIKeyCache.Get(id)
		if !ok {
			continue
		}
		if result := dbConn.Save(&ak); result.Error != nil {
			return result.Error
		}
	}

	return nil
}

func statsSaveDBWithDailyOverride(ctx context.Context, dailyOverride model.StatsDaily) error {
	statsTotalCacheLock.RLock()
	totalSnap := statsTotalCache
	statsTotalCacheLock.RUnlock()
	if totalSnap.ID == 0 {
		totalSnap.ID = 1
	}

	statsHourlyCacheLock.RLock()
	hourlyAll := statsHourlyCache
	statsHourlyCacheLock.RUnlock()

	channelIDs := drainDirtySet(&channelStatsNeedUpdateLock, channelStatsNeedUpdate)
	modelIDs := drainDirtySet(&channelModelStatsNeedUpdateLock, channelModelStatsNeedUpdate)
	keyIDs := drainDirtySet(&channelKeyStatsNeedUpdateLock, channelKeyStatsNeedUpdate)
	apiKeyIDs := drainDirtySet(&statsAPIKeyCacheNeedUpdateLock, statsAPIKeyCacheNeedUpdate)

	if err := persistStatsSnapshots(ctx, totalSnap, dailyOverride, hourlyAll, channelIDs, modelIDs, keyIDs, apiKeyIDs); err != nil {
		restoreStatsDirty(channelIDs, modelIDs, keyIDs, apiKeyIDs)
		return err
	}
	// 跨日时一并落库按日明细: 昨日的条目此后不再累加, 不落库就会在下次 drain 时被当作过期条目丢掉。
	if err := persistDailyDetails(ctx); err != nil {
		return err
	}
	return nil
}

// StatsChannelDailyRange 返回 since 当天及其之后各渠道与渠道模型的按日统计合计, since 为 20060102 格式。
// 两个返回值分别按渠道主键与渠道模型主键索引。
// 缓存与库内数据合并给出: 缓存里存的是该天的累计值而非增量, 同一 (主体, 日期) 在两侧都有时以缓存为准,
// 库内那一行是它上次落库的旧版本, 两者相加会把已计入的部分算两遍。
func StatsChannelDailyRange(ctx context.Context, since string) (map[int]model.StatsMetrics, map[int]model.StatsMetrics, error) {
	dbConn := db.GetDB().WithContext(ctx)

	channelCached := snapshotDailyCache(&channelDailyLock, channelDailyCache, since)
	modelCached := snapshotDailyCache(&channelModelDailyLock, channelModelDailyCache, since)

	var channelRows []model.StatsChannelDaily
	if err := dbConn.Where("date >= ?", since).Find(&channelRows).Error; err != nil {
		return nil, nil, err
	}
	var modelRows []model.StatsChannelModelDaily
	if err := dbConn.Where("date >= ?", since).Find(&modelRows).Error; err != nil {
		return nil, nil, err
	}

	channelTotals := make(map[int]model.StatsMetrics, len(channelRows))
	for _, row := range channelRows {
		if _, fresher := channelCached[dailyKey{ID: row.ChannelID, Date: row.Date}]; fresher {
			continue
		}
		total := channelTotals[row.ChannelID]
		total.Add(row.StatsMetrics)
		channelTotals[row.ChannelID] = total
	}
	for key, cached := range channelCached {
		total := channelTotals[key.ID]
		total.Add(cached)
		channelTotals[key.ID] = total
	}

	modelTotals := make(map[int]model.StatsMetrics, len(modelRows))
	for _, row := range modelRows {
		if _, fresher := modelCached[dailyKey{ID: row.ChannelModelID, Date: row.Date}]; fresher {
			continue
		}
		total := modelTotals[row.ChannelModelID]
		total.Add(row.StatsMetrics)
		modelTotals[row.ChannelModelID] = total
	}
	for key, cached := range modelCached {
		total := modelTotals[key.ID]
		total.Add(cached)
		modelTotals[key.ID] = total
	}

	return channelTotals, modelTotals, nil
}

// snapshotDailyCache 复制缓存中 since 当天及其之后的按日取值, 供与库内数据合并。
// 复制而非持锁遍历: 合并期间要查库, 持着累加锁会让转发侧的统计写入一起等待。
func snapshotDailyCache(lock *sync.Mutex, values map[dailyKey]model.StatsMetrics, since string) map[dailyKey]model.StatsMetrics {
	lock.Lock()
	defer lock.Unlock()

	snapshot := make(map[dailyKey]model.StatsMetrics, len(values))
	for key, metrics := range values {
		if key.Date >= since {
			snapshot[key] = metrics
		}
	}
	return snapshot
}

func StatsDailyUpdate(ctx context.Context, metrics model.StatsMetrics) error {
	today := time.Now().Format("20060102")

	statsDailyCacheLock.Lock()
	if statsDailyCache.Date == today {
		statsDailyCache.StatsMetrics.Add(metrics)
		statsDailyCacheLock.Unlock()
		return nil
	}

	prevDaily := statsDailyCache
	statsDailyCache = model.StatsDaily{Date: today}
	statsDailyCache.StatsMetrics.Add(metrics)
	statsDailyCacheLock.Unlock()

	return statsSaveDBWithDailyOverride(ctx, prevDaily)
}

func StatsTotalUpdate(metrics model.StatsMetrics) error {
	statsTotalCacheLock.Lock()
	defer statsTotalCacheLock.Unlock()
	if statsTotalCache.ID == 0 {
		statsTotalCache.ID = 1
	}
	statsTotalCache.StatsMetrics.Add(metrics)
	return nil
}

func StatsHourlyUpdate(metrics model.StatsMetrics) error {
	now := time.Now()
	nowHour := now.Hour()
	todayDate := time.Now().Format("20060102")

	statsHourlyCacheLock.Lock()
	defer statsHourlyCacheLock.Unlock()

	if statsHourlyCache[nowHour].Date != todayDate {
		statsHourlyCache[nowHour] = model.StatsHourly{
			Hour: nowHour,
			Date: todayDate,
		}
	}

	statsHourlyCache[nowHour].StatsMetrics.Add(metrics)
	return nil
}

// ChannelModelStatsUpdate 累加渠道模型统计并标记对应模型待持久化, 同时累加当日明细。
func ChannelModelStatsUpdate(channelModelID int, metrics model.StatsMetrics) error {
	channelModelStatsNeedUpdateLock.Lock()
	channelModel, ok := channelModelCache.Get(channelModelID)
	if !ok {
		channelModelStatsNeedUpdateLock.Unlock()
		return nil
	}
	channelModel.StatsMetrics.Add(metrics)
	channelModelCache.Set(channelModelID, channelModel)
	channelModelStatsNeedUpdate[channelModelID] = struct{}{}
	channelModelStatsNeedUpdateLock.Unlock()

	addDailyMetrics(&channelModelDailyLock, channelModelDailyCache, channelModelDailyNeedUpdate, channelModelID, metrics)
	return nil
}

// addDailyMetrics 把一次统计增量累加到指定主体的当日明细上并标记待持久化。
// 缓存里存的是当日累计值而非增量: 落库走整行覆盖, 由此中途失败重跑也不会重复计数。
func addDailyMetrics(lock *sync.Mutex, values map[dailyKey]model.StatsMetrics, dirty map[dailyKey]struct{}, id int, metrics model.StatsMetrics) {
	key := dailyKey{ID: id, Date: time.Now().Format("20060102")}

	lock.Lock()
	defer lock.Unlock()

	current := values[key]
	current.Add(metrics)
	values[key] = current
	dirty[key] = struct{}{}
}

// ChannelKeyStatsUpdate 累加渠道凭据统计并标记对应凭据待持久化。
func ChannelKeyStatsUpdate(channelKeyID int, metrics model.StatsMetrics) error {
	channelKeyStatsNeedUpdateLock.Lock()
	defer channelKeyStatsNeedUpdateLock.Unlock()
	channelKey, ok := channelKeyCache.Get(channelKeyID)
	if !ok {
		return nil
	}
	channelKey.StatsMetrics.Add(metrics)
	channelKeyCache.Set(channelKeyID, channelKey)
	channelKeyStatsNeedUpdate[channelKeyID] = struct{}{}
	return nil
}

// ChannelStatsUpdate 累加渠道统计并标记对应渠道待持久化, 同时累加当日明细。
func ChannelStatsUpdate(channelID int, metrics model.StatsMetrics) error {
	channelStatsNeedUpdateLock.Lock()
	channel, ok := channelCache.Get(channelID)
	if !ok {
		channelStatsNeedUpdateLock.Unlock()
		return nil
	}
	channel.StatsMetrics.Add(metrics)
	channelCache.Set(channelID, channel)
	channelStatsNeedUpdate[channelID] = struct{}{}
	channelStatsNeedUpdateLock.Unlock()

	addDailyMetrics(&channelDailyLock, channelDailyCache, channelDailyNeedUpdate, channelID, metrics)
	return nil
}

func StatsAPIKeyUpdate(apiKeyID int, metrics model.StatsMetrics) error {
	statsAPIKeyCacheNeedUpdateLock.Lock()
	defer statsAPIKeyCacheNeedUpdateLock.Unlock()
	apiKeyCache, ok := statsAPIKeyCache.Get(apiKeyID)
	if !ok {
		apiKeyCache = model.StatsAPIKey{
			APIKeyID: apiKeyID,
		}
	}
	apiKeyCache.StatsMetrics.Add(metrics)
	statsAPIKeyCache.Set(apiKeyID, apiKeyCache)
	statsAPIKeyCacheNeedUpdate[apiKeyID] = struct{}{}
	return nil
}

func StatsAPIKeyDel(id int) error {
	statsAPIKeyCacheNeedUpdateLock.Lock()
	if _, ok := statsAPIKeyCache.Get(id); !ok {
		statsAPIKeyCacheNeedUpdateLock.Unlock()
		return nil
	}
	statsAPIKeyCache.Del(id)
	delete(statsAPIKeyCacheNeedUpdate, id)
	statsAPIKeyCacheNeedUpdateLock.Unlock()
	return db.GetDB().Delete(&model.StatsAPIKey{}, id).Error
}

func StatsTotalGet() model.StatsTotal {
	statsTotalCacheLock.RLock()
	defer statsTotalCacheLock.RUnlock()
	return statsTotalCache
}

func StatsAPIKeyGet(id int) model.StatsAPIKey {
	if stats, ok := statsAPIKeyCache.Get(id); ok {
		return stats
	}
	statsAPIKeyCacheNeedUpdateLock.Lock()
	defer statsAPIKeyCacheNeedUpdateLock.Unlock()
	stats, ok := statsAPIKeyCache.Get(id)
	if !ok {
		tmp := model.StatsAPIKey{
			APIKeyID: id,
		}
		statsAPIKeyCache.Set(id, tmp)
		statsAPIKeyCacheNeedUpdate[id] = struct{}{}
		return tmp
	}
	return stats
}

func StatsAPIKeyList() []model.StatsAPIKey {
	apiKeys := make([]model.StatsAPIKey, 0, statsAPIKeyCache.Len())
	for _, v := range statsAPIKeyCache.GetAll() {
		apiKeys = append(apiKeys, v)
	}
	return apiKeys
}

func StatsHourlyGet() []model.StatsHourly {
	now := time.Now()
	currentHour := now.Hour()
	todayDate := time.Now().Format("20060102")

	statsHourlyCacheLock.RLock()
	defer statsHourlyCacheLock.RUnlock()

	result := make([]model.StatsHourly, 0, currentHour+1)

	for hour := 0; hour <= currentHour; hour++ {
		if statsHourlyCache[hour].Date == todayDate {
			result = append(result, statsHourlyCache[hour])
		} else {
			result = append(result, model.StatsHourly{
				Hour: hour,
				Date: todayDate,
			})
		}
	}

	return result
}

// StatsGetDaily 返回 since 当天及其之后的每日统计, since 为 20060102 格式。
// 只取窗口内的数据: 界面上的热力图与趋势图都有固定跨度, 全量返回会随运行时长无界增长。
func StatsGetDaily(ctx context.Context, since string) ([]model.StatsDaily, error) {
	var statsDaily []model.StatsDaily
	result := db.GetDB().WithContext(ctx).Where("date >= ?", since).Order("date").Find(&statsDaily)
	if result.Error != nil {
		return nil, result.Error
	}
	return statsDaily, nil
}

func statsRefreshCache(ctx context.Context) error {
	dbConn := db.GetDB().WithContext(ctx)
	today := time.Now().Format("20060102")

	var loadedDaily model.StatsDaily
	result := dbConn.Last(&loadedDaily)
	if result.Error != nil && !errors.Is(result.Error, gorm.ErrRecordNotFound) {
		return fmt.Errorf("failed to get daily stats: %v", result.Error)
	}
	if result.RowsAffected == 0 || loadedDaily.Date != today {
		loadedDaily = model.StatsDaily{Date: today}
	}

	var loadedTotal model.StatsTotal
	result = dbConn.First(&loadedTotal)
	if result.Error != nil && !errors.Is(result.Error, gorm.ErrRecordNotFound) {
		return fmt.Errorf("failed to get total stats: %v", result.Error)
	}
	if result.RowsAffected == 0 {
		loadedTotal = model.StatsTotal{ID: 1}
	} else if loadedTotal.ID == 0 {
		loadedTotal.ID = 1
	}

	var loadedHourly []model.StatsHourly
	result = dbConn.Find(&loadedHourly)
	if result.Error != nil {
		return fmt.Errorf("failed to get hourly stats: %v", result.Error)
	}

	statsDailyCacheLock.Lock()
	statsDailyCache = loadedDaily
	statsDailyCacheLock.Unlock()

	statsTotalCacheLock.Lock()
	statsTotalCache = loadedTotal
	statsTotalCacheLock.Unlock()

	var loadedAPIKeys []model.StatsAPIKey
	result = dbConn.Find(&loadedAPIKeys)
	if result.Error != nil {
		return fmt.Errorf("failed to get api key stats: %v", result.Error)
	}

	statsAPIKeyCache.Clear()
	// 就地清空而非重新赋值: drainDirtySet 与 restoreDirtySet 持有该 map 的引用, 换 map 会让它们写到旧对象上。
	statsAPIKeyCacheNeedUpdateLock.Lock()
	clear(statsAPIKeyCacheNeedUpdate)
	statsAPIKeyCacheNeedUpdateLock.Unlock()
	for _, v := range loadedAPIKeys {
		statsAPIKeyCache.Set(v.APIKeyID, v)
	}

	statsHourlyCacheLock.Lock()
	statsHourlyCache = [24]model.StatsHourly{}
	for _, v := range loadedHourly {
		if v.Hour >= 0 && v.Hour < 24 {
			statsHourlyCache[v.Hour] = v
		}
	}
	statsHourlyCacheLock.Unlock()

	// 只载入当日的按日明细: 累加只会写到当日, 更早的日期不再变动, 查询时直接读库即可。
	// 不载入则本进程的首次落库会以本次启动后的增量整行覆盖掉重启前已累计的当日取值。
	var loadedChannelDaily []model.StatsChannelDaily
	if err := dbConn.Where("date = ?", today).Find(&loadedChannelDaily).Error; err != nil {
		return fmt.Errorf("failed to get channel daily stats: %v", err)
	}
	var loadedChannelModelDaily []model.StatsChannelModelDaily
	if err := dbConn.Where("date = ?", today).Find(&loadedChannelModelDaily).Error; err != nil {
		return fmt.Errorf("failed to get channel model daily stats: %v", err)
	}

	// 就地清空: drain 与 restore 持有这两个 map 的引用, 换 map 会让它们写到旧对象上。
	channelDailyLock.Lock()
	clear(channelDailyCache)
	clear(channelDailyNeedUpdate)
	for _, row := range loadedChannelDaily {
		channelDailyCache[dailyKey{ID: row.ChannelID, Date: row.Date}] = row.StatsMetrics
	}
	channelDailyLock.Unlock()

	channelModelDailyLock.Lock()
	clear(channelModelDailyCache)
	clear(channelModelDailyNeedUpdate)
	for _, row := range loadedChannelModelDaily {
		channelModelDailyCache[dailyKey{ID: row.ChannelModelID, Date: row.Date}] = row.StatsMetrics
	}
	channelModelDailyLock.Unlock()

	return nil
}
