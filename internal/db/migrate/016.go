package migrate

import (
	"fmt"

	"github.com/bestruirui/octopus/internal/model"
	"gorm.io/gorm"
)

func init() {
	RegisterBeforeAutoMigration(Migration{Version: 16, Up: addResponsesSummaryTimeoutToChannel})
}

func addResponsesSummaryTimeoutToChannel(db *gorm.DB) error {
	if db == nil {
		return fmt.Errorf("db is nil")
	}
	if !db.Migrator().HasTable("channels") || hasPhysicalColumn(db, "channels", "responses_summary_timeout_seconds") {
		return nil
	}
	if err := db.Migrator().AddColumn(&model.Channel{}, "ResponsesSummaryTimeoutSeconds"); err != nil {
		return fmt.Errorf("failed to add channels.responses_summary_timeout_seconds: %w", err)
	}
	return nil
}
