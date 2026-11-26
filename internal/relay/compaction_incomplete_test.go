package relay

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/looplj/axonhub/llm"
	"github.com/looplj/axonhub/llm/httpclient"
)

func TestSummaryRejectsIncompleteOutput(t *testing.T) {
	for _, event := range []string{"response.incomplete", "response.completed"} {
		t.Run(event, func(t *testing.T) {
			var calls atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls.Add(1)
				w.Header().Set("Content-Type", "text/event-stream")
				fmt.Fprint(w, "event: response.output_text.delta\ndata: {\"type\":\"response.output_text.delta\",\"delta\":\"PARTIAL SUMMARY\"}\n\n")
				fmt.Fprintf(w, "event: %s\ndata: {\"type\":%q,\"response\":{\"status\":\"incomplete\",\"incomplete_details\":{\"reason\":\"max_output_tokens\"}}}\n\n", event, event)
			}))
			defer server.Close()
			target := responsesSummaryTarget{outbound: conversionTestOutbound(t, llm.APIFormatOpenAIResponse, server.URL), modelName: "model", passthrough: true}
			ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
			defer cancel()
			summary, err := summarizeResponsesHistory(map[string]any{"model": "model", "reasoning": map[string]any{"effort": "high"}}, nil, []any{map[string]any{"role": "user", "content": "EARLY FACT MUST SURVIVE"}}, func(body []byte) (string, error) {
				return summarizeResponsesForGroup(ctx, &httpclient.Request{Headers: make(http.Header)}, body, []responsesSummaryTarget{target})
			})
			if err != nil {
				t.Fatal(err)
			}
			if calls.Load() != responsesSummaryMaxAttempts {
				t.Errorf("attempts=%d want=%d", calls.Load(), responsesSummaryMaxAttempts)
			}
			result := string(conversionTestJSON(summary))
			if strings.Contains(result, "PARTIAL SUMMARY") || !strings.Contains(result, "EARLY FACT MUST SURVIVE") || !strings.Contains(result, "Conversation handoff transcript") {
				t.Fatalf("incomplete output replaced original history: %s", result)
			}
		})
	}
}
