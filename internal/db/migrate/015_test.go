package migrate

import (
	"testing"

	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
)

func TestAddResponsesAutoCompactToChannel(t *testing.T) {
	db, err := gorm.Open(sqlite.Open(t.TempDir()+"/migration.db"), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	if err := db.Exec("CREATE TABLE channels (id integer primary key, name text)").Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Exec("INSERT INTO channels (id, name) VALUES (1, 'test')").Error; err != nil {
		t.Fatal(err)
	}
	if err := addResponsesAutoCompactToChannel(db); err != nil {
		t.Fatal(err)
	}
	var result struct {
		ResponsesAutoCompact bool `gorm:"column:responses_auto_compact"`
	}
	if err := db.Table("channels").Select("responses_auto_compact").Where("id = ?", 1).Take(&result).Error; err != nil {
		t.Fatal(err)
	}
	if result.ResponsesAutoCompact {
		t.Fatal("legacy channels should have auto compact disabled by default")
	}
	if err := addResponsesAutoCompactToChannel(db); err != nil {
		t.Fatalf("migration should be safe to repeat: %v", err)
	}
}
