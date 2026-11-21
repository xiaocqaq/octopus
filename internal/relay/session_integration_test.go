package relay

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"

	"github.com/bestruirui/octopus/internal/model"
	"github.com/google/uuid"
	"github.com/looplj/axonhub/llm"
	"github.com/looplj/axonhub/llm/httpclient"
)

func TestResponsesSessionHTTPRecoveryAndThreshold(t *testing.T) {
	for _, header := range []string{"session-id", "session_id"} {
		for _, streaming := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/%v", header, streaming), func(t *testing.T) {
				body := fmt.Sprintf(`{"model":"mock","stream":%t,"input":"hello"}`, streaming)
				const success = `{"id":"resp_mock","object":"response","model":"mock","status":"completed","output":[{"id":"msg_mock","type":"message","role":"assistant","content":[{"type":"output_text","text":"ok"}]}]}`
				var mu sync.Mutex
				var ids []string
				server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
					data, _ := io.ReadAll(req.Body)
					if string(data) != body || req.Header.Get("thread-id") != "thread" {
						t.Error("body or thread changed")
					}
					mu.Lock()
					ids = append(ids, req.Header.Get(header))
					n := len(ids)
					mu.Unlock()
					if n == 1 {
						w.WriteHeader(400)
						fmt.Fprint(w, `{"error":{"code":"session_request_limit_exceeded"}}`)
						return
					}
					if streaming {
						w.Header().Set("Content-Type", "text/event-stream")
						fmt.Fprintf(w, "event: response.completed\ndata: {\"type\":\"response.completed\",\"response\":%s}\n\n", success)
					} else {
						w.Header().Set("Content-Type", "application/json")
						fmt.Fprint(w, success)
					}
				}))
				defer server.Close()
				channel := model.Channel{ID: 2, ChannelConfig: model.ChannelConfig{SessionIDHeader: header, SessionMaxRequests: 2}}
				raw := &httpclient.Request{Method: "POST", Headers: make(http.Header), Body: []byte(body)}
				original := uuid.NewString()
				raw.Headers.Set(header, original)
				raw.Headers.Set("thread-id", "thread")
				outbound := conversionTestOutbound(t, llm.APIFormatOpenAIResponse, server.URL)
				for range 3 {
					spent := false
					result, err := sendPassthrough(context.Background(), llm.APIFormatOpenAIResponse, raw, channel, outbound, streaming, "mock", 1, 3, &spent)
					if err != nil {
						t.Fatal(err)
					}
					if result == nil {
						t.Fatal("missing successful response")
					}
					if streaming {
						if result.first == nil || !result.last {
							t.Fatal("missing terminal response")
						}
						result.events.Close()
						if result.closeIdle != nil {
							result.closeIdle()
						}
					} else if string(result.body) != success {
						t.Fatal("successful body changed")
					}
				}
				mu.Lock()
				defer mu.Unlock()
				if len(ids) != 4 || ids[0] == original || ids[0] == ids[1] || ids[1] != ids[2] || ids[2] == ids[3] {
					t.Fatalf("wrong recovery/threshold sequence: %v", ids)
				}
				if raw.Headers.Get(header) != original {
					t.Fatal("inbound ID changed")
				}
			})
		}
	}
}

func TestResponsesSessionAcceptedStreamNeverReplayed(t *testing.T) {
	calls := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		calls++
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprint(w, `event: response.output_text.delta
data: {"type":"response.output_text.delta","delta":"partial"}

event: error
data: {"type":"error","code":"session_request_limit_exceeded","message":"late failure"}

`)
	}))
	defer server.Close()
	raw := &httpclient.Request{Method: "POST", Headers: http.Header{"Session-Id": {uuid.NewString()}}, Body: []byte(`{"model":"mock","stream":true,"input":"hello"}`)}
	channel := model.Channel{ID: 2, ChannelConfig: model.ChannelConfig{SessionIDHeader: "session-id"}}
	outbound := conversionTestOutbound(t, llm.APIFormatOpenAIResponse, server.URL)
	spent := false
	result, err := sendPassthrough(context.Background(), llm.APIFormatOpenAIResponse, raw, channel, outbound, true, "mock", 1, 3, &spent)
	if err != nil {
		t.Fatal(err)
	}
	defer result.events.Close()
	if result.closeIdle != nil {
		defer result.closeIdle()
	}
	if result.first == nil {
		t.Fatal("stream was not accepted")
	}
	sawError := false
	for result.events.Next() {
		if _, err := inspectStreamEvent(llm.APIFormatOpenAIResponse, result.events.Current()); err != nil {
			sawError = true
		}
	}
	if !sawError || calls != 1 || spent {
		t.Fatalf("late error replayed or lost: calls=%d spent=%v sawError=%v", calls, spent, sawError)
	}
}
