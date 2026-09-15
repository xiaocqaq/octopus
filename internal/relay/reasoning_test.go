package relay

import (
	"bytes"
	"encoding/json"
	"errors"
	"testing"

	"github.com/bestruirui/octopus/internal/model"
)

func TestShouldStripReasoningOnlyForCredentialErrors(t *testing.T) {
	for _, err := range []error{
		errors.New("responses stream error"),
		errors.New("rate limit exceeded"),
		errors.New("invalid parameter: temperature"),
		errors.New("conversation is too long"),
		errors.New("reasoning effort is not supported"),
	} {
		if shouldStripReasoning(err) {
			t.Errorf("普通上游错误不应触发思维链剥离: %v", err)
		}
	}
	for _, err := range []error{
		errors.New("invalid reasoning encrypted_content"),
		errors.New("previous_response_id is invalid"),
		errors.New("signature verification failed"),
		errors.New("conversation not found"),
		errors.New("reasoning item does not belong to this account"),
	} {
		if !shouldStripReasoning(err) {
			t.Errorf("思维凭据错误应触发一次清洗: %v", err)
		}
	}
}

// responsesBody 构造一个带 reasoning 项的 Responses 请求体。
func responsesBody() []byte {
	return []byte(`{"model":"gpt-6","input":[{"role":"user","content":"hi"},{"type":"reasoning","id":"rs_1","encrypted_content":"gAAAAAB"},{"role":"assistant","content":"hello"}],"include":["reasoning.encrypted_content"]}`)
}

// decodePayload 把请求体解成通用 map, 供断言取值。
func decodePayload(t *testing.T, body []byte) map[string]any {
	t.Helper()
	decoder := json.NewDecoder(bytes.NewReader(body))
	decoder.UseNumber()
	var payload map[string]any
	if err := decoder.Decode(&payload); err != nil {
		t.Fatalf("请求体不是合法 JSON: %v", err)
	}
	return payload
}

// TestStripResponsesReasoning 带加密内容的 reasoning 项应被整项删除, 其余项原样保留。
func TestStripResponsesReasoning(t *testing.T) {
	stripped, ok := stripSignedReasoning(responsesBody(), model.ProtocolOpenAIResponse)
	if !ok {
		t.Fatal("请求体带有 reasoning 项, 应报告已改写")
	}
	payload := decodePayload(t, stripped)
	input, _ := payload["input"].([]any)
	if len(input) != 2 {
		t.Fatalf("reasoning 项应被删除, 期望剩 2 项, 实际 %d 项", len(input))
	}
	for _, item := range input {
		if entry, ok := item.(map[string]any); ok && entry["type"] == "reasoning" {
			t.Fatal("reasoning 项未被删除")
		}
	}
	// include 是向本轮响应索要加密内容的声明, 与请求体里已有的凭据无关, 应保留。
	include, _ := payload["include"].([]any)
	if len(include) != 1 || include[0] != "reasoning.encrypted_content" {
		t.Fatalf("include 不应被改动, 实际 %v", include)
	}
}

// TestStripResponsesReasoningWithoutReasoning 没有 reasoning 项时不应改写请求体。
func TestStripResponsesReasoningWithoutReasoning(t *testing.T) {
	body := []byte(`{"model":"gpt-6","input":[{"role":"user","content":"hi"}]}`)
	stripped, ok := stripSignedReasoning(body, model.ProtocolOpenAIResponse)
	if ok {
		t.Fatal("没有 reasoning 项时不该报告已改写")
	}
	if !bytes.Equal(stripped, body) {
		t.Fatal("没有可去掉的内容时请求体必须原样返回")
	}
}

// TestStripAnthropicThinking 带签名的思维块应被删除, 同一消息的正文与工具调用保留。
func TestStripAnthropicThinking(t *testing.T) {
	body := []byte(`{"model":"claude-sonnet-5","messages":[{"role":"user","content":"hi"},{"role":"assistant","content":[{"type":"thinking","thinking":"...","signature":"sig"},{"type":"tool_use","id":"t1","name":"f","input":{}}]}]}`)
	stripped, ok := stripSignedReasoning(body, model.ProtocolAnthropicMessage)
	if !ok {
		t.Fatal("请求体带有 thinking 块, 应报告已改写")
	}
	payload := decodePayload(t, stripped)
	messages, _ := payload["messages"].([]any)
	if len(messages) != 2 {
		t.Fatalf("两条消息都该留下, 实际 %d 条", len(messages))
	}
	blocks, _ := messages[1].(map[string]any)["content"].([]any)
	if len(blocks) != 1 {
		t.Fatalf("只剩思维块的消息应保留其工具调用, 期望 1 个块, 实际 %d 个", len(blocks))
	}
	if block, _ := blocks[0].(map[string]any); block["type"] != "tool_use" {
		t.Fatalf("保留的应是 tool_use 块, 实际 %v", block["type"])
	}
}

// TestStripAnthropicThinkingOnlyMessage 一条只剩思维块的消息删完就空了, 应连消息一并丢弃。
func TestStripAnthropicThinkingOnlyMessage(t *testing.T) {
	body := []byte(`{"model":"claude-sonnet-5","messages":[{"role":"user","content":"hi"},{"role":"assistant","content":[{"type":"thinking","thinking":"...","signature":"sig"}]}]}`)
	stripped, ok := stripSignedReasoning(body, model.ProtocolAnthropicMessage)
	if !ok {
		t.Fatal("请求体带有 thinking 块, 应报告已改写")
	}
	payload := decodePayload(t, stripped)
	messages, _ := payload["messages"].([]any)
	if len(messages) != 1 {
		t.Fatalf("空消息应被丢弃, 期望剩 1 条, 实际 %d 条", len(messages))
	}
}

// TestStripChatReasoningUntouched Chat 的 reasoning_content 是纯文本, 没有账号绑定, 不该被去掉。
func TestStripChatReasoningUntouched(t *testing.T) {
	body := []byte(`{"model":"gpt-6","messages":[{"role":"assistant","reasoning_content":"thinking","content":"hi"}]}`)
	stripped, ok := stripSignedReasoning(body, model.ProtocolOpenAIChatCompletion)
	if ok {
		t.Fatal("Chat 请求没有绑定账号的思维凭据, 不该报告已改写")
	}
	if !bytes.Equal(stripped, body) {
		t.Fatal("Chat 请求体必须原样返回")
	}
}

// TestStripSignedReasoningPreservesNumbers 数字应逐字保留, 不能被解成浮点再写回科学计数法。
func TestStripSignedReasoningPreservesNumbers(t *testing.T) {
	body := []byte(`{"model":"gpt-6","max_output_tokens":9007199254740993,"input":[{"type":"reasoning","encrypted_content":"gAAAAAB"},{"role":"user","content":"hi"}]}`)
	stripped, ok := stripSignedReasoning(body, model.ProtocolOpenAIResponse)
	if !ok {
		t.Fatal("请求体带有 reasoning 项, 应报告已改写")
	}
	if !bytes.Contains(stripped, []byte("9007199254740993")) {
		t.Fatalf("大整数被改写: %s", stripped)
	}
}

// TestScrubResponsesPortability 开启过滤的清洗要一次清净所有服务端归属: 思维项, 记录 id,
// 会话引用, compaction 与 item_reference, 加密内容, include 声明, 并强制 store:false;
// 工具配对(call_id)与嵌套资源 id 不属于清洗范围, 必须原样保留。
func TestScrubResponsesPortability(t *testing.T) {
	body := []byte(`{"model":"gpt-6","store":true,"previous_response_id":"resp_1","include":["reasoning.encrypted_content","web_search_call.items"],` +
		`"context_management":[{"type":"compaction","compact_threshold":2000},{"type":"other"}],` +
		`"input":[` +
		`{"type":"reasoning","id":"rs_1","encrypted_content":"gAAAA"},` +
		`{"type":"message","id":"msg_1","role":"user","content":"hi"},` +
		`{"role":"assistant","content":[{"type":"output_text","text":"yo"}]},` +
		`{"type":"function_call","id":"fc_1","call_id":"call_1","name":"f","arguments":"{}"},` +
		`{"type":"function_call_output","id":"fco_1","call_id":"call_1","output":"ok"},` +
		`{"type":"item_reference","id":"item_9"},` +
		`{"type":"file_search_call","id":"fsc_1","encrypted_content":"gBBBB"},` +
		`{"type":"custom_tool_call","id":"ctc_1","call_id":"call_2","input":"x"}` +
		`]}`)
	scrubbed, ok := scrubSignedReasoning(body, model.ProtocolOpenAIResponse)
	if !ok {
		t.Fatal("请求体带有需清洗的内容, 应报告已改写")
	}
	payload := decodePayload(t, scrubbed)

	if _, exists := payload["previous_response_id"]; exists {
		t.Fatal("previous_response_id 应被删除")
	}
	if store, _ := payload["store"].(bool); store {
		t.Fatal("store 应被强制为 false")
	}
	include, _ := payload["include"].([]any)
	if len(include) != 1 || include[0] != "web_search_call.items" {
		t.Fatalf("include 应只留下非思维声明, 实际 %v", include)
	}
	managed, _ := payload["context_management"].([]any)
	if len(managed) != 1 {
		t.Fatalf("compaction 配置应被删除, 实际 %v", managed)
	}
	input, _ := payload["input"].([]any)
	if len(input) != 5 {
		t.Fatalf("reasoning/item_reference/encrypted_content 三项应被删除, 期望剩 5 项, 实际 %d 项", len(input))
	}
	for _, item := range input {
		entry, _ := item.(map[string]any)
		typeName, _ := entry["type"].(string)
		if _, exists := entry["id"]; exists && (typeName == "function_call" || typeName == "function_call_output" || typeName == "custom_tool_call") {
			t.Fatalf("%s 的记录 id 应被删除: %v", typeName, entry)
		}
		if _, exists := entry["id"]; exists && typeName == "" && entry["role"] == "assistant" {
			t.Fatalf("简写消息的记录 id 应被删除: %v", entry)
		}
		if callID, _ := entry["call_id"].(string); callID != "" && callID != "call_1" && callID != "call_2" {
			t.Fatalf("call_id 被改写: %v", entry)
		}
	}
	// 工具配对仍在且 call_id 完好。
	var calls int
	for _, item := range input {
		if entry, _ := item.(map[string]any); entry["call_id"] == "call_1" {
			calls++
		}
	}
	if calls != 2 {
		t.Fatalf("function_call 与其输出应都保留, 实际 %d", calls)
	}
}

// TestScrubResponsesMinimalChange 干净请求(仅缺 store)时也要补齐 store:false 并报告已改写。
func TestScrubResponsesMinimalChange(t *testing.T) {
	body := []byte(`{"model":"gpt-6","input":[{"role":"user","content":"hi"}]}`)
	scrubbed, ok := scrubSignedReasoning(body, model.ProtocolOpenAIResponse)
	if !ok {
		t.Fatal("store 缺失时应补上并报告已改写")
	}
	payload := decodePayload(t, scrubbed)
	if store, exists := payload["store"]; !exists || store != false {
		t.Fatalf("store 应为 false, 实际 %v", store)
	}
}

// TestScrubAnthropicEqualsStrip Anthropic 没有服务端会话引用, 完整清洗与思维块剥离同结果。
func TestScrubAnthropicEqualsStrip(t *testing.T) {
	body := []byte(`{"model":"claude-sonnet-5","messages":[{"role":"assistant","content":[{"type":"thinking","thinking":"...","signature":"sig"},{"type":"text","text":"hi"}]}]}`)
	scrubbed, ok := scrubSignedReasoning(body, model.ProtocolAnthropicMessage)
	if !ok {
		t.Fatal("thinking 块应被删除")
	}
	stripped, _ := stripSignedReasoning(body, model.ProtocolAnthropicMessage)
	if !bytes.Equal(scrubbed, stripped) {
		t.Fatalf("Anthropic 清洗应与剥离一致:\n%v\n%v", scrubbed, stripped)
	}
}

// TestScrubChatUntouched Chat 协议没有可清洗的账号绑定字段, 必须原样返回。
func TestScrubChatUntouched(t *testing.T) {
	body := []byte(`{"model":"gpt-6","messages":[{"role":"user","content":"hi"}]}`)
	scrubbed, ok := scrubSignedReasoning(body, model.ProtocolOpenAIChatCompletion)
	if ok || !bytes.Equal(scrubbed, body) {
		t.Fatal("Chat 请求体必须原样返回")
	}
}
