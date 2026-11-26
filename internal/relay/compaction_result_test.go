package relay

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/looplj/axonhub/llm"
	"github.com/looplj/axonhub/llm/httpclient"
)

func TestSummaryAcceptsCompletedTextWithoutRetry(t *testing.T) {
	for _, tc := range []struct{ name, events string }{
		{"completed_only", ""},
		{"partial_delta", "event: response.output_text.delta\ndata: {\"type\":\"response.output_text.delta\",\"delta\":\"hand\"}\n\n"},
		{"text_done", "event: response.output_text.done\ndata: {\"type\":\"response.output_text.done\",\"text\":\"handoff summary\"}\n\n"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var calls atomic.Int32
			closed := make(chan struct{}, responsesSummaryMaxAttempts)
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls.Add(1)
				w.Header().Set("Content-Type", "text/event-stream")
				fmt.Fprint(w, tc.events)
				output := `[{"type":"message","role":"assistant","content":[{"type":"output_text","text":"handoff summary"}]}]`
				if tc.name == "text_done" {
					output = "[]"
				}
				fmt.Fprintf(w, "event: response.completed\ndata: {\"type\":\"response.completed\",\"response\":{\"status\":\"completed\",\"output\":%s}}\n\n", output)
				w.(http.Flusher).Flush()
				// Completion must be consumed without waiting for the server to close the stream.
				<-r.Context().Done()
				closed <- struct{}{}
			}))
			defer server.Close()
			ctx, cancel := context.WithTimeout(context.Background(), time.Second)
			defer cancel()
			target := responsesSummaryTarget{outbound: conversionTestOutbound(t, llm.APIFormatOpenAIResponse, server.URL), modelName: "summary", passthrough: true}
			text, err := summarizeResponsesForGroup(ctx, &httpclient.Request{Headers: make(http.Header)}, []byte(`{"model":"summary","stream":true,"input":[]}`), []responsesSummaryTarget{target})
			if err != nil || text != "handoff summary" || calls.Load() != 1 {
				t.Errorf("completed summary discarded or retried: text=%q err=%v calls=%d", text, err, calls.Load())
			}
			select {
			case <-closed:
			case <-time.After(time.Second):
				t.Error("completed stream remained open")
			}
		})
	}
}
