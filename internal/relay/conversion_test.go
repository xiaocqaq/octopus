package relay

// 协议转换回归测试仅连接本地 mock 上游，不使用真实凭据或数据库。
import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/bestruirui/octopus/internal/model"
	"github.com/looplj/axonhub/llm"
	"github.com/looplj/axonhub/llm/httpclient"
	"github.com/looplj/axonhub/llm/transformer"
	"github.com/looplj/axonhub/llm/transformer/anthropic"
	"github.com/looplj/axonhub/llm/transformer/openai"
	"github.com/looplj/axonhub/llm/transformer/openai/responses"
)

type conversionTestMap = map[string]any

var conversionTestFormats = []llm.APIFormat{llm.APIFormatOpenAIChatCompletion, llm.APIFormatOpenAIResponse, llm.APIFormatAnthropicMessage}

func conversionTestJSON(v any) []byte {
	b, err := json.Marshal(v)
	if err != nil {
		panic(err)
	}
	return b
}
func conversionTestName(f llm.APIFormat) string {
	switch f {
	case llm.APIFormatOpenAIChatCompletion:
		return "chat"
	case llm.APIFormatOpenAIResponse:
		return "responses"
	default:
		return "messages"
	}
}
func conversionTestInbound(f llm.APIFormat) transformer.Inbound {
	switch f {
	case llm.APIFormatOpenAIChatCompletion:
		return openai.NewInboundTransformer()
	case llm.APIFormatOpenAIResponse:
		return responses.NewInboundTransformer()
	default:
		return anthropic.NewInboundTransformer()
	}
}
func conversionTestOutbound(t *testing.T, f llm.APIFormat, base string) transformer.Outbound {
	t.Helper()
	var p model.Protocol
	switch f {
	case llm.APIFormatOpenAIChatCompletion:
		p = model.ProtocolOpenAIChatCompletion
	case llm.APIFormatOpenAIResponse:
		p = model.ProtocolOpenAIResponse
	default:
		p = model.ProtocolAnthropicMessage
	}
	out, _, _, err := buildOutbound(model.Channel{ChannelConfig: model.ChannelConfig{BaseURL: base}}, model.ChannelGrant{Protocols: p}, model.ChannelKey{ChannelKeyConfig: model.ChannelKeyConfig{Key: "local-mock-only"}}, p)
	if err != nil {
		t.Fatal(err)
	}
	return out
}
func conversionTestRequest(f llm.APIFormat, stream, history bool) conversionTestMap {
	r := conversionTestMap{"model": "conversionTest-model", "stream": stream}
	schema := conversionTestMap{"type": "object", "properties": conversionTestMap{"city": conversionTestMap{"type": "string"}}, "required": []string{"city"}}
	switch f {
	case llm.APIFormatOpenAIChatCompletion:
		r["max_tokens"] = 128
		r["messages"] = []any{conversionTestMap{"role": "system", "content": "Be concise."}, conversionTestMap{"role": "user", "content": "Hello"}}
		if stream {
			r["stream_options"] = conversionTestMap{"include_usage": true}
		}
		if history {
			r["tools"] = []any{conversionTestMap{"type": "function", "function": conversionTestMap{"name": "weather", "description": "Get weather", "parameters": schema}}}
			r["messages"] = append(r["messages"].([]any), conversionTestMap{"role": "assistant", "content": nil, "tool_calls": []any{conversionTestMap{"id": "call_old", "type": "function", "function": conversionTestMap{"name": "weather", "arguments": "{\"city\":\"Tokyo\"}"}}}}, conversionTestMap{"role": "tool", "tool_call_id": "call_old", "content": "Sunny"}, conversionTestMap{"role": "user", "content": "What about Paris?"})
		}
	case llm.APIFormatOpenAIResponse:
		r["max_output_tokens"] = 128
		r["instructions"] = "Be concise."
		r["input"] = []any{conversionTestMap{"role": "user", "content": "Hello"}}
		if history {
			r["tools"] = []any{conversionTestMap{"type": "function", "name": "weather", "description": "Get weather", "parameters": schema}}
			r["input"] = append(r["input"].([]any), conversionTestMap{"type": "function_call", "call_id": "call_old", "name": "weather", "arguments": "{\"city\":\"Tokyo\"}"}, conversionTestMap{"type": "function_call_output", "call_id": "call_old", "output": "Sunny"}, conversionTestMap{"role": "user", "content": "What about Paris?"})
		}
	default:
		r["max_tokens"] = 128
		r["system"] = "Be concise."
		r["messages"] = []any{conversionTestMap{"role": "user", "content": "Hello"}}
		if history {
			r["tools"] = []any{conversionTestMap{"name": "weather", "description": "Get weather", "input_schema": schema}}
			r["messages"] = append(r["messages"].([]any), conversionTestMap{"role": "assistant", "content": []any{conversionTestMap{"type": "tool_use", "id": "call_old", "name": "weather", "input": conversionTestMap{"city": "Tokyo"}}}}, conversionTestMap{"role": "user", "content": []any{conversionTestMap{"type": "tool_result", "tool_use_id": "call_old", "content": "Sunny"}, conversionTestMap{"type": "text", "text": "What about Paris?"}}})
		}
	}
	return r
}
func conversionTestResponse(f llm.APIFormat, tool bool) conversionTestMap {
	switch f {
	case llm.APIFormatOpenAIChatCompletion:
		msg := conversionTestMap{"role": "assistant", "content": "Hello back"}
		finish := "stop"
		if tool {
			msg = conversionTestMap{"role": "assistant", "content": nil, "tool_calls": []any{conversionTestMap{"id": "call_new", "type": "function", "function": conversionTestMap{"name": "weather", "arguments": "{\"city\":\"Paris\"}"}}}}
			finish = "tool_calls"
		}
		return conversionTestMap{"id": "chatcmpl_mock", "object": "chat.completion", "created": 1789784000, "model": "conversionTest-model", "choices": []any{conversionTestMap{"index": 0, "message": msg, "finish_reason": finish}}, "usage": conversionTestMap{"prompt_tokens": 12, "completion_tokens": 4, "total_tokens": 16}}
	case llm.APIFormatOpenAIResponse:
		item := conversionTestMap{"id": "msg_mock", "type": "message", "role": "assistant", "status": "completed", "content": []any{conversionTestMap{"type": "output_text", "text": "Hello back", "annotations": []any{}}}}
		if tool {
			item = conversionTestMap{"id": "fc_mock", "type": "function_call", "call_id": "call_new", "name": "weather", "arguments": "{\"city\":\"Paris\"}", "status": "completed"}
		}
		return conversionTestMap{"id": "resp_mock", "object": "response", "created_at": 1789784000, "model": "conversionTest-model", "status": "completed", "output": []any{item}, "usage": conversionTestMap{"input_tokens": 12, "output_tokens": 4, "total_tokens": 16}}
	default:
		block := conversionTestMap{"type": "text", "text": "Hello back"}
		stop := "end_turn"
		if tool {
			block = conversionTestMap{"type": "tool_use", "id": "call_new", "name": "weather", "input": conversionTestMap{"city": "Paris"}}
			stop = "tool_use"
		}
		return conversionTestMap{"id": "msg_mock", "type": "message", "role": "assistant", "model": "conversionTest-model", "content": []any{block}, "stop_reason": stop, "stop_sequence": nil, "usage": conversionTestMap{"input_tokens": 12, "output_tokens": 4}}
	}
}
func conversionTestStream(f llm.APIFormat, tool, includeUsage bool) string {
	var b strings.Builder
	emit := func(event string, v any) {
		if event != "" {
			fmt.Fprintf(&b, "event: %s\n", event)
		}
		fmt.Fprintf(&b, "data: %s\n\n", conversionTestJSON(v))
	}
	finish := conversionTestResponse(f, tool)
	switch f {
	case llm.APIFormatOpenAIChatCompletion:
		chunk := func(delta any, reason any) {
			emit("", conversionTestMap{"id": "chatcmpl_mock", "object": "chat.completion.chunk", "created": 1789784000, "model": "conversionTest-model", "choices": []any{conversionTestMap{"index": 0, "delta": delta, "finish_reason": reason}}})
		}
		chunk(conversionTestMap{"role": "assistant", "content": ""}, nil)
		if tool {
			chunk(conversionTestMap{"tool_calls": []any{conversionTestMap{"index": 0, "id": "call_new", "type": "function", "function": conversionTestMap{"name": "weather", "arguments": ""}}}}, nil)
			for _, args := range []string{"{\"city\":", "\"Paris\"}"} {
				chunk(conversionTestMap{"tool_calls": []any{conversionTestMap{"index": 0, "function": conversionTestMap{"arguments": args}}}}, nil)
			}
			chunk(conversionTestMap{}, "tool_calls")
		} else {
			chunk(conversionTestMap{"content": "Hello "}, nil)
			chunk(conversionTestMap{"content": "back"}, nil)
			chunk(conversionTestMap{}, "stop")
		}
		if includeUsage {
			emit("", conversionTestMap{"id": "chatcmpl_mock", "object": "chat.completion.chunk", "created": 1789784000, "model": "conversionTest-model", "choices": []any{}, "usage": finish["usage"]})
		}
		b.WriteString("data: [DONE]\n\n")
	case llm.APIFormatOpenAIResponse:
		seq := 0
		ev := func(kind string, v conversionTestMap) {
			v["type"] = kind
			v["sequence_number"] = seq
			seq++
			emit(kind, v)
		}
		ev("response.created", conversionTestMap{"response": conversionTestMap{"id": "resp_mock", "object": "response", "created_at": 1789784000, "status": "in_progress", "model": "conversionTest-model", "output": []any{}}})
		item := finish["output"].([]any)[0].(conversionTestMap)
		if tool {
			ev("response.output_item.added", conversionTestMap{"output_index": 0, "item": conversionTestMap{"id": "fc_mock", "type": "function_call", "call_id": "call_new", "name": "weather", "arguments": "", "status": "in_progress"}})
			for _, args := range []string{"{\"city\":", "\"Paris\"}"} {
				ev("response.function_call_arguments.delta", conversionTestMap{"output_index": 0, "item_id": "fc_mock", "delta": args})
			}
			ev("response.function_call_arguments.done", conversionTestMap{"output_index": 0, "item_id": "fc_mock", "arguments": "{\"city\":\"Paris\"}"})
		} else {
			ev("response.output_item.added", conversionTestMap{"output_index": 0, "item": conversionTestMap{"id": "msg_mock", "type": "message", "role": "assistant", "status": "in_progress", "content": []any{}}})
			ev("response.content_part.added", conversionTestMap{"output_index": 0, "item_id": "msg_mock", "content_index": 0, "part": conversionTestMap{"type": "output_text", "text": "", "annotations": []any{}}})
			for _, text := range []string{"Hello ", "back"} {
				ev("response.output_text.delta", conversionTestMap{"output_index": 0, "item_id": "msg_mock", "content_index": 0, "delta": text})
			}
			ev("response.output_text.done", conversionTestMap{"output_index": 0, "item_id": "msg_mock", "content_index": 0, "text": "Hello back"})
			ev("response.content_part.done", conversionTestMap{"output_index": 0, "item_id": "msg_mock", "content_index": 0, "part": item["content"].([]any)[0]})
		}
		ev("response.output_item.done", conversionTestMap{"output_index": 0, "item": item})
		ev("response.completed", conversionTestMap{"response": finish})
	default:
		ev := func(kind string, v conversionTestMap) { v["type"] = kind; emit(kind, v) }
		ev("message_start", conversionTestMap{"message": conversionTestMap{"id": "msg_mock", "type": "message", "role": "assistant", "model": "conversionTest-model", "content": []any{}, "stop_reason": nil, "usage": conversionTestMap{"input_tokens": 12, "output_tokens": 0}}})
		if tool {
			ev("content_block_start", conversionTestMap{"index": 0, "content_block": conversionTestMap{"type": "tool_use", "id": "call_new", "name": "weather", "input": conversionTestMap{}}})
			for _, args := range []string{"{\"city\":", "\"Paris\"}"} {
				ev("content_block_delta", conversionTestMap{"index": 0, "delta": conversionTestMap{"type": "input_json_delta", "partial_json": args}})
			}
		} else {
			ev("content_block_start", conversionTestMap{"index": 0, "content_block": conversionTestMap{"type": "text", "text": ""}})
			for _, text := range []string{"Hello ", "back"} {
				ev("content_block_delta", conversionTestMap{"index": 0, "delta": conversionTestMap{"type": "text_delta", "text": text}})
			}
		}
		ev("content_block_stop", conversionTestMap{"index": 0})
		ev("message_delta", conversionTestMap{"delta": conversionTestMap{"stop_reason": finish["stop_reason"], "stop_sequence": nil}, "usage": conversionTestMap{"output_tokens": 4}})
		ev("message_stop", conversionTestMap{})
	}
	return b.String()
}
func conversionTestRun(t *testing.T, source, target llm.APIFormat, req conversionTestMap, tool, honorUsage bool) ([]byte, *llm.Usage, []byte, error) {
	return conversionTestRunWire(t, source, target, req, tool, honorUsage, "")
}
func conversionTestRunWire(t *testing.T, source, target llm.APIFormat, req conversionTestMap, tool, honorUsage bool, wire string) ([]byte, *llm.Usage, []byte, error) {
	t.Helper()
	var sent []byte
	streaming, _ := req["stream"].(bool)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		sent, _ = io.ReadAll(r.Body)
		if streaming {
			include := true
			if honorUsage && target == llm.APIFormatOpenAIChatCompletion {
				var p struct {
					Options struct {
						Usage bool `json:"include_usage"`
					} `json:"stream_options"`
				}
				json.Unmarshal(sent, &p)
				include = p.Options.Usage
			}
			w.Header().Set("Content-Type", "text/event-stream")
			if wire != "" {
				io.WriteString(w, wire)
			} else {
				io.WriteString(w, conversionTestStream(target, tool, include))
			}
		} else {
			w.Header().Set("Content-Type", "application/json")
			if wire != "" {
				io.WriteString(w, wire)
			} else {
				w.Write(conversionTestJSON(conversionTestResponse(target, tool)))
			}
		}
	}))
	defer server.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	raw := &httpclient.Request{Method: "POST", Headers: http.Header{"Content-Type": []string{"application/json"}}, Body: conversionTestJSON(req)}
	result, err := sendConverted(ctx, source, raw, model.Channel{}, conversionTestOutbound(t, target, server.URL), streaming)
	if err != nil {
		if result != nil {
			return nil, result.usage, sent, err
		}
		return nil, nil, sent, err
	}
	if !streaming {
		return result.body, result.usage, sent, nil
	}
	defer result.events.Close()
	if result.closeIdle != nil {
		defer result.closeIdle()
	}
	events := []*httpclient.StreamEvent{result.first}
	last := result.last
	for !last && result.events.Next() {
		ev := result.events.Current()
		events = append(events, ev)
		last, err = inspectStreamEvent(source, ev)
		if err != nil {
			return nil, result.streamUsage(), sent, err
		}
	}
	if err = result.events.Err(); err != nil {
		return nil, nil, sent, err
	}
	if !last {
		return nil, nil, sent, fmt.Errorf("missing terminal event")
	}
	body, meta, err := conversionTestInbound(source).AggregateStreamChunks(ctx, events)
	if err != nil {
		return nil, nil, sent, err
	}
	// 同时校验客户端聚合用量及实际计量入口；有用量时二者都必须正确。
	upstreamUsage := result.streamUsage()
	if upstreamUsage != nil && (meta.Usage == nil || meta.Usage.PromptTokens != upstreamUsage.PromptTokens || meta.Usage.CompletionTokens != upstreamUsage.CompletionTokens) {
		t.Errorf("client usage=%+v differs from upstream usage=%+v", meta.Usage, upstreamUsage)
	}
	return body, upstreamUsage, sent, nil
}
func TestConversionMatrix(t *testing.T) {
	for _, src := range conversionTestFormats {
		for _, dst := range conversionTestFormats {
			if src == dst {
				continue
			}
			for _, stream := range []bool{false, true} {
				for _, tool := range []bool{false, true} {
					t.Run(fmt.Sprintf("%s_to_%s/stream_%t/tool_%t", conversionTestName(src), conversionTestName(dst), stream, tool), func(t *testing.T) {
						body, usage, sent, err := conversionTestRun(t, src, dst, conversionTestRequest(src, stream, tool), tool, false)
						if err != nil {
							t.Fatal(err)
						}
						parsed, err := conversionTestOutbound(t, src, "http://127.0.0.1").TransformResponse(context.Background(), &httpclient.Response{StatusCode: 200, Headers: http.Header{}, Body: body})
						if err != nil {
							t.Fatal(err)
						}
						if len(parsed.Choices) == 0 {
							t.Fatalf("no choices: %s", body)
						}
						msg := parsed.Choices[0].Message
						if tool {
							if len(msg.ToolCalls) != 1 || msg.ToolCalls[0].ID != "call_new" || msg.ToolCalls[0].Function.Name != "weather" {
								t.Fatalf("bad tool: %s", body)
							}
							var args conversionTestMap
							if json.Unmarshal([]byte(msg.ToolCalls[0].Function.Arguments), &args) != nil || args["city"] != "Paris" {
								t.Fatalf("bad args: %s", body)
							}
							unified, e := conversionTestInbound(dst).TransformRequest(context.Background(), &httpclient.Request{Headers: http.Header{"Content-Type": []string{"application/json"}}, Body: sent})
							if e != nil {
								t.Fatal(e)
							}
							encoded := string(conversionTestJSON(unified))
							for _, marker := range []string{"call_old", "Tokyo", "Sunny", "Paris"} {
								if !strings.Contains(encoded, marker) {
									t.Errorf("lost history %q: %s", marker, sent)
								}
							}
							if len(unified.Tools) != 1 {
								t.Errorf("lost tools: %s", sent)
							}
						} else if !strings.Contains(string(body), "Hello back") {
							t.Fatalf("lost text: %s", body)
						}
						if usage == nil || usage.PromptTokens != 12 || usage.CompletionTokens != 4 {
							t.Errorf("usage mismatch: %+v; response=%s", usage, body)
						}
					})
				}
			}
		}
	}
}
func TestConversionChatUsageNegotiation(t *testing.T) {
	for _, src := range []llm.APIFormat{llm.APIFormatOpenAIResponse, llm.APIFormatAnthropicMessage} {
		t.Run(conversionTestName(src), func(t *testing.T) {
			body, usage, sent, err := conversionTestRun(t, src, llm.APIFormatOpenAIChatCompletion, conversionTestRequest(src, true, false), false, true)
			if err != nil {
				t.Fatal(err)
			}
			t.Logf("outbound=%s; usage=%+v; response=%s", sent, usage, body)
			if usage == nil || usage.PromptTokens != 12 || usage.CompletionTokens != 4 {
				t.Errorf("cross-protocol Chat SSE must request include_usage")
			}
		})
	}
}
func TestConversionPreviousResponseContext(t *testing.T) {
	for _, dst := range []llm.APIFormat{llm.APIFormatOpenAIChatCompletion, llm.APIFormatAnthropicMessage} {
		t.Run(conversionTestName(dst), func(t *testing.T) {
			req := conversionTestRequest(llm.APIFormatOpenAIResponse, false, false)
			req["previous_response_id"] = "resp_context"
			req["input"] = "Continue the previous answer"
			_, _, sent, err := conversionTestRun(t, llm.APIFormatOpenAIResponse, dst, req, false, false)
			t.Logf("outbound=%s; err=%v", sent, err)
			if conversionClientError(err) == nil || len(sent) != 0 {
				t.Errorf("unsupported previous_response_id must be rejected locally: err=%v outbound=%s", err, sent)
			}
		})
	}
}
func TestConversionStructuredOutputToMessages(t *testing.T) {
	for _, src := range []llm.APIFormat{llm.APIFormatOpenAIChatCompletion, llm.APIFormatOpenAIResponse} {
		t.Run(conversionTestName(src), func(t *testing.T) {
			req := conversionTestRequest(src, false, false)
			schema := conversionTestMap{"type": "object", "properties": conversionTestMap{"ok": conversionTestMap{"type": "boolean"}}, "required": []string{"ok"}, "additionalProperties": false}
			if src == llm.APIFormatOpenAIChatCompletion {
				req["response_format"] = conversionTestMap{"type": "json_schema", "json_schema": conversionTestMap{"name": "result", "strict": true, "schema": schema}}
			} else {
				req["text"] = conversionTestMap{"format": conversionTestMap{"type": "json_schema", "name": "result", "strict": true, "schema": schema}}
			}
			_, _, sent, err := conversionTestRun(t, src, llm.APIFormatAnthropicMessage, req, false, false)
			t.Logf("outbound=%s; err=%v", sent, err)
			if conversionClientError(err) == nil || len(sent) != 0 {
				t.Errorf("unsupported structured output must be rejected locally: err=%v outbound=%s", err, sent)
			}
		})
	}
}
func TestConversionNativeToolToChat(t *testing.T) {
	req := conversionTestRequest(llm.APIFormatOpenAIResponse, false, false)
	req["tools"] = []any{
		conversionTestMap{"type": "web_search"},
		conversionTestMap{"type": "function", "name": "weather", "parameters": conversionTestMap{"type": "object"}},
		conversionTestMap{"type": "custom", "name": "run_code", "description": "Execute code", "format": conversionTestMap{"type": "text"}},
	}
	_, _, sent, err := conversionTestRun(t, llm.APIFormatOpenAIResponse, llm.APIFormatOpenAIChatCompletion, req, false, false)
	if err != nil {
		t.Fatal(err)
	}
	body := string(sent)
	if strings.Contains(body, "web_search") || strings.Contains(body, "run_code") || strings.Contains(body, `"custom"`) {
		t.Fatalf("axonhub should drop native tools chat cannot represent: %s", body)
	}
	if !strings.Contains(body, "weather") {
		t.Fatalf("function tool was dropped with native tools: %s", body)
	}
}

func TestConversionResponsesFailedStream(t *testing.T) {
	for _, src := range []llm.APIFormat{llm.APIFormatOpenAIChatCompletion, llm.APIFormatAnthropicMessage} {
		t.Run(conversionTestName(src), func(t *testing.T) {
			wire := conversionTestStream(llm.APIFormatOpenAIResponse, false, true)
			pos := strings.Index(wire, "event: response.completed")
			wire = wire[:pos] + "event: response.failed\ndata: " + string(conversionTestJSON(conversionTestMap{"type": "response.failed", "sequence_number": 99, "response": conversionTestMap{"id": "resp_mock", "object": "response", "status": "failed", "model": "conversionTest-model", "output": []any{}, "error": conversionTestMap{"code": "server_error", "message": "mock failed after partial output"}, "usage": conversionTestMap{"input_tokens": 12, "output_tokens": 4, "total_tokens": 16}}})) + "\n\n"
			body, usage, _, err := conversionTestRunWire(t, src, llm.APIFormatOpenAIResponse, conversionTestRequest(src, true, false), false, false, wire)
			t.Logf("response=%s err=%v", body, err)
			if usage == nil || usage.PromptTokens != 12 || usage.CompletionTokens != 4 {
				t.Errorf("failure usage lost: %+v", usage)
			}
			if err == nil {
				t.Error("Responses failed terminal was not classified as failure by relay")
			}
		})
	}
}
func TestConversionResponsesMaxTokens(t *testing.T) {
	for _, src := range []llm.APIFormat{llm.APIFormatOpenAIChatCompletion, llm.APIFormatAnthropicMessage} {
		t.Run(conversionTestName(src), func(t *testing.T) {
			resp := conversionTestResponse(llm.APIFormatOpenAIResponse, false)
			resp["status"] = "incomplete"
			resp["incomplete_details"] = conversionTestMap{"reason": "max_output_tokens"}
			body, _, _, err := conversionTestRunWire(t, src, llm.APIFormatOpenAIResponse, conversionTestRequest(src, false, false), false, false, string(conversionTestJSON(resp)))
			t.Logf("response=%s err=%v", body, err)
			if err != nil {
				t.Errorf("valid token-limited partial response discarded: %v", err)
			}
		})
	}
}
func TestConversionTruncatedTransport(t *testing.T) {
	for _, src := range conversionTestFormats {
		for _, dst := range conversionTestFormats {
			if src == dst {
				continue
			}
			t.Run(conversionTestName(src)+"_to_"+conversionTestName(dst), func(t *testing.T) {
				wire := conversionTestStream(dst, false, true)
				marker := ""
				switch dst {
				case llm.APIFormatOpenAIChatCompletion:
					marker = "data: [DONE]"
				case llm.APIFormatOpenAIResponse:
					marker = "event: response.completed"
				default:
					marker = "event: message_stop"
				}
				wire = wire[:strings.Index(wire, marker)]
				body, usage, _, err := conversionTestRunWire(t, src, dst, conversionTestRequest(src, true, false), false, false, wire)
				t.Logf("response=%s err=%v", body, err)
				if dst != llm.APIFormatOpenAIResponse && (usage == nil || usage.PromptTokens != 12 || usage.CompletionTokens != 4) {
					t.Errorf("confirmed usage lost at incomplete terminal tail: %+v", usage)
				}
				if err == nil {
					t.Errorf("upstream SSE missing terminal accepted as normal")
				}
			})
		}
	}
}

func TestConversionEarlyEOF(t *testing.T) {
	for _, src := range conversionTestFormats {
		for _, dst := range conversionTestFormats {
			if src == dst {
				continue
			}
			t.Run(conversionTestName(src)+"_to_"+conversionTestName(dst), func(t *testing.T) {
				wire := conversionTestStream(dst, false, true)
				marker := ""
				switch dst {
				case llm.APIFormatOpenAIChatCompletion:
					marker = `"content":"back"`
				case llm.APIFormatOpenAIResponse:
					marker = `"delta":"back"`
				default:
					marker = `"text":"back"`
				}
				pos := strings.Index(wire, marker)
				if pos < 0 {
					t.Fatal("bad fixture")
				}
				end := pos + strings.Index(wire[pos:], "\n\n") + 2
				wire = wire[:end]
				body, _, _, err := conversionTestRunWire(t, src, dst, conversionTestRequest(src, true, false), false, false, wire)
				t.Logf("response=%s err=%v", body, err)
				if err == nil {
					t.Error("partial SSE without finish reason or terminal accepted as normal")
				}
			})
		}
	}
}
func TestConversionExplicitStreamError(t *testing.T) {
	for _, src := range conversionTestFormats {
		for _, dst := range conversionTestFormats {
			if src == dst {
				continue
			}
			t.Run(conversionTestName(src)+"_to_"+conversionTestName(dst), func(t *testing.T) {
				wire := conversionTestStream(dst, false, true)
				marker := ""
				switch dst {
				case llm.APIFormatOpenAIChatCompletion:
					marker = `"content":"back"`
				case llm.APIFormatOpenAIResponse:
					marker = `"delta":"back"`
				default:
					marker = `"text":"back"`
				}
				pos := strings.Index(wire, marker)
				end := pos + strings.Index(wire[pos:], "\n\n") + 2
				wire = wire[:end]
				wire += "event: error\ndata: " + string(conversionTestJSON(conversionTestMap{"type": "error", "error": conversionTestMap{"type": "api_error", "code": "server_error", "message": "synthetic upstream error"}, "message": "synthetic upstream error", "code": "server_error"})) + "\n\n"
				_, _, _, err := conversionTestRunWire(t, src, dst, conversionTestRequest(src, true, false), false, false, wire)
				if err == nil {
					t.Error("explicit SSE error lost")
				}
			})
		}
	}
}
