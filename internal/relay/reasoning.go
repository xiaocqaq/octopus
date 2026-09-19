package relay

import (
	"bytes"
	"encoding/json"
	"strings"

	"github.com/bestruirui/octopus/internal/model"
)

// shouldStripReasoning 只在上游错误明确指向思维凭据时重试清洗。
// 任意快速错误都清洗会把限流、参数错误等无关故障误当成凭据问题，导致下一轮请求丢上下文。
func shouldStripReasoning(err error) bool {
	if err == nil {
		return false
	}
	message := strings.ToLower(err.Error())
	if containsAny(message, "encrypted_content", "previous_response_id", "signature") {
		return true
	}
	// 资源归属类报错直接命中: 这类文案里既没有 reasoning 也没有 conversation,
	// 走下面的"主体词 + 否定词"组合会整类漏掉 —— 而它正是历史引用属于另一个上游资源/账号时最先炸的那个。
	if isResourceOwnershipError(message) {
		return true
	}
	rejected := containsAny(message, "invalid", "not found", "unknown", "expired", "verify", "decrypt", "mismatch", "does not exist", "not accessible", "does not belong")
	if strings.Contains(message, "conversation") {
		return rejected
	}
	return strings.Contains(message, "reasoning") && !strings.Contains(message, "reasoning effort") && rejected
}

// ownershipWords 是"归属"这一层意思的措辞, presenceWords 是被引用物的类别。
// 两者都出现才算资源归属错误: 只用 ownershipWords 会把"请换个账号重试"这类也拉进来,
// 只用 presenceWords 又会被配额、限流文案里的 resource 命中。
var (
	ownershipWords = []string{"different", "another", "other"}
	presenceWords  = []string{"resource", "account", "organization", "organisation", "project"}
)

// isResourceOwnershipError 判定"请求里引用的东西属于另一个上游资源/账号"这类报错。
//
// 按"两个词都出现"判定而不是整串匹配: 厂商名会夹在中间,
// 实测报文 "The requested item was created under a different Azure OpenAI resource. ... [trace_id=...]"
// 里 "different" 与 "resource" 之间隔着 "Azure OpenAI", 整串匹配必然漏掉。
// 误判的代价很小: 多一次清洗重试(那次请求少了服务端引用与思维连续性), 之后照常记成员失败;
// 漏判的代价是这条错永远救不回来 —— 所以这一档宁可宽一点。
func isResourceOwnershipError(message string) bool {
	if !containsAny(message, presenceWords...) {
		return false
	}
	return containsAny(message, ownershipWords...)
}

// needsPortabilityScrub 判断这次清洗要不要按"可移植"标准做深一层。
//
// 分组开关只决定常规错误的清洗力度; 资源归属类错误不适用开关: 报错本身说的就是"你引用的东西不属于这个资源",
// 只剥思维凭据救不回来 —— 带归属的与服务端存储引用(previous_response_id, 记录 id, store)必须一起去掉,
// 否则重试必然同样失败, 客户端也躲不掉那个错误(实测会一路重试到超时)。
func needsPortabilityScrub(err error) bool {
	if err == nil {
		return false
	}
	return isResourceOwnershipError(strings.ToLower(err.Error()))
}

func containsAny(value string, candidates ...string) bool {
	for _, candidate := range candidates {
		if strings.Contains(value, candidate) {
			return true
		}
	}
	return false
}

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

// scrubSignedReasoning 在 stripSignedReasoning 的基础上做完整的历史可移植清洗, 供开启思维凭据过滤的分组使用。
//
// 加密思维链绑定签发账号, 同一供应商下并非每个模型都签发; 开启过滤的分组会把请求打到可能换号的上游,
// 于是历史里凡是绑定服务端存储的东西都可能不是本轮那一个账号签发的: reasoning 凭据只是最先炸的一个,
// 记录 id, previous_response_id, compaction 密文同样带着归属。逐次撞一次 400 再剥一样剥不净, 开启过滤后按这份标准一次清完。
// 与脚本版"拒绝转发"不同, octopus 选择全部静默删除: 客户端(Codex)每轮都带全量历史, 删掉这些字段
// 只丢服务端引用与思维连续性, 请求本身仍可完成; 而故障转移场景下拒绝也没有意义——引用在客户端手里, 换成员照样带。
func scrubSignedReasoning(body []byte, protocol model.Protocol) ([]byte, bool) {
	switch protocol {
	case model.ProtocolOpenAIResponse:
	case model.ProtocolAnthropicMessage:
		return stripSignedReasoning(body, protocol) // Anthropic 没有服务端会话引用, 思维块剥离即全部。
	default:
		return body, false
	}

	decoder := json.NewDecoder(bytes.NewReader(body))
	decoder.UseNumber()
	var payload map[string]any
	if err := decoder.Decode(&payload); err != nil {
		return body, false
	}

	changed := stripResponsesReasoning(payload)
	if scrubResponsesPortability(payload) {
		changed = true
	}
	if !changed {
		return body, false
	}

	var buffer bytes.Buffer
	encoder := json.NewEncoder(&buffer)
	encoder.SetEscapeHTML(false)
	if err := encoder.Encode(payload); err != nil {
		return body, false
	}
	return bytes.TrimRight(buffer.Bytes(), "\n"), true
}

// responsesPortableItemTypes 是内容可整体重发的历史条目类型: 它们的顶层 id 只是服务端资源编号,
// 换账号即失效且无配对作用, 删掉由上游重新编号即可。call_id 不在其列——它配对调用与结果, 必须保留。
var responsesPortableItemTypes = map[string]bool{
	"message": true, "function_call": true, "function_call_output": true,
	"custom_tool_call": true, "custom_tool_call_output": true,
}

// scrubResponsesPortability 清掉 Responses 请求体里引用上游服务端存储的字段:
// 服务端会话引用, compaction 与 item_reference 条目, 带 encrypted_content 的条目, 可重发条目的记录 id,
// include 里的加密思维请求, context_management 里的自动压缩, 并强制 store:false。
func scrubResponsesPortability(payload map[string]any) bool {
	changed := false

	// 这两个字段整条指向服务端会话, 值本身没有可重发的内容, 只能删。
	for _, key := range []string{"previous_response_id", "conversation"} {
		if _, ok := payload[key]; ok {
			delete(payload, key)
			changed = true
		}
	}

	// store:true 会让上游把本轮响应存进服务端会话供后续引用; 过滤转发的每一轮账号都可能不同, 存了也没人认。
	if store, ok := payload["store"]; !ok || store != false {
		payload["store"] = false
		changed = true
	}

	if include, ok := payload["include"].([]any); ok {
		kept := make([]any, 0, len(include))
		for _, value := range include {
			if value == "reasoning.encrypted_content" {
				changed = true
				continue
			}
			kept = append(kept, value)
		}
		if changed {
			payload["include"] = kept
		}
	}

	// 自动压缩会把上下文凝成一坨只有签发账号解得开的密文, 声明它的配置项一并删掉。
	if managed, ok := payload["context_management"].([]any); ok {
		kept := make([]any, 0, len(managed))
		for _, item := range managed {
			if entry, ok := item.(map[string]any); ok && entry["type"] == "compaction" {
				changed = true
				continue
			}
			kept = append(kept, item)
		}
		if changed {
			payload["context_management"] = kept
		}
	}

	input, ok := payload["input"].([]any)
	if !ok {
		return changed
	}
	kept := make([]any, 0, len(input))
	for _, item := range input {
		entry, ok := item.(map[string]any)
		if !ok {
			kept = append(kept, item)
			continue
		}
		// compaction 条目与任何带加密上下文的条目: 内容解不开, 留着必被拒, 整条删除。
		if entry["type"] == "compaction" || entry["type"] == "item_reference" {
			changed = true
			continue
		}
		if _, ok := entry["encrypted_content"]; ok {
			changed = true
			continue
		}
		// 顶层 id 只删可整体重发的条目; 其余条目可能本就靠 id 寻址服务器资源, 递归清 id 会删坏配对。
		// 无 type 但带 role 与 content 的是简写消息, 同属可重发。
		typeName, _ := entry["type"].(string)
		_, hasRole := entry["role"]
		_, hasContent := entry["content"]
		portable := responsesPortableItemTypes[typeName] || (typeName == "" && hasRole && hasContent)
		if portable {
			if _, ok := entry["id"]; ok {
				delete(entry, "id")
				changed = true
			}
		}
		kept = append(kept, entry)
	}
	if changed {
		payload["input"] = kept
	}
	return changed
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
