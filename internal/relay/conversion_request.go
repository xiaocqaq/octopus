package relay

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"

	"github.com/looplj/axonhub/llm"
	"github.com/looplj/axonhub/llm/httpclient"
	"github.com/looplj/axonhub/llm/transformer"
)

// conversionRequestError 是本次请求与目标协议不兼容，不代表渠道不健康。
// handler 必须将其直接返回客户端，不能沿用上游故障的重试/冷却路径。
type conversionRequestError struct {
	param   string
	message string
}

func (e *conversionRequestError) Error() string { return e.message }

func incompatibleField(format llm.APIFormat, param string) error {
	return &conversionRequestError{param: param, message: fmt.Sprintf("cannot convert %s to %s without losing its semantics; use a compatible same-protocol channel or send a self-contained supported request", param, format)}
}

const anthropicJSONModeInstruction = "Return only one valid JSON object. Do not wrap it in Markdown fences or add any text before or after the JSON object."

func normalizeAnthropicResponseFormat(request *llm.Request) error {
	responseFormat := request.ResponseFormat
	if responseFormat == nil || responseFormat.Type == "" || responseFormat.Type == "text" {
		return nil
	}
	if responseFormat.Type != "json_object" {
		return incompatibleField(llm.APIFormatAnthropicMessage, "response_format / text.format")
	}

	instruction := anthropicJSONModeInstruction
	request.Messages = append([]llm.Message{{
		Role: "system",
		Content: llm.MessageContent{Content: &instruction},
	}}, request.Messages...)
	request.ResponseFormat = nil
	return nil
}

// filterIncompatibleTools 删除请求中目标格式不支持的可选工具。
// tool_type function, web_search, image_generation 等是可转换的：
//   - function：所有格式都支持，可转换
//   - web_search：仅 Responses 与 Anthropic 支持，若目标为 Chat 则过滤
//   - image_generation：Responses 支持，Chat 不支持，Anthropic 不直接支持，过滤
//
// 若遇到 custom 等不可转换的工具，不能过滤，必须报错。
// 返回 true 表示过滤成功，false 表示遇到不可转换工具（调用方需报错）
func filterIncompatibleTools(request *llm.Request, targetFormat llm.APIFormat) bool {
	if len(request.Tools) == 0 {
		return true
	}
	filtered := make([]llm.Tool, 0, len(request.Tools))
	for _, tool := range request.Tools {
		switch targetFormat {
		case llm.APIFormatOpenAIChatCompletion:
			// ChatCompletion 只支持 function 类型的工具
			switch tool.Type {
			case llm.ToolTypeFunction, "":
				// function 和空类型（无工具）可以保留
				filtered = append(filtered, tool)
			case llm.ToolTypeWebSearch, llm.ToolTypeImageGeneration:
				// web_search 和 image_generation 在 Chat 不支持，过滤
				continue
			default:
				// custom, google_* 等不可转换，直接返回 false 触发报错
				return false
			}
		case llm.APIFormatAnthropicMessage:
			// Anthropic 支持 function 与 web_search
			switch tool.Type {
			case llm.ToolTypeFunction, llm.ToolTypeWebSearch:
				filtered = append(filtered, tool)
			default:
				// 不支持的工具类型（如 image_generation 等），过滤而不报错
				continue
			}
		case llm.APIFormatOpenAIResponse:
			// Responses API 支持 function, web_search, image_generation, custom 等
			// 保留所有已知类型
			filtered = append(filtered, tool)
		}
	}
	request.Tools = filtered
	return true
}

// validateConversionReferences 拦住统一请求模型没有承载的 Responses 服务端引用。
// 同协议透传不经过这里；代理没有存储上游会话，不能把引用删掉后假装完成了转换。
func validateConversionReferences(source, target llm.APIFormat, raw *httpclient.Request) error {
	if source != llm.APIFormatOpenAIResponse || source == target {
		return nil
	}
	var payload struct {
		Conversation json.RawMessage `json:"conversation"`
		Input        json.RawMessage `json:"input"`
	}
	if err := json.Unmarshal(raw.Body, &payload); err != nil {
		return fmt.Errorf("%w: %v", transformer.ErrInvalidRequest, err)
	}
	if len(payload.Conversation) > 0 && string(payload.Conversation) != "null" {
		return incompatibleField(target, "conversation")
	}
	var items []struct {
		Type string `json:"type"`
	}
	if json.Unmarshal(payload.Input, &items) == nil {
		for _, item := range items {
			if item.Type == "item_reference" {
				return incompatibleField(target, "input.item_reference")
			}
		}
	}
	return nil
}

func (m *conversionMiddleware) OnInboundLlmRequest(_ context.Context, request *llm.Request) (*llm.Request, error) {
	if request == nil {
		return nil, fmt.Errorf("%w: request is empty", transformer.ErrInvalidRequest)
	}
	if m.format != llm.APIFormatOpenAIResponse && request.PreviousResponseID != nil && *request.PreviousResponseID != "" {
		return nil, incompatibleField(m.format, "previous_response_id")
	}

	// 尝试过滤不兼容的工具
	if !filterIncompatibleTools(request, m.format) {
		// 遇到不可转换的工具类型（如 custom），报错
		for _, tool := range request.Tools {
			switch m.format {
			case llm.APIFormatOpenAIChatCompletion:
				// custom, image_generation 等在 Chat 不支持
				if tool.Type != llm.ToolTypeFunction && tool.Type != "" {
					return nil, incompatibleField(m.format, "tools ("+tool.Type+")")
				}
			case llm.APIFormatAnthropicMessage:
				// image_generation 等在 Anthropic 不支持
				if tool.Type != llm.ToolTypeFunction && tool.Type != llm.ToolTypeWebSearch {
					return nil, incompatibleField(m.format, "tools ("+tool.Type+")")
				}
			}
		}
	}

	if m.format == llm.APIFormatAnthropicMessage {
		if err := normalizeAnthropicResponseFormat(request); err != nil {
			return nil, err
		}
	}
	for _, tool := range request.Tools {
		supported := true
		switch m.format {
		case llm.APIFormatOpenAIChatCompletion:
			supported = tool.Type == llm.ToolTypeFunction
		case llm.APIFormatAnthropicMessage:
			supported = tool.Type == llm.ToolTypeFunction || tool.Type == llm.ToolTypeWebSearch
		}
		if !supported {
			return nil, incompatibleField(m.format, "tools ("+tool.Type+")")
		}
	}
	return request, nil
}

// conversionClientError 只分类本地能力/请求解析错误；上游返回的 HTTP 400 仍由现有策略处理。
func conversionClientError(err error) *llm.ResponseError {
	var incompatible *conversionRequestError
	if errors.As(err, &incompatible) {
		return &llm.ResponseError{StatusCode: http.StatusBadRequest, Detail: llm.ErrorDetail{Type: "invalid_request_error", Code: "unsupported_protocol_conversion", Param: incompatible.param, Message: incompatible.message}}
	}
	if errors.Is(err, transformer.ErrInvalidRequest) {
		return &llm.ResponseError{StatusCode: http.StatusBadRequest, Detail: llm.ErrorDetail{Type: "invalid_request_error", Message: err.Error()}}
	}
	return nil
}