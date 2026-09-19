package relay

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/looplj/axonhub/llm"
	"github.com/looplj/axonhub/llm/httpclient"
	"github.com/looplj/axonhub/llm/streams"
	"github.com/looplj/axonhub/llm/transformer/anthropic"
	"github.com/looplj/axonhub/llm/transformer/openai"
	"github.com/looplj/axonhub/llm/transformer/openai/responses"
)

// validatedUpstreamStream 在转换前确认上游协议终态。不能依赖转换后的 DONE：
// 某些转换器会在正常 HTTP EOF 时补结束事件，或把失败 finish_reason 映射成 end_turn。
// 收到真实终态后停止读取，不要求上游同时关闭 HTTP 连接。
type validatedUpstreamStream struct {
	source   streams.Stream[*httpclient.StreamEvent]
	format   llm.APIFormat
	observe  func(*httpclient.StreamEvent)
	current  *httpclient.StreamEvent
	pending  []*httpclient.StreamEvent
	terminal bool
	err      error
}

func (s *validatedUpstreamStream) Next() bool {
	if s.err != nil {
		return false
	}
	if len(s.pending) > 0 {
		s.current, s.pending = s.pending[0], s.pending[1:]
		return true
	}
	if s.terminal {
		return false
	}
	event := s.readEvent()
	if event == nil {
		return false
	}
	// Chat finish_reason 或 Messages stop_reason 会让目标转换器提前生成终态。
	// 先验证其后确实有源协议结束标记，再交付这小段尾部，避免错误被目标终态遮住。
	if s.startsTerminalTail(event) && !s.terminal {
		bytesBuffered := 0
		for !s.terminal {
			next := s.readEvent()
			if next == nil {
				s.pending = nil
				return false
			}
			bytesBuffered += len(next.Data)
			if len(s.pending) >= 1024 || bytesBuffered > 8*1024*1024 {
				s.err = errors.New("upstream terminal tail exceeds buffer limit")
				s.pending = nil
				return false
			}
			s.pending = append(s.pending, next)
		}
	}
	s.current = event
	return true
}

func (s *validatedUpstreamStream) readEvent() *httpclient.StreamEvent {
	for s.source.Next() {
		event := s.source.Current()
		if event == nil || len(event.Data) == 0 {
			continue
		}
		if s.observe != nil {
			s.observe(event)
		}
		last, err := inspectStreamEvent(s.format, event)
		if err != nil {
			s.err = err
			return nil
		}
		s.terminal = last
		return event
	}
	s.err = s.source.Err()
	if s.err == nil {
		s.err = fmt.Errorf("%s: %w", s.format, llm.ErrStreamIncomplete)
	}
	return nil
}

func (s *validatedUpstreamStream) startsTerminalTail(event *httpclient.StreamEvent) bool {
	switch s.format {
	case llm.APIFormatOpenAIChatCompletion:
		var chunk struct {
			Choices []struct {
				FinishReason *string `json:"finish_reason"`
			} `json:"choices"`
		}
		if json.Unmarshal(event.Data, &chunk) == nil {
			for _, choice := range chunk.Choices {
				if choice.FinishReason != nil && *choice.FinishReason != "" {
					return true
				}
			}
		}
	case llm.APIFormatAnthropicMessage:
		var chunk struct {
			Type  string `json:"type"`
			Delta struct {
				StopReason *string `json:"stop_reason"`
			} `json:"delta"`
		}
		if json.Unmarshal(event.Data, &chunk) == nil {
			return chunk.Type == "message_delta" && chunk.Delta.StopReason != nil
		}
	}
	return false
}

func (s *validatedUpstreamStream) Current() *httpclient.StreamEvent { return s.current }
func (s *validatedUpstreamStream) Err() error                       { return s.err }
func (s *validatedUpstreamStream) Close() error                     { return s.source.Close() }

func (m *conversionMiddleware) OnOutboundRawStream(_ context.Context, source streams.Stream[*httpclient.StreamEvent]) (streams.Stream[*httpclient.StreamEvent], error) {
	return &validatedUpstreamStream{source: source, format: m.format, observe: m.observeRawUsage}, nil
}

// observeRawUsage 保留原始上游用量，失败终态和被缓冲的尾部也参与计量。
// 必须在原始层采集，避免转换器提前终止或合成占位值导致用量丢失。
func (m *conversionMiddleware) observeRawUsage(event *httpclient.StreamEvent) {
	switch m.format {
	case llm.APIFormatOpenAIChatCompletion:
		var chunk struct {
			Usage *openai.Usage `json:"usage"`
		}
		if json.Unmarshal(event.Data, &chunk) == nil && chunk.Usage != nil {
			m.usage = chunk.Usage.ToLLMUsage()
		}
	case llm.APIFormatOpenAIResponse:
		var chunk struct {
			Response *struct {
				Usage *responses.Usage `json:"usage"`
			} `json:"response"`
		}
		if json.Unmarshal(event.Data, &chunk) == nil && chunk.Response != nil && chunk.Response.Usage != nil {
			m.usage = chunk.Response.Usage.ToUsage()
		}
	case llm.APIFormatAnthropicMessage:
		var chunk struct {
			Usage   json.RawMessage `json:"usage"`
			Message *struct {
				Usage json.RawMessage `json:"usage"`
			} `json:"message"`
		}
		if json.Unmarshal(event.Data, &chunk) != nil {
			return
		}
		raw := chunk.Usage
		if chunk.Message != nil {
			raw = chunk.Message.Usage
		}
		if len(raw) == 0 || string(raw) == "null" {
			return
		}
		usage := m.messageUsage
		if json.Unmarshal(raw, &usage) != nil {
			return
		}
		m.messageUsage = usage
		m.usage = directMessagesUsage(usage)
	}
}

// 渠道的 Messages 出站固定采用 PlatformDirect。输入总量按官方口径包含缓存读写；
// 增量 usage 先合并再换算，不能将 message_delta 省略的 input_tokens 当成零。
func directMessagesUsage(usage anthropic.Usage) *llm.Usage {
	cached := usage.CacheReadInputTokens
	if usage.CachedTokens > 0 && usage.CacheCreationInputTokens == 0 {
		cached = usage.CachedTokens
	}
	prompt := usage.InputTokens + cached + usage.CacheCreationInputTokens
	result := &llm.Usage{PromptTokens: prompt, CompletionTokens: usage.OutputTokens, TotalTokens: prompt + usage.OutputTokens}
	if cached != 0 || usage.CacheCreationInputTokens != 0 || usage.CacheCreation.Ephemeral5mInputTokens != 0 || usage.CacheCreation.Ephemeral1hInputTokens != 0 {
		result.PromptTokensDetails = &llm.PromptTokensDetails{CachedTokens: cached, WriteCachedTokens: usage.CacheCreationInputTokens, WriteCached5MinTokens: usage.CacheCreation.Ephemeral5mInputTokens, WriteCached1HourTokens: usage.CacheCreation.Ephemeral1hInputTokens}
	}
	return result
}

// clientErrorStream 将转换器的读取错误变成客户端协议的错误事件。Responses 转换器
// 可能已经生成 response.failed；看见终态后不再读取，避免追加第二个错误或 DONE。
type clientErrorStream struct {
	source   streams.Stream[*httpclient.StreamEvent]
	format   llm.APIFormat
	current  *httpclient.StreamEvent
	terminal bool
	err      error
}

func (s *clientErrorStream) Next() bool {
	if s.terminal {
		return false
	}
	if s.source.Next() {
		s.current = s.source.Current()
		s.terminal, _ = inspectStreamEvent(s.format, s.current)
		return true
	}
	s.err = s.source.Err()
	if s.err == nil {
		s.err = llm.ErrStreamIncomplete
	}
	s.current = streamErrorEvent(s.format, s.err)
	s.terminal = true
	return true
}

func (s *clientErrorStream) Current() *httpclient.StreamEvent { return s.current }
func (s *clientErrorStream) Err() error                       { return s.err }
func (s *clientErrorStream) Close() error                     { return s.source.Close() }

func streamErrorEvent(format llm.APIFormat, err error) *httpclient.StreamEvent {
	detail := llm.ErrorDetail{Type: "upstream_error", Code: "upstream_error", Message: err.Error()}
	var failure *llm.ResponseError
	if errors.As(err, &failure) {
		detail = failure.Detail
		if detail.Message == "" {
			detail.Message = err.Error()
		}
	}
	var body any
	eventType := "error"
	switch format {
	case llm.APIFormatAnthropicMessage:
		body = map[string]any{"type": "error", "error": map[string]string{"type": "api_error", "message": detail.Message}}
	case llm.APIFormatOpenAIResponse:
		body = map[string]any{"type": "error", "code": detail.Code, "message": detail.Message}
	default:
		eventType = ""
		body = map[string]any{"error": detail}
	}
	data, _ := json.Marshal(body)
	return &httpclient.StreamEvent{Type: eventType, Data: data}
}
