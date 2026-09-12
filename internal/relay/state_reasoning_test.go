package relay

import "testing"

// TestReasoningOf 覆盖三种协议各自的思维强度字段与需要按未声明处理的输入。
func TestReasoningOf(t *testing.T) {
	cases := []struct {
		name string
		body string
		want string
	}{
		{"OpenAI Chat 档位", `{"model":"gpt-5","reasoning_effort":"high"}`, "high"},
		{"Responses 档位", `{"model":"gpt-5","reasoning":{"effort":"minimal"}}`, "minimal"},
		{"Anthropic 预算折 k", `{"model":"claude","thinking":{"type":"enabled","budget_tokens":8000}}`, "8k"},
		{"Anthropic 预算不足 1k", `{"model":"claude","thinking":{"type":"enabled","budget_tokens":512}}`, "512"},
		{"Anthropic 显式关闭", `{"model":"claude","thinking":{"type":"disabled","budget_tokens":8000}}`, ""},
		{"Anthropic 预算为零", `{"model":"claude","thinking":{"type":"enabled","budget_tokens":0}}`, ""},
		{"未声明", `{"model":"gpt-5"}`, ""},
		{"空正文", ``, ""},
		{"非法 JSON", `{oops`, ""},
		{"Chat 优先于 Responses", `{"reasoning_effort":"low","reasoning":{"effort":"high"}}`, "low"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := reasoningOf(c.body); got != c.want {
				t.Errorf("reasoningOf(%s) = %q, 期望 %q", c.body, got, c.want)
			}
		})
	}
}
