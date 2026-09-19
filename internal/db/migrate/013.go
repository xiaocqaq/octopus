package migrate

import (
	"fmt"

	"gorm.io/gorm"
)

func init() {
	RegisterAfterAutoMigration(Migration{
		Version: 13,
		Up:      migrateReasoningFilterToGroup,
	})
}

// channelReasoningFilterColumn 只声明待删除的列: 模型里已经没有这个字段,
// GORM DropColumn 仍要一份带该列的 schema, 否则 MySQL/Postgres 端找不到列定义。
type channelReasoningFilterColumn struct {
	ReasoningFilter bool `gorm:"column:reasoning_filter"`
}

func (channelReasoningFilterColumn) TableName() string { return "channels" }

// migrateReasoningFilterToGroup 删除渠道上的思维凭据过滤列。开关已改挂在分组 Relay JSON 上,
// 加密思维链绑定签发账号, 同一供应商下并非每个模型都签发, 渠道级开关会误伤同渠道的其他模型。
// 存量渠道上开过的值不往分组拷: 拷过去等于按供应商全开, 与这次改粒度的目的相反; 需要的分组请在界面上单独打开。
func migrateReasoningFilterToGroup(db *gorm.DB) error {
	if db == nil {
		return fmt.Errorf("db is nil")
	}
	if !db.Migrator().HasTable("channels") {
		return nil
	}
	return dropColumnIfExists(db, &channelReasoningFilterColumn{}, "channels", "reasoning_filter")
}
