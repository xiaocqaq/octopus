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
	"time"

	"github.com/bestruirui/octopus/internal/db"
	"github.com/bestruirui/octopus/internal/model"
	"github.com/bestruirui/octopus/internal/op"
	"github.com/gin-gonic/gin"
	"github.com/looplj/axonhub/llm"
	"github.com/tidwall/gjson"
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
	allowFullHistory := false
	var cancelSummary func(RequestState)
	var summaryDone chan struct{}
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
			state := assertCompactionRequestState(t, "compaction-test", true)
			if cancelSummary != nil {
				defer close(summaryDone)
				cancelSummary(state)
				select {
				case <-r.Context().Done():
				case <-time.After(2 * time.Second):
					t.Error("summary was not canceled")
				}
				return
			}
			if body["stream"] != true {
				t.Error("summary request must use Responses streaming")
			}
			w.Header().Set("Content-Type", "text/event-stream")
			_, _ = w.Write([]byte("event: response.output_text.delta\ndata: {\"type\":\"response.output_text.delta\",\"delta\":\"handoff summary\"}\n\nevent: response.completed\ndata: {\"type\":\"response.completed\",\"response\":{\"status\":\"completed\"}}\n\n"))
			return
		}
		assertCompactionRequestState(t, "compaction-test", false)
		if autoCompactEnabled && !allowFullHistory && assistantTurns >= responsesCompactAtTurns {
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
	if calls != 3 {
		t.Fatalf("upstream calls=%d, want two summaries and one response", calls)
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
	if recorder.Code != http.StatusOK || calls != 4 {
		t.Fatalf("disabled channel must forward without a summary: status=%d calls=%d", recorder.Code, calls)
	}

	channel.ResponsesAutoCompact = true
	if _, err := op.ChannelUpdate(channel, ctx); err != nil {
		t.Fatal(err)
	}
	allowFullHistory = true
	for _, entireRequest := range []bool{false, true} {
		t.Run(fmt.Sprintf("cancel_request_%t", entireRequest), func(t *testing.T) {
			beforeCalls := calls
			summaryDone = make(chan struct{})
			cancelSummary = func(state RequestState) {
				if entireRequest {
					CancelRequest(state.ID)
				} else {
					Interrupt(state.ID, state.Round)
				}
			}
			recorder = httptest.NewRecorder()
			c, _ = gin.CreateTestContext(recorder)
			c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", strings.NewReader(string(body)))
			c.Request.Header.Set("Content-Type", "application/json")
			Forward(llm.APIFormatOpenAIResponse)(c)
			<-summaryDone
			states, updates := OpenRequestStream()
			CloseRequestStream(updates)
			state := states[0]
			wantCalls, wantStatus := 2, StatusSuccess
			if entireRequest {
				wantCalls, wantStatus = 1, StatusCanceled
			}
			if calls-beforeCalls != wantCalls || state.Status != wantStatus || len(state.RetryErrors) != 0 {
				t.Errorf("canceled summary: calls=%d state=%+v", calls-beforeCalls, state)
			}
		})
	}
}

// Assert the serialized state that the log SSE sends while the upstream is busy.
func assertCompactionRequestState(t *testing.T, modelName string, compacting bool) RequestState {
	t.Helper()
	states, updates := OpenRequestStream()
	CloseRequestStream(updates)
	for _, state := range states {
		if state.Model != modelName || state.Status != StatusRunning {
			continue
		}
		encoded, err := json.Marshal(state)
		if err != nil {
			t.Error(err)
		}
		if state.Round == 0 || state.RoundStartedAt.IsZero() || !state.Sending || state.TargetChannelKey == "" || state.TargetModel != "model" || state.TargetProtocol != model.ProtocolOpenAIResponse || gjson.GetBytes(encoded, "compacting").Bool() != compacting {
			t.Errorf("unexpected active round during compaction=%t: %s", compacting, encoded)
		}
		return state
	}
	t.Errorf("no active request for %s", modelName)
	return RequestState{}
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
	var cancelSummary func(RequestState)
	var summaryDone chan struct{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Error(err)
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		switch {
		case calls == 1:
			w.WriteHeader(http.StatusBadRequest)
			_, _ = w.Write([]byte(`{"error":{"code":"session_turn_limit_exceeded","message":"100 turn limit"}}`))
		case strings.Contains(fmt.Sprint(body["instructions"]), "Summarize the supplied conversation segment"):
			state := assertCompactionRequestState(t, "compaction-retry-test", true)
			if cancelSummary != nil {
				defer close(summaryDone)
				cancelSummary(state)
				select {
				case <-r.Context().Done():
				case <-time.After(2 * time.Second):
					t.Error("forced summary was not canceled")
				}
				return
			}
			if body["stream"] != true {
				t.Errorf("summary request must use Responses streaming, got %v", body["stream"])
			}
			w.Header().Set("Content-Type", "text/event-stream")
			_, _ = w.Write([]byte("event: response.output_text.delta\ndata: {\"type\":\"response.output_text.delta\",\"delta\":\"handoff summary\"}\n\nevent: response.completed\ndata: {\"type\":\"response.completed\",\"response\":{\"status\":\"completed\"}}\n\n"))
		default:
			if cancelSummary != nil && (r.Header.Get("Session-Id") != "original-session" || body["prompt_cache_key"] != "original-session") {
				t.Error("canceled forced summary changed the original session")
			}
			items := body["input"].([]any)
			if cancelSummary == nil && responsesModelTurns(items) >= responsesCompactAtTurns {
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
	body := conversionTestJSON(map[string]any{"model": group.Name, "input": input, "stream": false, "prompt_cache_key": "original-session"})
	req := httptest.NewRequest(http.MethodPost, "/v1/responses", strings.NewReader(string(body)))
	req.Header.Set("Content-Type", "application/json")
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = req.WithContext(ctx)
	Forward(llm.APIFormatOpenAIResponse)(c)
	if recorder.Code != http.StatusOK || !strings.Contains(recorder.Body.String(), "Hello back") || calls != 4 {
		t.Fatalf("post-limit compaction failed: status=%d calls=%d body=%s", recorder.Code, calls, recorder.Body.String())
	}
	for _, entireRequest := range []bool{false, true} {
		t.Run(fmt.Sprintf("cancel_request_%t", entireRequest), func(t *testing.T) {
			calls = 0
			summaryDone = make(chan struct{})
			cancelSummary = func(state RequestState) {
				if entireRequest {
					CancelRequest(state.ID)
				} else {
					Interrupt(state.ID, state.Round)
				}
			}
			recorder := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(recorder)
			c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", strings.NewReader(string(body)))
			c.Request.Header.Set("Content-Type", "application/json")
			c.Request.Header.Set("Session-Id", "original-session")
			Forward(llm.APIFormatOpenAIResponse)(c)
			<-summaryDone
			states, updates := OpenRequestStream()
			CloseRequestStream(updates)
			state := states[0]
			wantCalls, wantStatus := 3, StatusSuccess
			if entireRequest {
				wantCalls, wantStatus = 2, StatusCanceled
			}
			if calls != wantCalls || state.Status != wantStatus || len(state.RetryErrors) != 1 || !strings.Contains(state.RetryErrors[0].Error, "session_turn_limit_exceeded") {
				t.Errorf("canceled forced summary: calls=%d state=%+v", calls, state)
			}
		})
	}

}

func TestForwardCompactsAfterLimitWhenClientSentShortenedHistory(t *testing.T) {
	for _, status := range []int{http.StatusOK, http.StatusBadRequest} {
		t.Run(fmt.Sprint(status), func(t *testing.T) {
			testForwardCompactsShortenedHistory(t, status)
		})
	}
}

func testForwardCompactsShortenedHistory(t *testing.T, summaryStatus int) {
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
		switch {
		case calls == 1:
			w.WriteHeader(http.StatusBadRequest)
			_, _ = w.Write([]byte(`{"error":{"code":"session_turn_limit_exceeded","message":"105 turn limit"}}`))
		case strings.Contains(fmt.Sprint(body["instructions"]), "Summarize the supplied"):
			summarySessionID = r.Header.Get("Session-Id")
			if body["stream"] != true {
				t.Errorf("summary request must use Responses streaming, got %v", body["stream"])
			}
			if summaryStatus != http.StatusOK {
				w.WriteHeader(summaryStatus)
				_, _ = w.Write([]byte(`{"error":{"message":"Upstream request failed","type":"upstream_error"}}`))
				return
			}
			w.Header().Set("Content-Type", "text/event-stream")
			_, _ = w.Write([]byte("event: response.output_text.delta\ndata: {\"type\":\"response.output_text.delta\",\"delta\":\"recovered handoff\"}\n\nevent: response.completed\ndata: {\"type\":\"response.completed\",\"response\":{\"status\":\"completed\"}}\n\n"))
		default:
			retrySessionID = r.Header.Get("Session-Id")
			input := body["input"].([]any)
			if len(input) >= 4 || responsesModelTurns(input) >= 10 {
				t.Errorf("retry should compact short client history: items=%d turns=%d", len(input), responsesModelTurns(input))
			}
			if summaryStatus != http.StatusOK && !strings.Contains(fmt.Sprint(input), "Acknowledged SHORT-105") {
				t.Error("summary failure must preserve the earlier conversation in the retry")
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
	wantCalls := 3
	if summaryStatus != http.StatusOK {
		wantCalls = 2 + responsesSummaryMaxAttempts
	}
	if recorder.Code != http.StatusOK || calls != wantCalls || !strings.Contains(recorder.Body.String(), "Hello back") || retrySessionID == "" || retrySessionID == firstSessionID || summarySessionID != retrySessionID {
		t.Fatalf("short-history post-limit recovery failed: status=%d calls=%d summary session=%q retry session=%q body=%s", recorder.Code, calls, summarySessionID, retrySessionID, recorder.Body.String())
	}
}
