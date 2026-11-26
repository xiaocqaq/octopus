package relay

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/bestruirui/octopus/internal/model"
	"github.com/looplj/axonhub/llm"
	"github.com/looplj/axonhub/llm/httpclient"
	"github.com/tidwall/gjson"
)

func TestSummaryTimeoutConfiguration(t *testing.T) {
	for _, seconds := range []int{0, 120, 600, 86400} {
		want := seconds
		if want == 0 {
			want = 120
		}
		channel := model.Channel{ChannelConfig: model.ChannelConfig{ResponsesSummaryTimeoutSeconds: seconds}}
		if got := responsesSummaryTimeout(channel); got != time.Duration(want)*time.Second {
			t.Errorf("timeout for %d = %s", seconds, got)
		}
	}
	targets := []responsesSummaryTarget{
		{channel: model.Channel{ChannelConfig: model.ChannelConfig{ResponsesSummaryTimeoutSeconds: 120}}},
		{channel: model.Channel{ChannelConfig: model.ChannelConfig{ResponsesSummaryTimeoutSeconds: 600}}},
	}
	if got := responsesSummaryBudget(targets); got != 2400*time.Second {
		t.Fatalf("summary budget=%s, want 40m", got)
	}
}

func TestSummaryUsesEachCandidateTimeout(t *testing.T) {
	done := make(chan struct{})
	primary := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer close(done)
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
		case <-time.After(5 * time.Second):
			t.Error("summary was not canceled at channel timeout")
		}
	}))
	defer primary.Close()
	backup := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Longer than the first candidate's entire budget, but within this channel's own budget.
		select {
		case <-r.Context().Done():
			return
		case <-time.After(1200 * time.Millisecond):
		}
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprint(w, conversionTestStream(llm.APIFormatOpenAIResponse, false, true))
	}))
	defer backup.Close()
	var targets []responsesSummaryTarget
	for i, server := range []*httptest.Server{primary, backup} {
		targets = append(targets, responsesSummaryTarget{channel: model.Channel{ChannelConfig: model.ChannelConfig{BaseURL: server.URL, ResponsesSummaryTimeoutSeconds: i + 1}}, outbound: conversionTestOutbound(t, llm.APIFormatOpenAIResponse, server.URL), modelName: "summary", passthrough: true})
	}
	ctx, cancel := context.WithTimeout(context.Background(), 6*time.Second)
	defer cancel()
	text, err := summarizeResponsesForGroup(ctx, &httpclient.Request{Headers: make(http.Header)}, conversionTestJSON(map[string]any{"model": "original", "stream": true, "input": []any{map[string]any{"role": "user", "content": "summarize"}}}), targets)
	<-done
	if err != nil || text != "Hello back" {
		t.Fatalf("per-channel timeout failed: text=%q err=%v", text, err)
	}
}

func TestSummaryInheritsReasoningEffort(t *testing.T) {
	for _, effort := range []string{"", "none", "low", "high", "xhigh"} {
		t.Run("effort_"+effort, func(t *testing.T) {
			payload := map[string]any{"model": "model", "tools": []any{map[string]any{"type": "function"}}}
			if effort != "" {
				payload["reasoning"] = map[string]any{"effort": effort, "summary": "auto"}
			}
			calls := 0
			_, err := summarizeResponsesChunks(payload, nil, [][]any{{map[string]any{"role": "user", "content": "first"}}, {map[string]any{"role": "user", "content": "second"}}}, func(body []byte) (string, error) {
				calls++
				if got := gjson.GetBytes(body, "reasoning.effort").String(); got != effort {
					t.Errorf("reasoning effort=%q want=%q", got, effort)
				}
				wantTokens := int64(2048)
				if effort != "" && effort != "none" {
					wantTokens = 32768
				}
				if got := gjson.GetBytes(body, "max_output_tokens").Int(); got != wantTokens {
					t.Errorf("summary token budget=%d want=%d", got, wantTokens)
				}
				if effort == "" && gjson.GetBytes(body, "reasoning").Exists() {
					t.Error("added reasoning without original effort")
				}
				if gjson.GetBytes(body, "reasoning.summary").Exists() || gjson.GetBytes(body, "tools").Exists() {
					t.Error("inherited unrelated settings")
				}
				return "summary", nil
			})
			if err != nil || calls != 2 {
				t.Fatalf("summaries=%d err=%v", calls, err)
			}
		})
	}
}
