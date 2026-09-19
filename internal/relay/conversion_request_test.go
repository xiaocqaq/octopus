package relay

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"testing"

	"github.com/bestruirui/octopus/internal/model"
	"github.com/looplj/axonhub/llm"
	"github.com/looplj/axonhub/llm/httpclient"
	"github.com/looplj/axonhub/llm/transformer"
)

func TestConversionServerReferences(t *testing.T) {
	for _, target := range []llm.APIFormat{llm.APIFormatOpenAIChatCompletion, llm.APIFormatAnthropicMessage} {
		for _, kind := range []string{"conversation", "item_reference"} {
			t.Run(conversionTestName(target)+"/"+kind, func(t *testing.T) {
				req := conversionTestRequest(llm.APIFormatOpenAIResponse, false, false)
				if kind == "conversation" {
					req["conversation"] = "conv_other"
				} else {
					req["input"] = []any{conversionTestMap{"type": "item_reference", "id": "item_other"}}
				}
				_, _, sent, err := conversionTestRun(t, llm.APIFormatOpenAIResponse, target, req, false, false)
				if conversionClientError(err) == nil || len(sent) > 0 {
					t.Fatalf("stateful reference not rejected before upstream: err=%v outbound=%s", err, sent)
				}
			})
		}
	}
}

func TestConversionClientErrorClassification(t *testing.T) {
	if conversionClientError(errors.New("network failure")) != nil {
		t.Fatal("network error classified as client error")
	}
	if conversionClientError(&httpclient.Error{StatusCode: 400}) != nil {
		t.Fatal("upstream HTTP 400 classified as local conversion error")
	}
	for _, err := range []error{
		fmt.Errorf("pipeline: %w", incompatibleField(llm.APIFormatOpenAIChatCompletion, "tools")),
		fmt.Errorf("decode: %w", transformer.ErrInvalidRequest),
	} {
		client := conversionClientError(err)
		if client == nil || client.StatusCode != 400 {
			t.Fatalf("client error not recognized: %v", err)
		}
		for _, source := range conversionTestFormats {
			response := conversionTestInbound(source).TransformError(context.Background(), client)
			if response.StatusCode != 400 || !json.Valid(response.Body) {
				t.Fatalf("bad client error response: %s", response.Body)
			}
		}
	}
}

func TestSameProtocolPreservesAdvancedRequest(t *testing.T) {
	format := llm.APIFormatOpenAIResponse
	body := []byte(`{"model":"mock","previous_response_id":"resp_previous","tools":[{"type":"custom","name":"run_code","format":{"type":"text"}}],"input":"continue","text":{"format":{"type":"json_schema","name":"result","schema":{"type":"object"}}}}`)
	raw := &httpclient.Request{Method: "POST", Headers: http.Header{"Content-Type": []string{"application/json"}}, Body: body}
	request, err := buildPassthroughRequest(format, raw, model.Channel{}, conversionTestOutbound(t, format, "http://127.0.0.1"), "mock")
	if err != nil {
		t.Fatal(err)
	}
	if string(request.Body) != string(body) {
		t.Fatalf("same-protocol request was changed: %s", request.Body)
	}
}

func TestConversionChatUsageAfterOverrides(t *testing.T) {
	middleware := &conversionMiddleware{format: llm.APIFormatOpenAIChatCompletion, channel: model.Channel{ChannelConfig: model.ChannelConfig{ParamOverride: `{"stream_options":{"include_usage":false}}`}}}
	request := &httpclient.Request{Headers: http.Header{}, Body: []byte(`{"stream":true}`), JSONBody: []byte(`{"stream":true}`)}
	request, err := middleware.OnOutboundRawRequest(context.Background(), request)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(request.Body), `"include_usage":true`) || string(request.JSONBody) != string(request.Body) {
		t.Fatalf("outbound usage not enforced consistently: body=%s JSONBody=%s", request.Body, request.JSONBody)
	}
}

func TestConversionStructuredOutputChatResponses(t *testing.T) {
	for _, source := range []llm.APIFormat{llm.APIFormatOpenAIChatCompletion, llm.APIFormatOpenAIResponse} {
		t.Run(conversionTestName(source), func(t *testing.T) {
			target := llm.APIFormatOpenAIResponse
			req := conversionTestRequest(source, false, false)
			schema := conversionTestMap{"type": "object", "properties": conversionTestMap{"value": conversionTestMap{"type": "string"}}}
			if source == llm.APIFormatOpenAIChatCompletion {
				req["response_format"] = conversionTestMap{"type": "json_schema", "json_schema": conversionTestMap{"name": "result", "strict": true, "schema": schema}}
			} else {
				target = llm.APIFormatOpenAIChatCompletion
				req["text"] = conversionTestMap{"format": conversionTestMap{"type": "json_schema", "name": "result", "strict": true, "schema": schema}}
			}
			_, _, sent, err := conversionTestRun(t, source, target, req, false, false)
			if err != nil {
				t.Fatal(err)
			}
			if !strings.Contains(string(sent), `"json_schema"`) || !strings.Contains(string(sent), `"strict":true`) {
				t.Fatalf("supported schema conversion lost: %s", sent)
			}
		})
	}
}
