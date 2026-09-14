package relay

import (
	"bytes"
	"encoding/json"

	"github.com/bestruirui/octopus/internal/model"
)

// stripSignedReasoning 去掉请求体里绑定到上游账号的思维内容, 返回改写后的请求体与是否真的改过。
//
// 思维内容在两家协议里的形状不同, 但性质一致: 它不是客户端自己能生成的东西, 而是某个上游账号签发给它的凭据,
// 只有那个账号认。换账号重发必然被拒, 而 octopus 的故障转移正是要换账号, 于是坏凭据会被反复转发,
// 让分组成员逐个被冷却。去掉凭据这轮请求仍能完成, 代价只是这一轮少了思维连续性——比整个分组转不动小得多。
//
// OpenAI Chat 不在此列: 它的 reasoning_content 是纯文本, 任何上游都收得下, 没有绑定关系, 去掉反而白丢上下文。
func stripSignedReasoning(body []byte, protocol model.Protocol) ([]byte, bool) {
	switch protocol {
	case model.ProtocolOpenAIResponse, model.ProtocolAnthropicMessage:
	default:
		return body, false
	}

	// UseNumber 保留数字字面量: 默认解成 float64 会把大整数写成科学计数法, 请求体不是原样就不该动它。
	decoder := json.NewDecoder(bytes.NewReader(body))
	decoder.UseNumber()
	var payload map[string]any
	if err := decoder.Decode(&payload); err != nil {
		return body, false
	}

	changed := false
	if protocol == model.ProtocolOpenAIResponse {
		changed = stripResponsesReasoning(payload)
	} else {
		changed = stripAnthropicReasoning(payload)
	}
	if !changed {
		return body, false
	}

	// 不转义 HTML: 请求体里出现 < > & 是正文内容, 写成 < 之类虽仍是合法 JSON, 但白白改变了客户端发来的字节。
	var buffer bytes.Buffer
	encoder := json.NewEncoder(&buffer)
	encoder.SetEscapeHTML(false)
	if err := encoder.Encode(payload); err != nil {
		return body, false
	}
	return bytes.TrimRight(buffer.Bytes(), "\n"), true
}

// stripResponsesReasoning 去掉 Responses 请求 input 里的 reasoning 项。
// 整项删除而非只删 encrypted_content: 少了加密内容后它只剩一个别的账号不认识的 id, 留着同样被拒。
func stripResponsesReasoning(payload map[string]any) bool {
	input, ok := payload["input"].([]any)
	if !ok {
		return false
	}

	kept := make([]any, 0, len(input))
	changed := false
	for _, item := range input {
		if entry, ok := item.(map[string]any); ok && entry["type"] == "reasoning" {
			changed = true
			continue
		}
		kept = append(kept, item)
	}
	if !changed {
		return false
	}
	payload["input"] = kept
	return true
}

// stripAnthropicReasoning 去掉 Anthropic 请求 messages 里带签名的 thinking 与 redacted_thinking 块。
// 只删块不删整条消息: 思维块之后通常还跟着正文或工具调用, 那才是这条消息的实质。
// 某条消息若只剩思维块, 删完就空了, 上游会因空 content 报错, 故连消息一并丢弃。
func stripAnthropicReasoning(payload map[string]any) bool {
	messages, ok := payload["messages"].([]any)
	if !ok {
		return false
	}

	kept := make([]any, 0, len(messages))
	changed := false
	for _, message := range messages {
		entry, ok := message.(map[string]any)
		if !ok {
			kept = append(kept, message)
			continue
		}
		blocks, ok := entry["content"].([]any)
		if !ok {
			kept = append(kept, message)
			continue
		}

		remaining := make([]any, 0, len(blocks))
		for _, block := range blocks {
			if item, ok := block.(map[string]any); ok {
				switch item["type"] {
				case "thinking", "redacted_thinking":
					changed = true
					continue
				}
			}
			remaining = append(remaining, block)
		}
		if len(remaining) == 0 {
			changed = true
			continue
		}
		entry["content"] = remaining
		kept = append(kept, entry)
	}
	if !changed {
		return false
	}
	payload["messages"] = kept
	return true
}
