package relay

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/bestruirui/octopus/internal/db"
	"github.com/bestruirui/octopus/internal/model"
	"github.com/bestruirui/octopus/internal/op"
	"github.com/gin-gonic/gin"
	"github.com/looplj/axonhub/llm"
)

func TestForwardCompactsResponsesBeforeLimit(t *testing.T) {
	if err := db.InitDB("sqlite", filepath.Join(t.TempDir(), "compaction.db"), false); err != nil {
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

	calls := 0
	autoCompactEnabled := true
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Error(err)
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		input := body["input"].([]any)
		assistantTurns := 0
		for _, item := range input {
			if item.(map[string]any)["role"] == "assistant" {
				assistantTurns++
			}
		}
		w.Header().Set("Content-Type", "application/json")
		if strings.Contains(fmt.Sprint(body["instructions"]), "Summarize the supplied conversation segment") {
			if body["stream"] != true {
				t.Error("summary request must use Responses streaming")
			}
			w.Header().Set("Content-Type", "text/event-stream")
			_, _ = w.Write([]byte("event: response.output_text.delta\ndata: {\"type\":\"response.output_text.delta\",\"delta\":\"handoff summary\"}\n\nevent: response.completed\ndata: {\"type\":\"response.completed\",\"response\":{\"status\":\"completed\"}}\n\n"))
			return
		}
		if autoCompactEnabled && assistantTurns >= responsesCompactAtTurns {
			t.Errorf("full history reached upstream: %d turns", assistantTurns)
			w.WriteHeader(http.StatusBadRequest)
			_, _ = w.Write([]byte(`{"error":{"code":"session_turn_limit_exceeded"}}`))
			return
		}
		_, _ = w.Write(conversionTestJSON(conversionTestResponse(llm.APIFormatOpenAIResponse, false)))
	}))
	defer server.Close()

	ctx := context.Background()
	channel, err := op.ChannelCreate(&model.ChannelDetail{
		ChannelConfig: model.ChannelConfig{Name: "compaction-test", Enabled: true, BaseURL: server.URL, ResponsesAutoCompact: true},
		Keys:          []model.ChannelKeyConfig{{Name: "test", Key: "local-mock-key", Enabled: true}},
		Models:        []string{"model"},
		Grants:        []model.ChannelGrantConfig{{ModelName: "model", KeyName: "test", Protocols: model.ProtocolOpenAIResponse}},
	}, ctx)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = op.ChannelDel(channel.ID, ctx) })
	grantID := 0
	for _, grant := range op.ChannelGrantCandidates() {
		if grant.ChannelID == channel.ID {
			grantID = grant.ID
		}
	}
	group, err := op.GroupCreate(&model.GroupCreateRequest{
		Name: "compaction-test", Mode: model.GroupModeFailover, RelayConfig: model.DefaultGroupRelayConfig(),
		Items: []model.GroupItemInput{{ChannelGrantID: grantID}},
	}, ctx)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = op.GroupDel(group.ID, ctx)
		ResetRouteState(group.ID)
	})

	input := []any{map[string]any{"role": "developer", "content": "Keep the system instructions"}}
	for i := 0; i < responsesCompactAtTurns; i++ {
		input = append(input, map[string]any{"role": "user", "content": "question"}, map[string]any{"role": "assistant", "content": "answer"})
	}
	input = append(input, map[string]any{"role": "user", "content": "active question"})
	body := conversionTestJSON(map[string]any{"model": group.Name, "input": input, "stream": false})
	req := httptest.NewRequest(http.MethodPost, "/v1/responses", strings.NewReader(string(body)))
	req.Header.Set("Content-Type", "application/json")
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = req.WithContext(ctx)
	Forward(llm.APIFormatOpenAIResponse)(c)

	if recorder.Code != http.StatusOK || !strings.Contains(recorder.Body.String(), "Hello back") {
		t.Fatalf("forward failed after compaction: status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	if calls != 2 {
		t.Fatalf("upstream calls=%d, want one summary and one response", calls)
	}

	autoCompactEnabled = false
	channel.ResponsesAutoCompact = false
	if _, err := op.ChannelUpdate(channel, ctx); err != nil {
		t.Fatal(err)
	}
	recorder = httptest.NewRecorder()
	c, _ = gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", strings.NewReader(string(body)))
	c.Request.Header.Set("Content-Type", "application/json")
	Forward(llm.APIFormatOpenAIResponse)(c)
	if recorder.Code != http.StatusOK || calls != 3 {
		t.Fatalf("disabled channel must forward without a summary: status=%d calls=%d", recorder.Code, calls)
	}
}

func TestForwardCompactsAfterSessionLimitError(t *testing.T) {
	if err := db.InitDB("sqlite", filepath.Join(t.TempDir(), "compaction-retry.db"), false); err != nil {
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

	var calls int
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Error(err)
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		switch calls {
		case 1:
			w.WriteHeader(http.StatusBadRequest)
			_, _ = w.Write([]byte(`{"error":{"code":"session_turn_limit_exceeded","message":"100 turn limit"}}`))
		case 2:
			if body["stream"] != true {
				t.Errorf("summary request must use Responses streaming, got %v", body["stream"])
			}
			w.Header().Set("Content-Type", "text/event-stream")
			_, _ = w.Write([]byte("event: response.output_text.delta\ndata: {\"type\":\"response.output_text.delta\",\"delta\":\"handoff summary\"}\n\nevent: response.completed\ndata: {\"type\":\"response.completed\",\"response\":{\"status\":\"completed\"}}\n\n"))
		case 3:
			items := body["input"].([]any)
			if responsesModelTurns(items) >= responsesCompactAtTurns {
				t.Errorf("retried request still at limit: turns=%d", responsesModelTurns(items))
			}
			_, _ = w.Write(conversionTestJSON(conversionTestResponse(llm.APIFormatOpenAIResponse, false)))
		}
	}))
	defer server.Close()

	ctx := context.Background()
	channel, err := op.ChannelCreate(&model.ChannelDetail{
		ChannelConfig: model.ChannelConfig{Name: "compaction-retry-test", Enabled: true, BaseURL: server.URL, ResponsesAutoCompact: true},
		Keys:          []model.ChannelKeyConfig{{Name: "test", Key: "local-mock-key", Enabled: true}},
		Models:        []string{"model"},
		Grants:        []model.ChannelGrantConfig{{ModelName: "model", KeyName: "test", Protocols: model.ProtocolOpenAIResponse}},
	}, ctx)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = op.ChannelDel(channel.ID, ctx) })
	var grantID int
	for _, grant := range op.ChannelGrantCandidates() {
		if grant.ChannelID == channel.ID {
			grantID = grant.ID
		}
	}
	group, err := op.GroupCreate(&model.GroupCreateRequest{
		Name: "compaction-retry-test", Mode: model.GroupModeFailover, RelayConfig: model.DefaultGroupRelayConfig(),
		Items: []model.GroupItemInput{{ChannelGrantID: grantID}},
	}, ctx)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = op.GroupDel(group.ID, ctx)
		ResetRouteState(group.ID)
	})

	input := make([]any, 0, responsesCompactAtTurns*3+1)
	for i := 0; i < responsesCompactAtTurns; i++ {
		input = append(input,
			map[string]any{"role": "user", "content": "run tool"},
			map[string]any{"type": "function_call", "call_id": fmt.Sprintf("call_%d", i), "name": "probe", "arguments": "{}"},
			map[string]any{"type": "function_call_output", "call_id": fmt.Sprintf("call_%d", i), "output": "ok"},
		)
	}
	input = append(input, map[string]any{"role": "user", "content": "active task"})
	body := conversionTestJSON(map[string]any{"model": group.Name, "input": input, "stream": false})
	req := httptest.NewRequest(http.MethodPost, "/v1/responses", strings.NewReader(string(body)))
	req.Header.Set("Content-Type", "application/json")
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = req.WithContext(ctx)
	Forward(llm.APIFormatOpenAIResponse)(c)
	if recorder.Code != http.StatusOK || !strings.Contains(recorder.Body.String(), "Hello back") || calls != 3 {
		t.Fatalf("post-limit compaction failed: status=%d calls=%d body=%s", recorder.Code, calls, recorder.Body.String())
	}
}

func TestForwardCompactsAfterLimitWhenClientSentShortenedHistory(t *testing.T) {
	if err := db.InitDB("sqlite", filepath.Join(t.TempDir(), "compaction-short-history.db"), false); err != nil {
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

	var calls int
	firstSessionID, summarySessionID, retrySessionID := "", "", ""
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		if calls == 1 {
			firstSessionID = r.Header.Get("Session-Id")
		}
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Error(err)
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		switch calls {
		case 1:
			w.WriteHeader(http.StatusBadRequest)
			_, _ = w.Write([]byte(`{"error":{"code":"session_turn_limit_exceeded","message":"105 turn limit"}}`))
		case 2:
			summarySessionID = r.Header.Get("Session-Id")
			if body["stream"] != true {
				t.Errorf("summary request must use Responses streaming, got %v", body["stream"])
			}
			w.Header().Set("Content-Type", "text/event-stream")
			_, _ = w.Write([]byte("event: response.output_text.delta\ndata: {\"type\":\"response.output_text.delta\",\"delta\":\"recovered handoff\"}\n\nevent: response.completed\ndata: {\"type\":\"response.completed\",\"response\":{\"status\":\"completed\"}}\n\n"))
		case 3:
			retrySessionID = r.Header.Get("Session-Id")
			input := body["input"].([]any)
			if len(input) >= 4 || responsesModelTurns(input) >= 10 {
				t.Errorf("retry should compact short client history: items=%d turns=%d", len(input), responsesModelTurns(input))
			}
			_, _ = w.Write(conversionTestJSON(conversionTestResponse(llm.APIFormatOpenAIResponse, false)))
		}
	}))
	defer server.Close()

	ctx := context.Background()
	channel, err := op.ChannelCreate(&model.ChannelDetail{
		ChannelConfig: model.ChannelConfig{Name: "compaction-short-history-test", Enabled: true, BaseURL: server.URL, ResponsesAutoCompact: true},
		Keys:          []model.ChannelKeyConfig{{Name: "test", Key: "local-mock-key", Enabled: true}},
		Models:        []string{"model"},
		Grants:        []model.ChannelGrantConfig{{ModelName: "model", KeyName: "test", Protocols: model.ProtocolOpenAIResponse}},
	}, ctx)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = op.ChannelDel(channel.ID, ctx) })
	var grantID int
	for _, grant := range op.ChannelGrantCandidates() {
		if grant.ChannelID == channel.ID {
			grantID = grant.ID
		}
	}
	group, err := op.GroupCreate(&model.GroupCreateRequest{
		Name: "compaction-short-history-test", Mode: model.GroupModeFailover, RelayConfig: model.DefaultGroupRelayConfig(),
		Items: []model.GroupItemInput{{ChannelGrantID: grantID}},
	}, ctx)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = op.GroupDel(group.ID, ctx)
		ResetRouteState(group.ID)
	})

	input := []any{
		map[string]any{"role": "developer", "content": "Core task identifier SHORT-105"},
		map[string]any{"role": "user", "content": "Continue the task"},
		map[string]any{"role": "assistant", "content": "Acknowledged SHORT-105"},
		map[string]any{"role": "user", "content": "Finish and return SHORT-105"},
	}
	body := conversionTestJSON(map[string]any{"model": group.Name, "input": input, "stream": false})
	req := httptest.NewRequest(http.MethodPost, "/v1/responses", strings.NewReader(string(body)))
	req.Header.Set("Content-Type", "application/json")
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = req.WithContext(ctx)
	Forward(llm.APIFormatOpenAIResponse)(c)
	if recorder.Code != http.StatusOK || calls != 3 || !strings.Contains(recorder.Body.String(), "Hello back") || retrySessionID == "" || retrySessionID == firstSessionID || summarySessionID != retrySessionID {
		t.Fatalf("short-history post-limit recovery failed: status=%d calls=%d summary session=%q retry session=%q body=%s", recorder.Code, calls, summarySessionID, retrySessionID, recorder.Body.String())
	}
}
