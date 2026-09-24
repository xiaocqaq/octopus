package op

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/bestruirui/octopus/internal/db"
	"github.com/bestruirui/octopus/internal/model"
)

func setupChannelModelActionsTest(t *testing.T) context.Context {
	t.Helper()
	ctx := context.Background()
	if err := db.InitDB("sqlite", filepath.Join(t.TempDir(), "channel-model-actions.db"), false); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = db.Close()
		groupCache.Clear()
		groupNameIndex.Clear()
		channelCache.Clear()
		channelKeyCache.Clear()
		channelModelCache.Clear()
		channelGrantCache.Clear()
	})
	groupCache.Clear()
	groupNameIndex.Clear()
	channelCache.Clear()
	channelKeyCache.Clear()
	channelModelCache.Clear()
	channelGrantCache.Clear()
	return ctx
}

func seedChannelModelActionsChannel(t *testing.T, ctx context.Context) (int, map[string][]int) {
	t.Helper()
	detail := &model.ChannelDetail{
		ChannelConfig: model.ChannelConfig{Name: "channel-actions", Dialect: model.DialectGeneric, Enabled: true, BaseURL: "https://example.invalid"},
		Keys:          []model.ChannelKeyConfig{{Name: "key-a", Key: "sk-a", Enabled: true}, {Name: "key-b", Key: "sk-b", Enabled: true}},
		Models:        []string{"model-a", "model-b"},
		Grants: []model.ChannelGrantConfig{
			{ModelName: "model-a", KeyName: "key-a", Protocols: model.ProtocolOpenAIResponse},
			{ModelName: "model-a", KeyName: "key-b", Protocols: model.ProtocolOpenAIResponse},
			{ModelName: "model-b", KeyName: "key-a", Protocols: model.ProtocolOpenAIResponse},
		},
	}
	channel, err := ChannelCreate(detail, ctx)
	if err != nil {
		t.Fatal(err)
	}
	ids := map[string][]int{}
	for _, candidate := range ChannelGrantCandidates() {
		if candidate.ChannelID == channel.ID {
			ids[candidate.ModelName] = append(ids[candidate.ModelName], candidate.ID)
		}
	}
	return channel.ID, ids
}

func TestGroupAssignChannelModelsAppendsOnlySelectedModel(t *testing.T) {
	ctx := setupChannelModelActionsTest(t)
	channelID, grants := seedChannelModelActionsChannel(t, ctx)
	group, err := GroupCreate(&model.GroupCreateRequest{Name: "target", Mode: model.GroupModeFailover}, ctx)
	if err != nil {
		t.Fatal(err)
	}
	results, err := GroupAssignChannelModels(&model.GroupAssignChannelModelsRequest{
		ChannelID:   channelID,
		Assignments: []model.ChannelModelGroupAssignment{{ModelName: "model-a", GroupID: group.ID}},
	}, ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 1 || results[0].Skipped || results[0].GrantCount != 2 {
		t.Fatalf("unexpected result: %+v", results)
	}
	got, err := GroupGet(group.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Items) != 2 {
		t.Fatalf("want both enabled grants of model-a, got %d", len(got.Items))
	}
	for _, item := range got.Items {
		if !containsInt(grants["model-a"], item.ChannelGrantID) || containsInt(grants["model-b"], item.ChannelGrantID) {
			t.Fatalf("wrong member appended: %+v", item)
		}
	}
}

func TestGroupAssignChannelModelsIsIdempotentAndPreservesExisting(t *testing.T) {
	ctx := setupChannelModelActionsTest(t)
	channelID, grants := seedChannelModelActionsChannel(t, ctx)
	group, err := GroupCreate(&model.GroupCreateRequest{
		Name: "target", Mode: model.GroupModeFailover,
		Items: []model.GroupItemInput{{ChannelGrantID: grants["model-b"][0]}},
	}, ctx)
	if err != nil {
		t.Fatal(err)
	}
	first, err := GroupAssignChannelModels(&model.GroupAssignChannelModelsRequest{
		ChannelID:   channelID,
		Assignments: []model.ChannelModelGroupAssignment{{ModelName: "model-a", GroupID: group.ID}},
	}, ctx)
	if err != nil || first[0].GrantCount != 2 {
		t.Fatalf("first assignment failed: results=%+v err=%v", first, err)
	}
	before, _ := GroupGet(group.ID)
	second, err := GroupAssignChannelModels(&model.GroupAssignChannelModelsRequest{
		ChannelID:   channelID,
		Assignments: []model.ChannelModelGroupAssignment{{ModelName: "model-a", GroupID: group.ID}},
	}, ctx)
	if err != nil || second[0].GrantCount != 0 {
		t.Fatalf("second assignment should be idempotent: results=%+v err=%v", second, err)
	}
	after, _ := GroupGet(group.ID)
	if len(after.Items) != len(before.Items) || after.Items[0].ChannelGrantID != before.Items[0].ChannelGrantID || after.Items[0].Priority != before.Items[0].Priority {
		t.Fatalf("existing member/order changed: before=%+v after=%+v", before.Items, after.Items)
	}
}

func TestGroupAssignChannelModelsSkipsNoTarget(t *testing.T) {
	ctx := setupChannelModelActionsTest(t)
	channelID, _ := seedChannelModelActionsChannel(t, ctx)
	results, err := GroupAssignChannelModels(&model.GroupAssignChannelModelsRequest{
		ChannelID:   channelID,
		Assignments: []model.ChannelModelGroupAssignment{{ModelName: "model-a", GroupID: 0}},
	}, ctx)
	if err != nil || len(results) != 1 || !results[0].Skipped {
		t.Fatalf("no target should be skipped: results=%+v err=%v", results, err)
	}
}

func TestGroupAssignChannelModelsReportsUnsavedWithoutGrants(t *testing.T) {
	ctx := setupChannelModelActionsTest(t)
	channelID, _ := seedChannelModelActionsChannel(t, ctx)
	group, err := GroupCreate(&model.GroupCreateRequest{Name: "target", Mode: model.GroupModeFailover}, ctx)
	if err != nil {
		t.Fatal(err)
	}
	results, err := GroupAssignChannelModels(&model.GroupAssignChannelModelsRequest{
		ChannelID:   channelID,
		Assignments: []model.ChannelModelGroupAssignment{{ModelName: "not-saved", GroupID: group.ID}},
	}, ctx)
	if err != nil || len(results) != 1 || !results[0].Skipped || results[0].Reason != "model_not_saved" || results[0].GrantCount != 0 {
		t.Fatalf("unsaved model must not look successful: results=%+v err=%v", results, err)
	}
}

func TestGroupAssignChannelModelsCreatesUnsavedGrantThenAppends(t *testing.T) {
	ctx := setupChannelModelActionsTest(t)
	channelID, _ := seedChannelModelActionsChannel(t, ctx)
	group, err := GroupCreate(&model.GroupCreateRequest{Name: "target", Mode: model.GroupModeFailover}, ctx)
	if err != nil {
		t.Fatal(err)
	}
	results, err := GroupAssignChannelModels(&model.GroupAssignChannelModelsRequest{
		ChannelID: channelID,
		KeyName:   "key-a",
		Assignments: []model.ChannelModelGroupAssignment{{
			ModelName: "fresh-model",
			GroupID:   group.ID,
			Grants:    []model.ChannelGrantConfig{{ModelName: "fresh-model", KeyName: "key-a", Protocols: model.ProtocolOpenAIResponse}},
		}},
	}, ctx)
	if err != nil || len(results) != 1 || results[0].Skipped || results[0].GrantCount != 1 || results[0].GroupName != "target" {
		t.Fatalf("unsaved grant should be created and appended: results=%+v err=%v", results, err)
	}
	got, err := GroupGet(group.ID)
	if err != nil || len(got.Items) != 1 || got.Items[0].ModelName != "fresh-model" || got.Items[0].KeyName != "key-a" {
		t.Fatalf("group did not receive the new grant: %+v err=%v", got.Items, err)
	}
}

func containsInt(values []int, want int) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}
