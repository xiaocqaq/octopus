package migrate

import (
	"path/filepath"
	"testing"

	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
)

// openTestSQLite 打开一个归属当前测试的临时 SQLite 库。
func openTestSQLite(t *testing.T) *gorm.DB {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(filepath.Join(t.TempDir(), "test.db")))
	if err != nil {
		t.Fatalf("failed to open sqlite: %v", err)
	}
	sqlDB, err := db.DB()
	if err != nil {
		t.Fatalf("failed to take sql.DB: %v", err)
	}
	// Windows 上不关连接, t.TempDir 的清理会因为文件仍被占用而失败。
	t.Cleanup(func() { sqlDB.Close() })
	return db
}

// createFreshChannelsTable 建出当前架构的 channels 表: 只有 id 主键, 没有 base_url/key/base_urls/type。
// id 用 "integer PRIMARY KEY AUTOINCREMENT" —— 建表语句里唯一含 key 一词的地方, 正是误判的来源。
func createFreshChannelsTable(t *testing.T, db *gorm.DB) {
	t.Helper()
	if err := db.Exec(`CREATE TABLE channels (id integer PRIMARY KEY AUTOINCREMENT, name text)`).Error; err != nil {
		t.Fatalf("failed to create channels: %v", err)
	}
}

// TestHasColumnMisjudgesSQLitePrimaryKey 固定住 SQLite 下 Migrator().HasColumn 会把
// "PRIMARY KEY" 里的 key 认成 channels.key 这一事实, 迁移代码因此必须改用 hasPhysicalColumn。
func TestHasColumnMisjudgesSQLitePrimaryKey(t *testing.T) {
	db := openTestSQLite(t)
	createFreshChannelsTable(t, db)

	if !db.Migrator().HasColumn("channels", "key") {
		t.Fatal("expected Migrator().HasColumn to misjudge SQLite PRIMARY KEY as a key column")
	}
	if hasPhysicalColumn(db, "channels", "key") {
		t.Fatal("expected hasPhysicalColumn to report that channels.key does not exist")
	}
}

// TestMigrateChannelToSingleURLAndKeyOnFreshSchema 全新安装(两列都不存在)必须直接跳过,
// 而不是像修复前那样返回错误 —— 那会让新装的 MySQL/Postgres 实例卡在迁移阶段起不来。
func TestMigrateChannelToSingleURLAndKeyOnFreshSchema(t *testing.T) {
	db := openTestSQLite(t)
	createFreshChannelsTable(t, db)

	if err := migrateChannelToSingleURLAndKey(db); err != nil {
		t.Fatalf("migration must be a no-op on a fresh schema, got: %v", err)
	}
}
