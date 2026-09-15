package migrate

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/bestruirui/octopus/internal/model"
	"gorm.io/gorm"
)

func init() {
	RegisterAfterAutoMigration(Migration{
		Version: 5,
		Up:      migrateChannelToSingleURLAndKey,
	})
}

// migrateChannelToSingleURLAndKey 将多地址、多凭据渠道收敛为单地址、单凭据，并删除旧结构。
func migrateChannelToSingleURLAndKey(db *gorm.DB) error {
	if db == nil {
		return fmt.Errorf("db is nil")
	}
	if !db.Migrator().HasTable("channels") {
		return nil
	}
	// 本迁移针对旧架构(渠道行自带 base_url 与 key 两列)。新库的 channels 表没有 key 列
	// (凭据在 channel_keys 表), 旧架构要么早已收敛完毕, 要么根本不存在, 两种情况都无事可做。
	// 此前这里把缺列当作错误抛出, 全新 MySQL/Postgres 安装会在 AutoMigrate 建出无 key 列的表后直接卡死;
	// SQLite 只是侥幸: 驱动按建表语句模糊匹配, PRIMARY KEY 里的 key 让它误判为存在。
	if !db.Migrator().HasColumn("channels", "base_url") || !db.Migrator().HasColumn("channels", "key") {
		return nil
	}

	if db.Migrator().HasColumn("channels", "base_urls") {
		type legacyBaseURL struct {
			URL string `json:"url"` // 旧地址值。
		}
		type legacyChannel struct {
			ID       int    `gorm:"column:id"`        // 渠道主键。
			BaseURLs string `gorm:"column:base_urls"` // 旧地址数组 JSON。
		}

		rows := make([]legacyChannel, 0)
		if err := db.Table("channels").Select("id, base_urls").Find(&rows).Error; err != nil {
			return fmt.Errorf("failed to read channels.base_urls: %w", err)
		}
		for _, row := range rows {
			if strings.TrimSpace(row.BaseURLs) == "" || strings.TrimSpace(row.BaseURLs) == "null" {
				continue
			}
			urls := make([]legacyBaseURL, 0)
			if err := json.Unmarshal([]byte(row.BaseURLs), &urls); err != nil {
				return fmt.Errorf("failed to decode channels.base_urls for id=%d: %w", row.ID, err)
			}
			if len(urls) == 0 || strings.TrimSpace(urls[0].URL) == "" {
				continue
			}
			if err := db.Table("channels").Where("id = ? AND (base_url IS NULL OR base_url = '')", row.ID).Update("base_url", urls[0].URL).Error; err != nil {
				return fmt.Errorf("failed to migrate channels.base_urls for id=%d: %w", row.ID, err)
			}
		}
	}

	if db.Migrator().HasTable("channel_keys") && hasPhysicalColumn(db, "channel_keys", "channel_key") {
		type legacyChannelKey struct {
			ChannelID  int    `gorm:"column:channel_id"`  // 所属渠道主键。
			ChannelKey string `gorm:"column:channel_key"` // 旧凭据值。
		}

		keys := make([]legacyChannelKey, 0)
		// 按旧记录主键顺序读取，每个渠道只保留第一项。
		if err := db.Table("channel_keys").
			Select("channel_id, channel_key").
			Where("channel_key <> ''").
			Order("channel_id ASC, id ASC").
			Find(&keys).Error; err != nil {
			return fmt.Errorf("failed to read channel_keys: %w", err)
		}
		selected := make(map[int]struct{})
		var quotedKey strings.Builder
		db.Dialector.QuoteTo(&quotedKey, "key")
		emptyKeyCondition := fmt.Sprintf("(%s IS NULL OR %s = '')", quotedKey.String(), quotedKey.String())
		for _, key := range keys {
			if _, ok := selected[key.ChannelID]; ok || strings.TrimSpace(key.ChannelKey) == "" {
				continue
			}
			if err := db.Table("channels").Where("id = ? AND "+emptyKeyCondition, key.ChannelID).Update("key", key.ChannelKey).Error; err != nil {
				return fmt.Errorf("failed to migrate channel key for channel_id=%d: %w", key.ChannelID, err)
			}
			selected[key.ChannelID] = struct{}{}
		}
	}

	// Migration 11 重新引入了 channel_keys 表用于多凭据架构，只有在旧架构（channels.type 存在）时才删除该表。
	// Migration 11 会删除 channels.type 列，若该列不存在说明 Migration 11 已执行，channel_keys 表不应删除。
	if db.Migrator().HasTable("channel_keys") && hasPhysicalColumn(db, "channels", "type") {
		if err := db.Migrator().DropTable("channel_keys"); err != nil {
			return fmt.Errorf("failed to drop channel_keys: %w", err)
		}
	}
	if db.Migrator().HasColumn("channels", "base_urls") {
		if db.Dialector.Name() == "sqlite" {
			if err := db.Exec(`ALTER TABLE "channels" DROP COLUMN "base_urls"`).Error; err != nil {
				return fmt.Errorf("failed to drop channels.base_urls: %w", err)
			}
		} else if err := db.Migrator().DropColumn(&model.Channel{}, "base_urls"); err != nil {
			return fmt.Errorf("failed to drop channels.base_urls: %w", err)
		}
	}
	return nil
}
