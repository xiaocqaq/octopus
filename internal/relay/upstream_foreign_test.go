package relay

import (
	"context"
	"net/http"
	"testing"

	"github.com/bestruirui/octopus/internal/model"
	"github.com/looplj/axonhub/llm"
	"github.com/looplj/axonhub/llm/httpclient"
)

// TestDropForeignChatFields 跨协议转换到 Chat Completions 时, OpenAI 专有的 prompt_cache_key 必须被清掉。
// 实测: Codex 用 Responses 协议发的这个字段经转换后仍在 Chat 体里, DeepSeek 系端点直接 400 UNKNOWN_FIELD。
func TestDropForeignChatFields(t *testing.T) {
	body := []byte(`{"model":"deepseek-flash","messages":[{"role":"user","content":"hi"}],"prompt_cache_key":"abc","stream":true}`)

	converted := &httpclient.Request{Body: body, JSONBody: body}
	dropForeignChatFields(llm.APIFormatOpenAIChatCompletion, converted)
	payload := decodePayload(t, converted.Body)
	if _, exists := payload["prompt_cache_key"]; exists {
		t.Fatalf("转换后的 Chat 体里仍留着 prompt_cache_key: %s", converted.Body)
	}
	if payload["model"] != "deepseek-flash" || payload["stream"] != true {
		t.Fatalf("只该删这一个字段, 其余字段被改动了: %s", converted.Body)
	}
	if string(converted.JSONBody) != string(converted.Body) {
		t.Fatalf("JSONBody 未被同步: %s", converted.JSONBody)
	}

	// 同协议透传不经过本函数; 万一传了非 Chat 协议也不该动它。
	untouched := &httpclient.Request{Body: body}
	dropForeignChatFields(llm.APIFormatOpenAIResponse, untouched)
	if string(untouched.Body) != string(body) {
		t.Fatalf("非 Chat 出站协议不该改动请求体: %s", untouched.Body)
	}

	// 没有该字段的请求体保持原样(不做无谓的解析与重编码)。
	plain := []byte(`{"model":"m","messages":[]}`)
	untouched = &httpclient.Request{Body: plain}
	dropForeignChatFields(llm.APIFormatOpenAIChatCompletion, untouched)
	if string(untouched.Body) != string(plain) {
		t.Fatalf("没有该字段时不该改动请求体: %s", untouched.Body)
	}

	// nil 请求体不 panic。
	dropForeignChatFields(llm.APIFormatOpenAIChatCompletion, nil)
	dropForeignChatFields(llm.APIFormatOpenAIChatCompletion, &httpclient.Request{})
}

// TestConversionMiddlewareDropOrder 渠道参数覆盖必须发生在自动清理之后:
// 渠道若显式写了这个字段, 那是运维的明确意图, 不该被自动清理再删一遍。
func TestConversionMiddlewareDropOrder(t *testing.T) {
	body := []byte(`{"model":"deepseek-flash","messages":[],"prompt_cache_key":"from-client"}`)
	channel := model.Channel{ChannelConfig: model.ChannelConfig{ParamOverride: `{"prompt_cache_key":"from-channel"}`}}
	middleware := &conversionMiddleware{channel: channel, format: llm.APIFormatOpenAIChatCompletion}

	request := &httpclient.Request{Method: http.MethodPost, Body: body, JSONBody: body, Headers: http.Header{}}
	updated, err := middleware.OnOutboundRawRequest(context.Background(), request)
	if err != nil {
		t.Fatalf("中间件返回错误: %v", err)
	}
	payload := decodePayload(t, updated.Body)
	if payload["prompt_cache_key"] != "from-channel" {
		t.Fatalf("渠道显式覆盖的值应生效, 却得到 %v (体: %s)", payload["prompt_cache_key"], updated.Body)
	}

	// 渠道没写这个字段时, 客户端带来的值应被清掉。
	plain := &conversionMiddleware{channel: model.Channel{}, format: llm.APIFormatOpenAIChatCompletion}
	request = &httpclient.Request{Method: http.MethodPost, Body: body, JSONBody: body, Headers: http.Header{}}
	updated, err = plain.OnOutboundRawRequest(context.Background(), request)
	if err != nil {
		t.Fatalf("中间件返回错误: %v", err)
	}
	if _, exists := decodePayload(t, updated.Body)["prompt_cache_key"]; exists {
		t.Fatalf("渠道未覆盖时该字段应被清掉: %s", updated.Body)
	}
}
