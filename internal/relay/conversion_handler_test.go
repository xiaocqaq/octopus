package relay

import (
	"context"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/bestruirui/octopus/internal/db"
	"github.com/bestruirui/octopus/internal/model"
	"github.com/bestruirui/octopus/internal/op"
	"github.com/gin-gonic/gin"
	"github.com/looplj/axonhub/llm"
)

// TestConversionForward 使用独立临时 SQLite 验证实际 Forward 的统计和冷却分支。
// 数据库、渠道和 key 都只属于测试进程，不读取项目 data 目录。
func TestConversionForward(t *testing.T) {
	if err := db.InitDB("sqlite", filepath.Join(t.TempDir(), "relay-test.db"), false); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := db.Close(); err != nil {
			t.Error(err)
		}
	})
	resetRoutes()
	t.Cleanup(resetRoutes)
	t.Cleanup(Clear)

	cases := []struct {
		name         string
		source       llm.APIFormat
		target       model.Protocol
		targetFormat llm.APIFormat
		stream       bool
		mutate       func(conversionTestMap)
		wire         string
		status       int
		calls        int32
		failed       bool
	}{
		{"reject_context", llm.APIFormatOpenAIResponse, model.ProtocolOpenAIChatCompletion, llm.APIFormatOpenAIChatCompletion, false, func(r conversionTestMap) { r["previous_response_id"] = "resp_other" }, "", 400, 0, false},
		{"reject_custom_tool", llm.APIFormatOpenAIResponse, model.ProtocolOpenAIChatCompletion, llm.APIFormatOpenAIChatCompletion, true, func(r conversionTestMap) {
			r["tools"] = []any{conversionTestMap{"type": "custom", "name": "execute", "format": conversionTestMap{"type": "text"}}}
		}, "", 400, 0, false},
		{"reject_json_schema", llm.APIFormatOpenAIChatCompletion, model.ProtocolAnthropicMessage, llm.APIFormatAnthropicMessage, false, func(r conversionTestMap) { r["response_format"] = conversionTestMap{"type": "json_object"} }, "", 400, 0, false},
		{"stream_usage", llm.APIFormatAnthropicMessage, model.ProtocolOpenAIChatCompletion, llm.APIFormatOpenAIChatCompletion, true, nil, "", 200, 1, false},
		{"stream_failed", llm.APIFormatAnthropicMessage, model.ProtocolOpenAIResponse, llm.APIFormatOpenAIResponse, true, nil,
			strings.ReplaceAll(strings.ReplaceAll(conversionTestStream(llm.APIFormatOpenAIResponse, false, true), "response.completed", "response.failed"), `"status":"completed"`, `"status":"failed"`), 200, 1, true},
		{"nonstream_length", llm.APIFormatOpenAIChatCompletion, model.ProtocolOpenAIResponse, llm.APIFormatOpenAIResponse, false, nil,
			strings.ReplaceAll(string(conversionTestJSON(conversionTestResponse(llm.APIFormatOpenAIResponse, false))), `"status":"completed"`, `"status":"incomplete"`), 200, 1, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var calls atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls.Add(1)
				if tc.stream {
					w.Header().Set("Content-Type", "text/event-stream")
				} else {
					w.Header().Set("Content-Type", "application/json")
				}
				wire := tc.wire
				if wire == "" {
					if tc.stream {
						wire = conversionTestStream(tc.targetFormat, false, true)
					} else {
						wire = string(conversionTestJSON(conversionTestResponse(tc.targetFormat, false)))
					}
				}
				_, _ = w.Write([]byte(wire))
			}))
			defer server.Close()
			ctx := context.Background()
			channel, err := op.ChannelCreate(&model.ChannelDetail{
				ChannelConfig: model.ChannelConfig{Name: tc.name, Enabled: true, BaseURL: server.URL},
				Keys:          []model.ChannelKeyConfig{{Name: "test", Key: "local-mock-key", Enabled: true}}, Models: []string{"model"},
				Grants: []model.ChannelGrantConfig{{ModelName: "model", KeyName: "test", Protocols: tc.target}},
			}, ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer op.ChannelDel(channel.ID, ctx)
			grantID := 0
			for _, grant := range op.ChannelGrantCandidates() {
				if grant.ChannelID == channel.ID {
					grantID = grant.ID
					break
				}
			}
			group, err := op.GroupCreate(&model.GroupCreateRequest{Name: "conversion-" + tc.name, Mode: model.GroupModeFailover, RelayConfig: model.DefaultGroupRelayConfig(), Items: []model.GroupItemInput{{ChannelGrantID: grantID}}}, ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer op.GroupDel(group.ID, ctx)
			defer ResetRouteState(group.ID)
			requestBody := conversionTestRequest(tc.source, tc.stream, false)
			requestBody["model"] = group.Name
			if tc.mutate != nil {
				tc.mutate(requestBody)
			}
			req := httptest.NewRequest("POST", "/test", strings.NewReader(string(conversionTestJSON(requestBody))))
			req.Header.Set("Content-Type", "application/json")
			requestCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
			defer cancel()
			recorder := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(recorder)
			c.Request = req.WithContext(requestCtx)
			Forward(tc.source)(c)
			if recorder.Code != tc.status {
				t.Fatalf("status=%d body=%s", recorder.Code, recorder.Body.String())
			}
			if calls.Load() != tc.calls {
				t.Fatalf("upstream calls=%d want=%d", calls.Load(), tc.calls)
			}
			stats, err := op.ChannelGet(channel.ID)
			if err != nil {
				t.Fatal(err)
			}
			route := RouteStateOf(*group)
			if tc.status == 400 {
				if !strings.Contains(recorder.Body.String(), "cannot convert") {
					t.Fatalf("missing actionable error: %s", recorder.Body.String())
				}
				if stats.RequestFailed != 0 || stats.RequestSuccess != 0 || len(route.Cooldowns) > 0 || route.ProbeItemID != 0 {
					t.Fatalf("local rejection penalized channel: stats=%+v route=%+v", stats.StatsMetrics, route)
				}
			} else {
				if stats.InputToken != 12 || stats.OutputToken != 4 {
					t.Errorf("wrong usage: %+v", stats.StatsMetrics)
				}
				if tc.failed {
					if stats.RequestFailed != 1 || stats.RequestSuccess != 0 || len(route.Cooldowns) != 1 {
						t.Fatalf("stream failure not recorded: stats=%+v route=%+v", stats.StatsMetrics, route)
					}
					if !strings.Contains(recorder.Body.String(), `"type":"error"`) {
						t.Fatalf("missing client error: %s", recorder.Body.String())
					}
				} else if stats.RequestFailed != 0 || stats.RequestSuccess != 1 || len(route.Cooldowns) > 0 {
					t.Fatalf("valid response penalized: stats=%+v route=%+v", stats.StatsMetrics, route)
				}
			}
		})
	}
}
