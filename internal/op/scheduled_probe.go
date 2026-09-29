package op

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"github.com/bestruirui/octopus/internal/db"
	"github.com/bestruirui/octopus/internal/model"
	"github.com/bestruirui/octopus/internal/utils/cache"
	"gorm.io/gorm"
)

// scheduledProbeCache 是定时测活任务的进程内副本, 按主键索引, 已带 Targets。
// 调度器每一拍都要回答"现在轮到谁测", 走库会把一次纯内存判断变成一次查询;
// 任务数量不多但扫描频繁, 缓存是这里唯一合理的选择。写入侧同步维护, 读到的必然是最新配置。
var scheduledProbeCache = cache.New[int, model.ScheduledProbe](16)

// ScheduledProbeTargets 返回全部任务(含目标), 按主键升序 —— 也就是创建顺序。
// 定序在这里定稿而不交给缓存遍历: 调度器按这个顺序做 FIFO 轮转, 顺序随机轮转就失去意义,
// 界面上"下一条轮到谁"也无从解释。
func ScheduledProbeTargets() []model.ScheduledProbe {
	probes := make([]model.ScheduledProbe, 0, scheduledProbeCache.Len())
	for _, probe := range scheduledProbeCache.GetAll() {
		// 目标是切片, 直接交出去会让调用方拿到缓存的内部数组; 复制一份,
		// 免得某处顺手 append 就把缓存里的顺序改了。
		probe.Targets = append([]model.ScheduledProbeTarget(nil), probe.Targets...)
		probes = append(probes, probe)
	}
	sort.Slice(probes, func(i, j int) bool { return probes[i].ID < probes[j].ID })
	return probes
}

// ScheduledProbeList 返回全部任务及其展示字段, 供列表页直接渲染。
func ScheduledProbeList() []model.ScheduledProbeView {
	probes := ScheduledProbeTargets()
	views := make([]model.ScheduledProbeView, 0, len(probes))
	for _, probe := range probes {
		views = append(views, model.ScheduledProbeView{ScheduledProbe: probe})
	}
	return views
}

// ScheduledProbeGet 按主键取一条任务, 供手动测试与行组装读出它挂的目标。
func ScheduledProbeGet(id int) (model.ScheduledProbe, bool) {
	return scheduledProbeCache.Get(id)
}

// ScheduledProbeGrantIDs 返回该目标下当前可测的授权主键, 按凭据名称定序。
// 只收渠道与凭据都启用的那些: 停用的凭据连转发都进不去, 拿它去测只会得到一条"凭据已停用"的结论,
// 那条结论说明的是配置状态而不是通道可用性, 不该占用一次真实上游调用。
// 定序由凭据名称决定: 缓存遍历顺序随机, 不定序则每一拍轮转到的凭据不可预期, 多凭据的轮转也就没法复现。
//
// 目标里被用户逐行删掉的凭据在这里一并滤掉, 于是轮转不再测它、列表也不再出它 —— 这正是所要的效果:
// 排除生效的唯一开关就是这一处, 调度器与界面不可能看到两份不同的"可测凭据"。
func ScheduledProbeGrantIDs(target model.ScheduledProbeTarget) []int {
	type candidate struct {
		id      int
		keyName string
	}
	excluded := make(map[string]bool, len(target.ExcludedKeys))
	for _, keyName := range target.ExcludedKeys {
		excluded[keyName] = true
	}

	candidates := make([]candidate, 0, channelGrantCache.Len())
	for _, grant := range channelGrantCache.GetAll() {
		channelModel, modelOK := channelModelCache.Get(grant.ChannelModelID)
		channelKey, keyOK := channelKeyCache.Get(grant.ChannelKeyID)
		if !modelOK || !keyOK {
			continue
		}
		if channelModel.ChannelID != target.ChannelID || channelModel.Name != target.ModelName {
			continue
		}
		if excluded[channelKey.Name] {
			continue
		}
		channel, channelOK := channelCache.Get(channelModel.ChannelID)
		if !channelOK || !channel.Enabled || !channelKey.Enabled {
			continue
		}
		candidates = append(candidates, candidate{id: grant.ID, keyName: channelKey.Name})
	}
	sort.Slice(candidates, func(i, j int) bool { return candidates[i].keyName < candidates[j].keyName })
	ids := make([]int, 0, len(candidates))
	for _, item := range candidates {
		ids = append(ids, item.id)
	}
	return ids
}

// GrantKeyName 取授权所用凭据的名称, 用于展示"这条结论是哪条凭据测出来的"。
//
// 直读缓存而不走 ChannelGrantGet: 那个函数会因为凭据被停用而返回错误, 可"凭据被停用"
// 恰恰是用户最需要看清是哪一条凭据的场景 —— 结论是历史记录, 不该因为此刻的配置状态而失去名称。
// 取不到名称时返回空串: 少一个名字比让整个列表报错好得多, 结论本身(通不通, 多久之前测的)仍然有意义。
func GrantKeyName(grantID int) string {
	grant, ok := channelGrantCache.Get(grantID)
	if !ok {
		return ""
	}
	channelKey, ok := channelKeyCache.Get(grant.ChannelKeyID)
	if !ok {
		return ""
	}
	return channelKey.Name
}

// ChannelNameOf 取渠道名称, 供列表展示"这条目标属于哪个渠道"; 渠道已删除时返回空串。
func ChannelNameOf(channelID int) string {
	channel, ok := channelCache.Get(channelID)
	if !ok {
		return ""
	}
	return channel.Name
}

// ScheduledProbeCreate 新建一条定时测活任务, 一律先启用。
// 用户点了创建就是要它跑起来; 再让它在停用状态下躺进列表, 等于白建一条, 还得再点一次开关。
func ScheduledProbeCreate(req model.ScheduledProbeRequest, ctx context.Context) (model.ScheduledProbe, error) {
	if err := validateScheduledProbe(req); err != nil {
		return model.ScheduledProbe{}, err
	}
	probe := model.ScheduledProbe{
		Name:            strings.TrimSpace(req.Name),
		IntervalMinutes: req.IntervalMinutes,
		Enabled:         true,
		Weekdays:        req.Weekdays,
		StartHour:       req.StartHour,
		EndHour:         req.EndHour,
		Targets:         targetRowsOf(req.Targets, nil),
	}
	if err := db.GetDB().WithContext(ctx).Create(&probe).Error; err != nil {
		return model.ScheduledProbe{}, fmt.Errorf("failed to create scheduled probe: %w", err)
	}
	scheduledProbeCache.Set(probe.ID, probe)
	return probe, nil
}

// ScheduledProbeUpdate 整体替换一条任务的配置与目标集合, 启停也走这里。
// 用整体替换而不是逐字段补丁: 一条任务总共就这么几个字段, 界面的编辑与开关都提交完整配置,
// 补丁语义反而要额外定义"没传的字段算不算清空", 多一套规则就多一处可能与界面不一致的地方。
func ScheduledProbeUpdate(id int, req model.ScheduledProbeRequest, ctx context.Context) (model.ScheduledProbe, error) {
	existing, ok := scheduledProbeCache.Get(id)
	if !ok {
		return model.ScheduledProbe{}, fmt.Errorf("scheduled probe not found")
	}
	if err := validateScheduledProbe(req); err != nil {
		return model.ScheduledProbe{}, err
	}

	updated := model.ScheduledProbe{
		ID:              id,
		Name:            strings.TrimSpace(req.Name),
		IntervalMinutes: req.IntervalMinutes,
		Enabled:         req.Enabled,
		Weekdays:        req.Weekdays,
		StartHour:       req.StartHour,
		EndHour:         req.EndHour,
		// Save 会写全字段, 不带上创建时间就会被清成 0; 那是任务的出生记录, 不该被一次编辑抹掉。
		CreatedAt: existing.CreatedAt,
	}

	// 目标整体替换与任务字段写在同一个事务里: 只改了任务却没能落目标(或反过来),
	// 会留下"间隔已生效但目标还是旧的"这种半更新状态, 而调度器读的就是这份缓存。
	err := db.GetDB().WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Save(&updated).Error; err != nil {
			return fmt.Errorf("failed to update scheduled probe: %w", err)
		}
		if err := tx.Where("probe_id = ?", id).Delete(&model.ScheduledProbeTarget{}).Error; err != nil {
			return fmt.Errorf("failed to clear scheduled probe targets: %w", err)
		}
		targets := targetRowsOf(req.Targets, existing.Targets)
		for i := range targets {
			targets[i].ProbeID = id
		}
		if len(targets) > 0 {
			if err := tx.Create(&targets).Error; err != nil {
				return fmt.Errorf("failed to create scheduled probe targets: %w", err)
			}
		}
		updated.Targets = targets
		return nil
	})
	if err != nil {
		return model.ScheduledProbe{}, err
	}

	scheduledProbeCache.Set(id, updated)
	return updated, nil
}

// targetRowsOf 把提交的目标还原成待落库的行, 顺便去掉重复项。
// 去重在这里而不是靠数据库唯一索引: 界面上多选两个渠道的并集很容易点出重复的 (渠道, 模型),
// 而那时用户要的是"这些目标", 不是一条约束报错。
//
// existing 是这条任务原有的目标: 请求里没提排除项的目标沿用它既有的排除集合。
// 更新走的是"先清空再重建", 不沿用的话, 用户每编辑一次任务, 之前逐行删掉的凭据就会全部复活。
func targetRowsOf(requested []model.ScheduledProbeTargetRequest, existing []model.ScheduledProbeTarget) []model.ScheduledProbeTarget {
	kept := make(map[string][]string, len(existing))
	for _, target := range existing {
		kept[targetKey(target.ChannelID, target.ModelName)] = target.ExcludedKeys
	}

	targets := make([]model.ScheduledProbeTarget, 0, len(requested))
	seen := make(map[string]bool, len(requested))
	for _, item := range requested {
		key := targetKey(item.ChannelID, item.ModelName)
		if seen[key] {
			continue
		}
		seen[key] = true
		excluded := item.ExcludedKeys
		// nil 与空切片在这里分道扬镳: nil 是"这次提交不涉及排除项", 沿用既有值;
		// 显式空切片才是"清空排除项", 界面上那个「全部恢复」发的就是它。
		if excluded == nil {
			excluded = kept[key]
		}
		targets = append(targets, model.ScheduledProbeTarget{
			ChannelID:    item.ChannelID,
			ModelName:    item.ModelName,
			ExcludedKeys: excluded,
		})
	}
	return targets
}

// targetKey 生成目标的去重键。
// 用 "渠道ID\x00模型名" 而不是结构体: 模型名可能含各种字符, 拿不可见字符分隔
// 才能保证 (1, "2:3") 与 (12, ":3") 这类组合不会撞成同一个键。
func targetKey(channelID int, modelName string) string {
	return fmt.Sprintf("%d\x00%s", channelID, modelName)
}

// ScheduledProbeCredentialSet 在某个目标下隐藏或恢复一条凭据, 并立即刷新缓存。
//
// 单独一个入口而不是让界面提交整条任务: 界面上那一行只知道自己的渠道、模型与凭据名,
// 整条提交要求它把任务的其余字段也一并回传, 一次行内删除就会变成一次全量覆盖 ——
// 用户此刻只想动一行, 不该顺带承担覆盖别处的风险。
func ScheduledProbeCredentialSet(id, channelID int, modelName, keyName string, excluded bool, ctx context.Context) (model.ScheduledProbe, error) {
	probe, ok := scheduledProbeCache.Get(id)
	if !ok {
		return model.ScheduledProbe{}, fmt.Errorf("scheduled probe not found")
	}

	index := -1
	for i, target := range probe.Targets {
		if target.ChannelID == channelID && target.ModelName == modelName {
			index = i
			break
		}
	}
	if index < 0 {
		return model.ScheduledProbe{}, fmt.Errorf("target not found")
	}

	row := probe.Targets[index]
	row.ExcludedKeys = excludeKey(row.ExcludedKeys, keyName, excluded)
	// Save 整行而不是 Update 单个列: 排除集合是靠 serializer 序列化落库的,
	// 只给列名和一个切片, 序列化不会参与, 驱动也认不出 []string。
	if err := db.GetDB().WithContext(ctx).Save(&row).Error; err != nil {
		return model.ScheduledProbe{}, fmt.Errorf("failed to update scheduled probe credential: %w", err)
	}

	probe.Targets[index] = row
	scheduledProbeCache.Set(id, probe)
	return probe, nil
}

// excludeKey 返回增删了 keyName 之后的排除集合, 不改动传入的切片。
// 先整体剔除再按需追加: 同一个名字被点两次也不会在库里堆成重复项。
func excludeKey(keys []string, keyName string, excluded bool) []string {
	result := make([]string, 0, len(keys)+1)
	for _, name := range keys {
		if name != keyName {
			result = append(result, name)
		}
	}
	if excluded {
		result = append(result, keyName)
	}
	return result
}

// ScheduledProbeDelete 删除一条定时测活任务。
// 只删配置, 不回头清理已落在路由状态里的测活结论: 结论有自己的有效期, 到点自然消失,
// 而"任务被删了"并不意味着"那个成员此刻不通", 顺手把结论抹掉反而是凭空造了一次未测的假象。
func ScheduledProbeDelete(id int, ctx context.Context) error {
	probe := model.ScheduledProbe{ID: id}
	result := db.GetDB().WithContext(ctx).Delete(&probe)
	if result.Error != nil {
		return fmt.Errorf("failed to delete scheduled probe: %w", result.Error)
	}
	// GORM 出错时 RowsAffected 也为 0, 故错误必须先于行数判断, 否则瞬时故障会被误报成不存在。
	if result.RowsAffected == 0 {
		return fmt.Errorf("scheduled probe not found")
	}
	// 目标行由外键级联删除; sqlite 之外未必真开了级联, 故显式再删一次, 免得留下孤儿行。
	if err := db.GetDB().WithContext(ctx).
		Where("probe_id = ?", id).Delete(&model.ScheduledProbeTarget{}).Error; err != nil {
		return fmt.Errorf("failed to delete scheduled probe targets: %w", err)
	}
	scheduledProbeCache.Del(id)
	return nil
}

// validateScheduledProbe 校验任务的名字与每个目标引用的模型确实属于所选渠道。
// 这条校验只在提交时做一次: 之后渠道改配置(换模型集合)不会再回头动已存的目标 ——
// 目标记的是"这个渠道的这个模型", 模型被移除后它自然测不出结果, 界面上的那一行会显示为待测。
// 取值范围(间隔与整点)交给请求绑定上的标签把关, 这里不重复第二套规则。
func validateScheduledProbe(req model.ScheduledProbeRequest) error {
	if strings.TrimSpace(req.Name) == "" {
		return fmt.Errorf("name is required")
	}
	if len(req.Targets) == 0 {
		return fmt.Errorf("at least one target is required")
	}
	for _, target := range req.Targets {
		if _, ok := channelCache.Get(target.ChannelID); !ok {
			return fmt.Errorf("channel not found")
		}
		if !channelHasModel(target.ChannelID, target.ModelName) {
			return fmt.Errorf("model %s is not in this channel", target.ModelName)
		}
	}
	return nil
}

// channelHasModel 判断该模型是否属于该渠道。
func channelHasModel(channelID int, modelName string) bool {
	for _, channelModel := range channelModelCache.GetAll() {
		if channelModel.ChannelID == channelID && channelModel.Name == modelName {
			return true
		}
	}
	return false
}

// scheduledProbeRefreshCache 从数据库刷新定时测活任务缓存, 含各自的目标。
func scheduledProbeRefreshCache(ctx context.Context) error {
	probes := []model.ScheduledProbe{}
	// 连目标一起预载: 调度器与列表都要按目标逐条出行, 少了这一层预载就得在循环里逐条回查。
	if err := db.GetDB().WithContext(ctx).Preload("Targets").Find(&probes).Error; err != nil {
		return err
	}
	// 先清再灌: 导入数据后库里已不存在的任务不能留在缓存里被调度器接着测。
	scheduledProbeCache.Clear()
	for _, probe := range probes {
		sortTargets(probe.Targets)
		scheduledProbeCache.Set(probe.ID, probe)
	}
	return nil
}

// sortTargets 按主键给目标定序, 让轮转顺序等于界面上的行顺序, 也等于创建顺序。
func sortTargets(targets []model.ScheduledProbeTarget) {
	sort.Slice(targets, func(i, j int) bool { return targets[i].ID < targets[j].ID })
}
