package relay

import (
	"bytes"
	"encoding/json"
	"testing"

	"github.com/bestruirui/octopus/internal/model"
)

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
