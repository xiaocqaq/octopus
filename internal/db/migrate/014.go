package migrate

import (
	"fmt"

	"gorm.io/gorm"
)

func init() {
	RegisterAfterAutoMigration(Migration{
		Version: 14,
		Up:      migrateScheduledProbeTargets,
	})
}

// scheduledProbeLegacyColumn 只声明待读写的旧列。
// 模型里已经没有 channel_id / model_name 了, 但迁移要先把它们的值搬进新表再删列,
// 故这里单独描述一份"只有旧列"的 schema。
type scheduledProbeLegacyColumn struct {
	ID        int    `gorm:"primaryKey"`
	ChannelID int    `gorm:"column:channel_id"`
	ModelName string `gorm:"column:model_name"`
}

func (scheduledProbeLegacyColumn) TableName() string { return "scheduled_probes" }

// scheduledProbeTargetRow 是目标表的最小 schema, 迁移期间不依赖 model 包, 避免模型再次变更时牵动本文件。
type scheduledProbeTargetRow struct {
	ID        int    `gorm:"primaryKey"`
	ProbeID   int    `gorm:"column:probe_id;not null;index"`
	ChannelID int    `gorm:"column:channel_id;not null"`
	ModelName string `gorm:"column:model_name;not null"`
}

func (scheduledProbeTargetRow) TableName() string { return "scheduled_probe_targets" }

// migrateScheduledProbeTargets 把初版"一条任务一个模型"的两列搬成任务下的目标行, 然后删掉旧列。
//
// 为什么要搬而不是丢: 那两列是用户已经配好的监控对象, 直接删列等于把他们的任务清空,
// 而升级后任务还在、目标却没了, 界面上一片空白, 用户根本不知道发生过什么。
//
// name 留空由界面兜底显示: 初版没有自定义名字, 这里不拿模型名去填 ——
// 一个任务将来可能挂多个模型, 用其中一个的名字当任务名反而误导。
func migrateScheduledProbeTargets(db *gorm.DB) error {
	if db == nil {
		return fmt.Errorf("db is nil")
	}
	if !db.Migrator().HasTable("scheduled_probes") {
		return nil
	}
	// 全新安装: 表由 AutoMigrate 按新 schema 建出, 已经没有旧列, 无事可做。
	if !hasPhysicalColumn(db, "scheduled_probes", "channel_id") {
		return nil
	}

	if err := db.AutoMigrate(&scheduledProbeTargetRow{}); err != nil {
		return fmt.Errorf("failed to create scheduled_probe_targets: %w", err)
	}

	var legacy []scheduledProbeLegacyColumn
	if err := db.Find(&legacy).Error; err != nil {
		return fmt.Errorf("failed to read legacy scheduled probes: %w", err)
	}
	for _, probe := range legacy {
		// 模型名为空的旧行跳过: 那种行本来就测不出任何东西, 搬过去只会多一行空目标。
		if probe.ModelName == "" || probe.ChannelID == 0 {
			continue
		}
		row := scheduledProbeTargetRow{ProbeID: probe.ID, ChannelID: probe.ChannelID, ModelName: probe.ModelName}
		// 用 FirstOrCreate 语义手写判断而不是 clause.OnConflict: 旧表里同一任务只会有一行,
		// 正常不会撞, 但重跑迁移(上次中途失败)时不该插出第二份目标。
		var count int64
		if err := db.Model(&scheduledProbeTargetRow{}).
			Where("probe_id = ? AND channel_id = ? AND model_name = ?", row.ProbeID, row.ChannelID, row.ModelName).
			Count(&count).Error; err != nil {
			return fmt.Errorf("failed to check existing target: %w", err)
		}
		if count > 0 {
			continue
		}
		if err := db.Create(&row).Error; err != nil {
			return fmt.Errorf("failed to migrate target for probe %d: %w", probe.ID, err)
		}
	}

	// 两列都删掉: 留着就是一份永远没人再读的旧数据, 而且下次 AutoMigrate 也不会替我们删。
	// 必须先删索引再删列: SQLite 拒绝删除仍被索引引用的列, 报 "error in index ... after drop column"。
	// 这里直接点名索引而不是先探测: 初版模型给这两列都加了 index, 索引名由 GORM 按表名与列名生成, 固定可预期。
	for _, index := range []string{"idx_scheduled_probes_channel_id", "idx_scheduled_probes_model_name"} {
		if !db.Migrator().HasIndex(&scheduledProbeLegacyColumn{}, index) {
			continue
		}
		if err := db.Migrator().DropIndex(&scheduledProbeLegacyColumn{}, index); err != nil {
			return fmt.Errorf("failed to drop index %s: %w", index, err)
		}
	}
	if err := dropColumnIfExists(db, &scheduledProbeLegacyColumn{}, "scheduled_probes", "channel_id"); err != nil {
		return err
	}
	return dropColumnIfExists(db, &scheduledProbeLegacyColumn{}, "scheduled_probes", "model_name")
}
