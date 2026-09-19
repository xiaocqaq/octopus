package relay

import (
	"context"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/bestruirui/octopus/internal/db"
	"github.com/bestruirui/octopus/internal/model"
	"github.com/bestruirui/octopus/internal/op"
	"github.com/gin-gonic/gin"
	"github.com/looplj/axonhub/llm"
)

func groupStatsByName(t *testing.T, name string) model.StatsMetrics {
	t.Helper()
	rows, err := op.StatsGroupList(context.Background(), 0)
	if err != nil {
		t.Fatal(err)
	}
	for _, row := range rows {
		if row.GroupName == name {
			return row.StatsMetrics
		}
	}
	t.Fatalf("group %s missing", name)
	return model.StatsMetrics{}
}

func TestForwardGroupStatsNotDuplicated(t *testing.T) {
	if err := db.InitDB("sqlite", filepath.Join(t.TempDir(), "group-stats.db"), false); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := db.Close(); err != nil {
			t.Error(err)
		}
	})
	if err := op.InitCache(); err != nil {
		t.Fatal(err)
	}
	resetRoutes()
	t.Cleanup(resetRoutes)
	t.Cleanup(Clear)

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(conversionTestJSON(conversionTestResponse(llm.APIFormatOpenAIChatCompletion, false)))
	}))
	defer server.Close()

	ctx := context.Background()
	channel, err := op.ChannelCreate(&model.ChannelDetail{
		ChannelConfig: model.ChannelConfig{Name: "shared", Enabled: true, BaseURL: server.URL},
		Keys: []model.ChannelKeyConfig{
			{Name: "k1", Key: "local-mock-key-1", Enabled: true},
			{Name: "k2", Key: "local-mock-key-2", Enabled: true},
		},
		Models: []string{"deepseek"},
		Grants: []model.ChannelGrantConfig{
			{ModelName: "deepseek", KeyName: "k1", Protocols: model.ProtocolOpenAIChatCompletion},
			{ModelName: "deepseek", KeyName: "k2", Protocols: model.ProtocolOpenAIChatCompletion},
		},
	}, ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer op.ChannelDel(channel.ID, ctx)

	var grantIDs []int
	for _, grant := range op.ChannelGrantCandidates() {
		if grant.ChannelID == channel.ID {
			grantIDs = append(grantIDs, grant.ID)
		}
	}
	if len(grantIDs) != 2 {
		t.Fatalf("grants=%v", grantIDs)
	}

	groupA, err := op.GroupCreate(&model.GroupCreateRequest{
		Name: "group-a", Mode: model.GroupModeFailover, RelayConfig: model.DefaultGroupRelayConfig(),
		Items: []model.GroupItemInput{{ChannelGrantID: grantIDs[0]}, {ChannelGrantID: grantIDs[1]}},
	}, ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer op.GroupDel(groupA.ID, ctx)
	groupB, err := op.GroupCreate(&model.GroupCreateRequest{
		Name: "group-b", Mode: model.GroupModeFailover, RelayConfig: model.DefaultGroupRelayConfig(),
		Items: []model.GroupItemInput{{ChannelGrantID: grantIDs[0]}},
	}, ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer op.GroupDel(groupB.ID, ctx)

	forward := func(groupName string) {
		t.Helper()
		body := conversionTestRequest(llm.APIFormatOpenAIChatCompletion, false, false)
		body["model"] = groupName
		req := httptest.NewRequest("POST", "/test", strings.NewReader(string(conversionTestJSON(body))))
		req.Header.Set("Content-Type", "application/json")
		requestCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
		defer cancel()
		recorder := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(recorder)
		c.Request = req.WithContext(requestCtx)
		Forward(llm.APIFormatOpenAIChatCompletion)(c)
		if recorder.Code != 200 {
			t.Fatalf("%s status=%d body=%s", groupName, recorder.Code, recorder.Body.String())
		}
	}

	forward("group-a")
	forward("group-a")
	forward("group-b")

	a := groupStatsByName(t, "group-a")
	b := groupStatsByName(t, "group-b")
	if a.RequestSuccess != 2 || b.RequestSuccess != 1 {
		t.Fatalf("request counts A=%+v B=%+v", a, b)
	}
	if a.InputToken != 24 || b.InputToken != 12 {
		t.Fatalf("tokens A=%+v B=%+v", a, b)
	}
	channelStats, err := op.ChannelGet(channel.ID)
	if err != nil {
		t.Fatal(err)
	}
	if channelStats.RequestSuccess != 3 {
		t.Fatalf("channel should still count each request once: %+v", channelStats.StatsMetrics)
	}
}
