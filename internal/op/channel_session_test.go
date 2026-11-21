package op

import (
	"testing"

	"github.com/bestruirui/octopus/internal/db"
	"github.com/bestruirui/octopus/internal/model"
)

func TestChannelSessionConfigMigration(t *testing.T) {
	ctx := setupChannelModelActionsTest(t)
	id, grantIDs := seedChannelModelActionsChannel(t, ctx)
	database := db.GetDB()
	// Only this test's temporary database is changed to simulate the old schema.
	for _, column := range []string{"session_id_header", "session_max_requests"} {
		if err := database.Exec("ALTER TABLE channels DROP COLUMN " + column).Error; err != nil {
			t.Fatal(err)
		}
	}
	if err := database.AutoMigrate(&model.Channel{}); err != nil {
		t.Fatal(err)
	}
	var saved model.Channel
	if err := database.Preload("Keys").Preload("Models").First(&saved, id).Error; err != nil {
		t.Fatal(err)
	}
	if saved.SessionIDHeader != "" || saved.SessionMaxRequests != model.DefaultSessionMaxRequests || saved.Name != "channel-actions" || len(saved.Keys) != 2 || len(saved.Models) != 2 {
		t.Fatalf("migration changed existing data or defaults: %+v", saved)
	}
	var grants int64
	if err := database.Model(&model.ChannelGrant{}).Where("id IN ?", append(grantIDs["model-a"], grantIDs["model-b"]...)).Count(&grants).Error; err != nil {
		t.Fatal(err)
	}
	if grants != 3 {
		t.Fatalf("migration lost grants: %d", grants)
	}
}

func TestChannelSessionConfigPersistence(t *testing.T) {
	ctx := setupChannelModelActionsTest(t)
	detail, err := ChannelCreate(&model.ChannelDetail{ChannelConfig: model.ChannelConfig{Name: "session-config", BaseURL: "https://example.invalid"}}, ctx)
	if err != nil {
		t.Fatal(err)
	}
	if detail.SessionIDHeader != "" || detail.SessionMaxRequests != 90 {
		t.Fatalf("bad defaults: %+v", detail.ChannelConfig)
	}
	detail.SessionIDHeader = " SESSION-ID "
	detail.SessionMaxRequests = 85
	if _, err := ChannelUpdate(detail, ctx); err != nil {
		t.Fatal(err)
	}
	var saved model.Channel
	if err := db.GetDB().First(&saved, detail.ID).Error; err != nil {
		t.Fatal(err)
	}
	if saved.SessionIDHeader != "session-id" || saved.SessionMaxRequests != 85 {
		t.Fatalf("enabled config did not persist: %+v", saved.ChannelConfig)
	}
	detail.SessionIDHeader = "session_id"
	if _, err := ChannelUpdate(detail, ctx); err != nil {
		t.Fatal(err)
	}
	saved = model.Channel{}
	if err := db.GetDB().First(&saved, detail.ID).Error; err != nil {
		t.Fatal(err)
	}
	if saved.SessionIDHeader != "session_id" {
		t.Fatal("alternate header did not persist")
	}
	detail.SessionIDHeader = ""
	if _, err := ChannelUpdate(detail, ctx); err != nil {
		t.Fatal(err)
	}
	saved = model.Channel{}
	if err := db.GetDB().First(&saved, detail.ID).Error; err != nil {
		t.Fatal(err)
	}
	if saved.SessionIDHeader != "" {
		t.Fatal("disabled config did not persist")
	}
	for _, config := range []model.ChannelConfig{
		{Name: "x", BaseURL: "https://example.invalid", SessionIDHeader: "thread-id"},
		{Name: "x", BaseURL: "https://example.invalid", SessionMaxRequests: -1},
		{Name: "x", BaseURL: "https://example.invalid", SessionMaxRequests: 100},
	} {
		if _, err := normalizeChannelConfig(config); err == nil {
			t.Fatalf("invalid config accepted: %+v", config)
		}
	}
}
