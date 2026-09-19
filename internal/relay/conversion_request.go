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
	if m.format == llm.APIFormatAnthropicMessage && request.ResponseFormat != nil && request.ResponseFormat.Type != "" && request.ResponseFormat.Type != "text" {
		return nil, incompatibleField(m.format, "response_format / text.format")
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
