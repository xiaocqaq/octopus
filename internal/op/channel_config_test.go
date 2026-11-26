package op

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/bestruirui/octopus/internal/db"
	"github.com/bestruirui/octopus/internal/model"
)

func TestChannelResponsesAutoCompactRoundTrip(t *testing.T) {
	ctx := context.Background()
	if err := db.InitDB("sqlite", filepath.Join(t.TempDir(), "channel-config.db"), false); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = db.Close()
		channelCache.Clear()
		channelKeyCache.Clear()
		channelModelCache.Clear()
		channelGrantCache.Clear()
		groupCache.Clear()
		groupNameIndex.Clear()
	})
	channelCache.Clear()
	channelKeyCache.Clear()
	channelModelCache.Clear()
	channelGrantCache.Clear()
	groupCache.Clear()
	groupNameIndex.Clear()

	created, err := ChannelCreate(&model.ChannelDetail{
		ChannelConfig: model.ChannelConfig{Name: "channel-config-test", Enabled: true, BaseURL: "https://example.invalid", ResponsesAutoCompact: true},
		Keys:          []model.ChannelKeyConfig{{Name: "test", Key: "test-key", Enabled: true}},
		Models:        []string{"model"},
		Grants:        []model.ChannelGrantConfig{{ModelName: "model", KeyName: "test", Protocols: model.ProtocolOpenAIResponse}},
	}, ctx)
	if err != nil {
		t.Fatal(err)
	}
	if !created.ResponsesAutoCompact {
		t.Fatal("created channel lost responses_auto_compact")
	}
	if created.ResponsesSummaryTimeoutSeconds != model.DefaultResponsesSummaryTimeoutSeconds {
		t.Fatalf("default summary timeout=%d", created.ResponsesSummaryTimeoutSeconds)
	}
	created.ResponsesSummaryTimeoutSeconds = 600
	created.ResponsesAutoCompact = false
	updated, err := ChannelUpdate(created, ctx)
	if err != nil {
		t.Fatal(err)
	}
	if updated.ResponsesAutoCompact {
		t.Fatal("updated channel did not persist responses_auto_compact=false")
	}
	if updated.ResponsesSummaryTimeoutSeconds != 600 {
		t.Fatalf("updated summary timeout=%d", updated.ResponsesSummaryTimeoutSeconds)
	}
	var persisted model.Channel
	if err := db.GetDB().First(&persisted, created.ID).Error; err != nil {
		t.Fatal(err)
	}
	if persisted.ResponsesSummaryTimeoutSeconds != 600 {
		t.Fatalf("persisted summary timeout=%d", persisted.ResponsesSummaryTimeoutSeconds)
	}
	loaded, err := ChannelDetailGet(created.ID)
	if err != nil {
		t.Fatal(err)
	}
	if loaded.ResponsesSummaryTimeoutSeconds != 600 {
		t.Fatalf("cached summary timeout=%d", loaded.ResponsesSummaryTimeoutSeconds)
	}
	if loaded.ResponsesAutoCompact {
		t.Fatal("channel cache did not refresh responses_auto_compact=false")
	}
}
