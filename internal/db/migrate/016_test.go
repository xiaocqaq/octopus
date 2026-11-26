package migrate

import (
	"testing"

	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
)

func TestAddResponsesSummaryTimeoutToChannel(t *testing.T) {
	db, err := gorm.Open(sqlite.Open(t.TempDir()+"/migration.db"), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	if err := db.Exec("CREATE TABLE channels (id integer primary key, name text)").Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Exec("INSERT INTO channels (id, name) VALUES (1, 'legacy')").Error; err != nil {
		t.Fatal(err)
	}
	if err := addResponsesSummaryTimeoutToChannel(db); err != nil {
		t.Fatal(err)
	}
	var timeout int
	if err := db.Table("channels").Select("responses_summary_timeout_seconds").Where("id = ?", 1).Scan(&timeout).Error; err != nil {
		t.Fatal(err)
	}
	if timeout != 120 {
		t.Fatalf("legacy channel timeout=%d, want 120", timeout)
	}
	if err := db.Exec("UPDATE channels SET responses_summary_timeout_seconds = 600 WHERE id = 1").Error; err != nil {
		t.Fatal(err)
	}
	if err := addResponsesSummaryTimeoutToChannel(db); err != nil {
		t.Fatal(err)
	}
	if err := db.Table("channels").Select("responses_summary_timeout_seconds").Where("id = ?", 1).Scan(&timeout).Error; err != nil {
		t.Fatal(err)
	}
	if timeout != 600 {
		t.Fatalf("repeated migration replaced custom timeout with %d", timeout)
	}
}
