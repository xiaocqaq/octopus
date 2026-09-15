package relay

import (
	"errors"
	"testing"

	"github.com/looplj/axonhub/llm"
	"github.com/looplj/axonhub/llm/httpclient"
)

func TestInspectResponsesStreamError(t *testing.T) {
	event := &httpclient.StreamEvent{
		Type: "error",
		Data: []byte(`{"type":"error"}`),
	}

	last, err := inspectStreamEvent(llm.APIFormatOpenAIResponse, event)
	if !last {
		t.Fatal("Responses error 事件必须结束响应流")
	}
	var failure *llm.ResponseError
	if !errors.As(err, &failure) {
		t.Fatalf("Responses error 事件应返回协议错误, 实际 %v", err)
	}
	if failure.Detail.Type != "stream_error" {
		t.Fatalf("错误类型应为 stream_error, 实际 %q", failure.Detail.Type)
	}
}
