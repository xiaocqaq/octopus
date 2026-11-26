package relay

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/bestruirui/octopus/internal/db"
	"github.com/bestruirui/octopus/internal/model"
	"github.com/bestruirui/octopus/internal/op"
	"github.com/gin-gonic/gin"
	"github.com/looplj/axonhub/llm"
	"github.com/looplj/axonhub/llm/httpclient"
	"github.com/tidwall/gjson"
)

func TestForwardSummaryUsesGroupChannels(t *testing.T) {
	if err := db.InitDB("sqlite", filepath.Join(t.TempDir(), "summary-group.db"), false); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	resetRoutes()
	t.Cleanup(resetRoutes)
	t.Cleanup(Clear)
	for _, tc := range []struct {
		format  llm.APIFormat
		failure string
	}{
		{llm.APIFormatOpenAIResponse, "error"},
		{llm.APIFormatOpenAIChatCompletion, "error"},
		{llm.APIFormatAnthropicMessage, "error"},
		{llm.APIFormatOpenAIChatCompletion, "truncated"},
		{llm.APIFormatOpenAIResponse, "empty"},
		{llm.APIFormatOpenAIResponse, "slow"},
	} {
		t.Run(conversionTestName(tc.format)+"_"+tc.failure, func(t *testing.T) {
			var primarySummaries, primaryResponses, backupCalls atomic.Int32
			var summaryDone sync.WaitGroup
			summaryDone.Add(2)
			primary := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				var body map[string]any
				if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
					t.Error(err)
					return
				}
				if strings.Contains(fmt.Sprint(body["instructions"]), "Summarize the supplied") {
					primarySummaries.Add(1)
					defer summaryDone.Done()
					switch tc.failure {

					case "truncated":
						w.Header().Set("Content-Type", "text/event-stream")
						fmt.Fprint(w, "event: response.output_text.delta\ndata: {\"type\":\"response.output_text.delta\",\"delta\":\"partial\"}\n\n")
					case "empty":
						w.Header().Set("Content-Type", "text/event-stream")
						fmt.Fprint(w, "event: response.completed\ndata: {\"type\":\"response.completed\",\"response\":{\"status\":\"completed\"}}\n\n")
					default:
						if tc.failure == "slow" {
							time.Sleep(1100 * time.Millisecond)
						}
						w.WriteHeader(http.StatusServiceUnavailable)
					}
					return
				}
				primaryResponses.Add(1)
				if tc.failure == "slow" {
					time.Sleep(600 * time.Millisecond)
				}
				encoded := conversionTestJSON(body)
				if !strings.Contains(string(encoded), "Conversation handoff summary") || !strings.Contains(string(encoded), "Hello back") || !strings.Contains(string(encoded), "active question") {
					t.Error("backup summary or current question missing from main request")
				}
				if body["model"] != "primary-model" || gjson.GetBytes(encoded, "reasoning.effort").String() != "high" {
					t.Error("main request configuration changed")
				}
				w.Header().Set("Content-Type", "application/json")
				w.Write(conversionTestJSON(conversionTestResponse(llm.APIFormatOpenAIResponse, false)))
			}))
			defer primary.Close()
			backup := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if primarySummaries.Load() != backupCalls.Load()+1 {
					t.Error("preferred member was not tried first")
				}
				backupCalls.Add(1)
				var body map[string]any
				if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
					t.Error(err)
					return
				}
				encoded := string(conversionTestJSON(body))
				if body["model"] != "backup-model" || body["stream"] != true || backupCalls.Load() == 1 && (!strings.Contains(encoded, "EARLY-FACT") || !strings.Contains(encoded, "tool result")) {
					t.Error("backup received incorrect model or incomplete history")
				}
				if strings.Contains(encoded, "encrypted-secret") || body["tools"] != nil {
					t.Error("summary inherited private history or original generation settings")
				}
				if tc.format == llm.APIFormatOpenAIResponse && gjson.Get(encoded, "reasoning.effort").String() != "high" {
					t.Error("Responses summary lost reasoning effort")
				}
				if tc.format == llm.APIFormatOpenAIChatCompletion && gjson.Get(encoded, "reasoning_effort").String() != "high" {
					t.Error("Chat summary lost reasoning effort")
				}
				if tc.format == llm.APIFormatAnthropicMessage {
					if gjson.Get(encoded, "output_config.effort").String() != "high" {
						t.Error("Anthropic summary lost reasoning effort")
					}
					if r.Header.Get("X-Api-Key") != "key-backup" {
						t.Error("backup credential not used")
					}
				} else if r.Header.Get("Authorization") != "Bearer key-backup" {
					t.Error("backup credential not used")
				}
				w.Header().Set("Content-Type", "text/event-stream")
				fmt.Fprint(w, conversionTestStream(tc.format, false, true))
			}))
			defer backup.Close()
			primaryGrant := createSummaryGroupMember(t, "primary", "primary-model", primary.URL, model.ProtocolOpenAIResponse, true)
			protocol := model.ProtocolOpenAIResponse
			if tc.format == llm.APIFormatOpenAIChatCompletion {
				protocol = model.ProtocolOpenAIChatCompletion
			}
			if tc.format == llm.APIFormatAnthropicMessage {
				protocol = model.ProtocolAnthropicMessage
			}
			backupGrant := createSummaryGroupMember(t, "backup", "backup-model", backup.URL, protocol, true)
			disabledGrant := createSummaryGroupMember(t, "disabled", "disabled-model", backup.URL, protocol, false)
			config := model.DefaultGroupRelayConfig()
			config.MemberNonStreamResponseTimeoutSeconds = 1
			group, err := op.GroupCreate(&model.GroupCreateRequest{Name: "summary-group", Mode: model.GroupModeManual, RelayConfig: config, Items: []model.GroupItemInput{{ChannelGrantID: backupGrant}, {ChannelGrantID: disabledGrant}, {ChannelGrantID: primaryGrant}}}, context.Background())
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = op.GroupDel(group.ID, context.Background()); ResetRouteState(group.ID) })
			var selected int
			for _, item := range group.Items {
				if item.ChannelGrantID == primaryGrant {
					selected = item.ID
				}
			}
			group, err = op.GroupUpdate(group.ID, &model.GroupUpdateRequest{ActiveItemID: &selected}, context.Background())
			if err != nil {
				t.Fatal(err)
			}
			input := []any{map[string]any{"role": "developer", "content": "Keep constraints"}, map[string]any{"role": "user", "content": "EARLY-FACT"}, map[string]any{"type": "reasoning", "encrypted_content": "encrypted-secret"}, map[string]any{"type": "function_call_output", "call_id": "old-call", "output": "tool result"}}
			for i := 0; i < responsesCompactAtTurns; i++ {
				input = append(input, map[string]any{"role": "user", "content": "question"}, map[string]any{"role": "assistant", "content": "answer"})
			}
			input = append(input, map[string]any{"role": "user", "content": "active question"})
			body := conversionTestJSON(map[string]any{"model": group.Name, "input": input, "stream": false, "reasoning": map[string]any{"effort": "high"}})
			recorder := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(recorder)
			c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", strings.NewReader(string(body)))
			c.Request.Header.Set("Content-Type", "application/json")
			started := time.Now()
			Forward(llm.APIFormatOpenAIResponse)(c)
			summaryDone.Wait()
			if recorder.Code != http.StatusOK || primarySummaries.Load() != 2 || primaryResponses.Load() != 1 || backupCalls.Load() != 2 {
				t.Fatalf("summary failover failed: status=%d primary summaries=%d responses=%d backup=%d body=%s", recorder.Code, primarySummaries.Load(), primaryResponses.Load(), backupCalls.Load(), recorder.Body.String())
			}
			if time.Since(started) > 4*time.Second {
				t.Error("summary failover exceeded total budget")
			}
			if state := RouteStateOf(*group); state.CurrentItemID != selected || len(state.Cooldowns) != 0 {
				t.Errorf("summary changed main route: %+v", state)
			}
		})
	}
}

func createSummaryGroupMember(t *testing.T, name, modelName, baseURL string, protocol model.Protocol, enabled bool) int {
	t.Helper()
	channel, err := op.ChannelCreate(&model.ChannelDetail{ChannelConfig: model.ChannelConfig{Name: name, Enabled: enabled, BaseURL: baseURL, ResponsesAutoCompact: name == "primary"}, Keys: []model.ChannelKeyConfig{{Name: "test", Key: "key-" + name, Enabled: true}}, Models: []string{modelName}, Grants: []model.ChannelGrantConfig{{ModelName: modelName, KeyName: "test", Protocols: protocol}}}, context.Background())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = op.ChannelDel(channel.ID, context.Background()) })
	for _, grant := range op.ChannelGrantCandidates() {
		if grant.ChannelID == channel.ID {
			return grant.ID
		}
	}
	t.Fatal("missing summary grant")
	return 0
}

func TestSummaryChunksShareTotalBudget(t *testing.T) {
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Error(err)
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprint(w, "event: response.output_text.delta\ndata: {\"type\":\"response.output_text.delta\",\"delta\":\"partial\"}\n\n")
		w.(http.Flusher).Flush()
		select {
		case <-r.Context().Done():
		case <-time.After(time.Second):
			t.Error("summary request was not canceled")
		}
	}))
	defer server.Close()
	channel := model.Channel{ChannelConfig: model.ChannelConfig{BaseURL: server.URL}}
	target := responsesSummaryTarget{channel: channel, outbound: conversionTestOutbound(t, llm.APIFormatOpenAIResponse, server.URL), modelName: "summary-model", passthrough: true}
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	input := []any{map[string]any{"role": "user", "content": "EARLY-FACT"}}
	for i := 0; i < 200; i++ {
		input = append(input, map[string]any{"role": "user", "content": fmt.Sprint(i)}, map[string]any{"role": "assistant", "content": "answer"})
	}
	input = append(input, map[string]any{"role": "user", "content": "active question"})
	body := conversionTestJSON(map[string]any{"model": "model", "input": input})
	started := time.Now()
	got, changed, err := compactResponsesBody(body, func(body []byte) (string, error) {
		return summarizeResponsesForGroup(ctx, &httpclient.Request{Headers: make(http.Header)}, body, []responsesSummaryTarget{target})
	})
	if err != nil || !changed || calls.Load() != 1 || time.Since(started) > time.Second {
		t.Fatalf("summary budget reset between chunks: changed=%t calls=%d err=%v", changed, calls.Load(), err)
	}
	for _, fact := range []string{"EARLY-FACT", "active question", "199"} {
		if !strings.Contains(string(got), fact) {
			t.Errorf("deadline fallback lost %s", fact)
		}
	}
	_, items, err := decodeResponsesInput(got)
	if err != nil || responsesModelTurns(items) != responsesRetainedTurns {
		t.Fatalf("deadline fallback did not retain recent turns: %v", err)
	}
}

func TestSummaryRetriesAndSwitches(t *testing.T) {
	for _, tc := range []struct {
		name     string
		channels int
		success  bool
	}{{"single", 1, false}, {"two", 2, false}, {"fifth_candidate", 5, true}} {
		t.Run(tc.name, func(t *testing.T) {
			var mu sync.Mutex
			var sequence []string
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				var body map[string]any
				if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
					t.Error(err)
					return
				}
				name := body["model"].(string)
				mu.Lock()
				sequence = append(sequence, name)
				mu.Unlock()
				w.Header().Set("Content-Type", "text/event-stream")
				if tc.success {
					if name == "summary-4" {
						fmt.Fprint(w, conversionTestStream(llm.APIFormatOpenAIResponse, false, true))
					} else {
						w.WriteHeader(http.StatusServiceUnavailable)
					}
					return
				}
				w.WriteHeader(http.StatusServiceUnavailable)
			}))
			defer server.Close()
			var targets []responsesSummaryTarget
			for i := 0; i < tc.channels; i++ {
				targets = append(targets, responsesSummaryTarget{channel: model.Channel{ChannelConfig: model.ChannelConfig{BaseURL: server.URL}}, outbound: conversionTestOutbound(t, llm.APIFormatOpenAIResponse, server.URL), modelName: fmt.Sprintf("summary-%d", i), passthrough: true})
			}
			ctx, cancel := context.WithTimeout(context.Background(), time.Second)
			defer cancel()
			body := conversionTestJSON(map[string]any{"model": "original", "stream": true, "input": []any{map[string]any{"role": "user", "content": "summarize"}}})
			text, err := summarizeResponsesForGroup(ctx, &httpclient.Request{Headers: make(http.Header)}, body, targets)
			mu.Lock()
			gotSequence := append([]string(nil), sequence...)
			mu.Unlock()
			want := responsesSummaryMaxAttempts * tc.channels
			if tc.success {
				want = tc.channels
				if err != nil || text != "Hello back" {
					t.Errorf("last candidate failed: text=%q err=%v", text, err)
				}
			} else if err == nil || text != "" {
				t.Errorf("partial or timed out summary accepted: text=%q err=%v", text, err)
			}
			if len(gotSequence) != want {
				t.Fatalf("attempts=%d want=%d sequence=%v", len(gotSequence), want, gotSequence)
			}
			for i, name := range gotSequence {
				if name != fmt.Sprintf("summary-%d", i%tc.channels) {
					t.Errorf("candidate order=%v", gotSequence)
					break
				}
			}
		})
	}
}

func TestSummaryCandidatesPreserveFailoverState(t *testing.T) {
	if err := db.InitDB("sqlite", filepath.Join(t.TempDir(), "summary-route.db"), false); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	resetRoutes()
	t.Cleanup(resetRoutes)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	defer server.Close()
	primary := createSummaryGroupMember(t, "primary", "primary-model", server.URL, model.ProtocolOpenAIResponse, true)
	cooling := createSummaryGroupMember(t, "cooling", "cooling-model", server.URL, model.ProtocolOpenAIResponse, true)
	backup := createSummaryGroupMember(t, "backup", "backup-model", server.URL, model.ProtocolOpenAIResponse, true)
	image := createSummaryGroupMember(t, "image", "image-model", server.URL, model.ProtocolOpenAIImage, true)
	group, err := op.GroupCreate(&model.GroupCreateRequest{Name: "summary-route", Mode: model.GroupModeFailover, RelayConfig: model.DefaultGroupRelayConfig(), Items: []model.GroupItemInput{{ChannelGrantID: primary}, {ChannelGrantID: cooling}, {ChannelGrantID: image}, {ChannelGrantID: backup}}}, context.Background())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = op.GroupDel(group.ID, context.Background()); ResetRouteState(group.ID) })
	var selected, coolingItem int
	for _, item := range group.Items {
		if item.ChannelGrantID == primary {
			selected = item.ID
		}
		if item.ChannelGrantID == cooling {
			coolingItem = item.ID
		}
	}
	routeMu.Lock()
	routes[group.ID] = &RouteState{GroupID: group.ID, CurrentItemID: selected, ProbeItemID: coolingItem, AffinityUntil: time.Now().Add(time.Minute).UnixMilli(), Cooldowns: map[int]int64{coolingItem: time.Now().Add(time.Minute).UnixMilli()}}
	routeMu.Unlock()
	before := RouteStateOf(*group)
	targets := responsesSummaryTargets(*group, primary)
	if len(targets) != 2 || targets[0].grantID != primary || targets[1].grantID != backup {
		t.Fatalf("wrong summary candidates: %+v", targets)
	}
	// 在已取得候选后禁用备用渠道，下次发起前也必须重新校验。
	grant, err := op.ChannelGrantGet(backup)
	if err != nil {
		t.Fatal(err)
	}
	channel, err := op.ChannelGet(grant.ChannelModel.ChannelID)
	if err != nil {
		t.Fatal(err)
	}
	detail, err := op.ChannelDetailGet(channel.ID)
	if err != nil {
		t.Fatal(err)
	}
	detail.Enabled = false
	if _, err := op.ChannelUpdate(&detail, context.Background()); err != nil {
		t.Fatal(err)
	}
	if _, ok := responsesSummaryTargetForGrant(backup); ok {
		t.Error("disabled summary channel remained usable")
	}
	if after := RouteStateOf(*group); !reflect.DeepEqual(before, after) {
		t.Errorf("summary changed failover state: before=%+v after=%+v", before, after)
	}
}
