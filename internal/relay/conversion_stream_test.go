package relay

import (
	"context"
	"errors"
	"io"
	"strings"
	"testing"

	"github.com/looplj/axonhub/llm"
	"github.com/looplj/axonhub/llm/httpclient"
	"github.com/looplj/axonhub/llm/streams"
)

// terminalWatchStream 模拟收到终态后仍保持连接的上游：终态之后再 Next 即为错误。
type terminalWatchStream struct {
	source            streams.Stream[*httpclient.StreamEvent]
	format            llm.APIFormat
	finished          bool
	readAfterTerminal bool
}

func (s *terminalWatchStream) Next() bool {
	if s.finished {
		s.readAfterTerminal = true
		return false
	}
	return s.source.Next()
}
func (s *terminalWatchStream) Current() *httpclient.StreamEvent {
	event := s.source.Current()
	s.finished, _ = inspectStreamEvent(s.format, event)
	return event
}
func (s *terminalWatchStream) Err() error {
	if s.readAfterTerminal {
		return errors.New("read blocked after terminal")
	}
	return s.source.Err()
}
func (s *terminalWatchStream) Close() error { return s.source.Close() }

func TestConvertedStreamStopsAtUpstreamTerminal(t *testing.T) {
	for _, format := range conversionTestFormats {
		t.Run(conversionTestName(format), func(t *testing.T) {
			decoder := httpclient.NewDefaultSSEDecoder(context.Background(), io.NopCloser(strings.NewReader(conversionTestStream(format, false, true))))
			watch := &terminalWatchStream{source: decoder, format: format}
			guard := &validatedUpstreamStream{source: watch, format: format}
			defer guard.Close()
			last := false
			for guard.Next() {
				event := guard.Current()
				if guard.Current() != event {
					t.Fatal("Current must be stable")
				}
				var err error
				last, err = inspectStreamEvent(format, event)
				if err != nil {
					t.Fatal(err)
				}
			}
			if !last || guard.Err() != nil || watch.readAfterTerminal {
				t.Fatalf("last=%v err=%v readAfterTerminal=%v", last, guard.Err(), watch.readAfterTerminal)
			}
		})
	}
}

type conversionErrorOnlyStream struct{ err error }

func (s *conversionErrorOnlyStream) Next() bool                       { return false }
func (s *conversionErrorOnlyStream) Current() *httpclient.StreamEvent { return nil }
func (s *conversionErrorOnlyStream) Err() error                       { return s.err }
func (s *conversionErrorOnlyStream) Close() error                     { return nil }

func TestConvertedClientErrorOnlyOnce(t *testing.T) {
	for _, format := range conversionTestFormats {
		t.Run(conversionTestName(format), func(t *testing.T) {
			failure := errors.New("synthetic interrupted upstream")
			for _, source := range []streams.Stream[*httpclient.StreamEvent]{
				&conversionErrorOnlyStream{err: failure},
				streams.SliceStream([]*httpclient.StreamEvent{streamErrorEvent(format, failure)}),
			} {
				client := &clientErrorStream{source: source, format: format}
				if !client.Next() {
					t.Fatal("missing error event")
				}
				last, err := inspectStreamEvent(format, client.Current())
				if !last || err == nil {
					t.Fatal("not a terminal protocol error")
				}
				if client.Next() || client.Next() {
					t.Fatal("duplicate error or synthetic success")
				}
			}
		})
	}
}

func TestConvertedUsageUnknownIsNotSynthetic(t *testing.T) {
	// mock 故意不提供 usage，即使请求已经开启 include_usage，也不能把占位 1 token 记为真实用量。
	wire := conversionTestStream(llm.APIFormatOpenAIChatCompletion, false, false)
	_, usage, _, err := conversionTestRunWire(t, llm.APIFormatAnthropicMessage, llm.APIFormatOpenAIChatCompletion, conversionTestRequest(llm.APIFormatAnthropicMessage, true, false), false, false, wire)
	if err != nil {
		t.Fatal(err)
	}
	if usage != nil {
		t.Fatalf("unknown upstream usage became synthetic: %+v", usage)
	}
}

func TestConvertedResponseCancellation(t *testing.T) {
	for _, source := range []llm.APIFormat{llm.APIFormatOpenAIChatCompletion, llm.APIFormatAnthropicMessage} {
		t.Run(conversionTestName(source), func(t *testing.T) {
			wire := conversionTestStream(llm.APIFormatOpenAIResponse, false, true)
			wire = strings.ReplaceAll(wire, "response.completed", "response.cancelled")
			wire = strings.ReplaceAll(wire, `"status":"completed"`, `"status":"cancelled"`)
			_, _, _, err := conversionTestRunWire(t, source, llm.APIFormatOpenAIResponse, conversionTestRequest(source, true, false), false, false, wire)
			if err == nil {
				t.Fatal("upstream cancellation became success")
			}
		})
	}
}

func TestConvertedLengthTermination(t *testing.T) {
	for _, source := range conversionTestFormats {
		for _, target := range conversionTestFormats {
			if source == target {
				continue
			}
			for _, streaming := range []bool{false, true} {
				t.Run(conversionTestName(source)+"_to_"+conversionTestName(target)+map[bool]string{false: "/json", true: "/stream"}[streaming], func(t *testing.T) {
					var wire string
					if streaming {
						wire = conversionTestStream(target, false, true)
					} else {
						wire = string(conversionTestJSON(conversionTestResponse(target, false)))
					}
					switch target {
					case llm.APIFormatOpenAIChatCompletion:
						wire = strings.ReplaceAll(wire, `"finish_reason":"stop"`, `"finish_reason":"length"`)
					case llm.APIFormatOpenAIResponse:
						wire = strings.ReplaceAll(wire, "response.completed", "response.incomplete")
						wire = strings.ReplaceAll(wire, `"status":"completed"`, `"status":"incomplete"`)
					case llm.APIFormatAnthropicMessage:
						wire = strings.ReplaceAll(wire, `"stop_reason":"end_turn"`, `"stop_reason":"max_tokens"`)
					}
					body, usage, _, err := conversionTestRunWire(t, source, target, conversionTestRequest(source, streaming, false), false, false, wire)
					if err != nil {
						t.Fatal(err)
					}
					if !strings.Contains(string(body), "Hello back") || usage == nil || usage.CompletionTokens != 4 {
						t.Fatalf("partial response/usage lost: %s %+v", body, usage)
					}
					expected := map[llm.APIFormat]string{llm.APIFormatOpenAIChatCompletion: `"finish_reason":"length"`, llm.APIFormatOpenAIResponse: `"status":"incomplete"`, llm.APIFormatAnthropicMessage: `"stop_reason":"max_tokens"`}[source]
					if !strings.Contains(string(body), expected) {
						t.Fatalf("length reason lost: %s", body)
					}
				})
			}
		}
	}
}

func TestConvertedCacheUsage(t *testing.T) {
	for _, source := range conversionTestFormats {
		for _, target := range conversionTestFormats {
			if source == target {
				continue
			}
			t.Run(conversionTestName(source)+"_to_"+conversionTestName(target), func(t *testing.T) {
				wire := conversionTestStream(target, false, true)
				switch target {
				case llm.APIFormatOpenAIChatCompletion:
					wire = strings.ReplaceAll(wire, `"prompt_tokens":12`, `"prompt_tokens":12,"prompt_tokens_details":{"cached_tokens":8}`)
				case llm.APIFormatOpenAIResponse:
					wire = strings.ReplaceAll(wire, `"input_tokens":12`, `"input_tokens":12,"input_tokens_details":{"cached_tokens":8}`)
				case llm.APIFormatAnthropicMessage:
					wire = strings.ReplaceAll(wire, `"input_tokens":12`, `"input_tokens":3,"cache_read_input_tokens":8,"cache_creation_input_tokens":1`)
				}
				_, usage, _, err := conversionTestRunWire(t, source, target, conversionTestRequest(source, true, false), false, false, wire)
				if err != nil {
					t.Fatal(err)
				}
				if usage == nil || usage.PromptTokens != 12 || usage.CompletionTokens != 4 || usage.PromptTokensDetails == nil || usage.PromptTokensDetails.CachedTokens != 8 {
					t.Fatalf("cache usage lost: %+v", usage)
				}
			})
		}
	}
}
