package migrate

import (
	"fmt"

	"github.com/bestruirui/octopus/internal/model"
	"gorm.io/gorm"
)

func init() {
	RegisterBeforeAutoMigration(Migration{
		Version: 15,
		Up:      addResponsesAutoCompactToChannel,
	})
}

func addResponsesAutoCompactToChannel(db *gorm.DB) error {
	if db == nil {
		return fmt.Errorf("db is nil")
	}
	if !db.Migrator().HasTable("channels") || hasPhysicalColumn(db, "channels", "responses_auto_compact") {
		return nil
	}
	if err := db.Migrator().AddColumn(&model.Channel{}, "ResponsesAutoCompact"); err != nil {
		return fmt.Errorf("failed to add channels.responses_auto_compact: %w", err)
	}
	return nil
}
