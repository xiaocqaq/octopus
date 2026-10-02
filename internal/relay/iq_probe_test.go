package relay

import "testing"

// TestParseIQAnswer 覆盖"末尾整数"提取的各种形态。
// 判分口径是精确匹配, 因此提取环节的每一次偏差都会直接变成一次误判 —— 这些用例守的就是这条口径。
func TestParseIQAnswer(t *testing.T) {
	cases := []struct {
		name string
		body string
		want string
		ok   bool
	}{
		{
			name: "只给数字",
			body: `{"choices":[{"message":{"content":"21"}}]}`,
			want: "21",
			ok:   true,
		},
		{
			// 关键用例: 推理过程中先出现若干数字, 必须取末尾那个。取第一个会得到 7, 判错。
			name: "带推理过程的回答取末尾数字",
			body: `{"choices":[{"message":{"content":"圆苹果7 + 星桃子12 = 19, 但还要考虑西瓜8。最少需要 21 个。"}}]}`,
			want: "21",
			ok:   true,
		},
		{
			name: "末尾带句号和换行",
			body: `{"choices":[{"message":{"content":"答案是 21。"}}]}`,
			want: "21",
			ok:   true,
		},
		{
			name: "前导零规范化",
			body: `{"choices":[{"message":{"content":"007"}}]}`,
			want: "7",
			ok:   true,
		},
		{
			name: "纯文字回答提不出数字",
			body: `{"choices":[{"message":{"content":"我认为无法确定。"}}]}`,
			want: "",
			ok:   false,
		},
		{
			name: "空回答",
			body: `{"choices":[{"message":{"content":""}}]}`,
			want: "",
			ok:   false,
		},
		{
			// 非 JSON 正文(透传路径的原样响应)必须退回原文, 不能因为解析失败就丢掉回答。
			name: "非 JSON 正文退回原文",
			body: `plain text answer is 21`,
			want: "21",
			ok:   true,
		},
		{
			// 推理模型把内容放在 reasoning_content 时同样要能判分。
			name: "推理字段兜底",
			body: `{"choices":[{"message":{"content":"","reasoning_content":"let me think... 21"}}]}`,
			want: "21",
			ok:   true,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, ok := parseIQAnswer([]byte(tc.body))
			if ok != tc.ok {
				t.Fatalf("parseIQAnswer(%s) ok = %v, want %v", tc.body, ok, tc.ok)
			}
			if got != tc.want {
				t.Fatalf("parseIQAnswer(%s) = %q, want %q", tc.body, got, tc.want)
			}
		})
	}
}

// TestGradeIQAnswer 守的是"末尾数字精确匹配"这条口径本身: 只有与标准答案完全一致才算对。
func TestGradeIQAnswer(t *testing.T) {
	question, ok := iqQuestionByID(iqDefaultQuestionID)
	if !ok {
		t.Fatalf("默认题 %s 不在题库里", iqDefaultQuestionID)
	}
	if question.Answer != "21" {
		t.Fatalf("默认题的标准答案应为 21, 实际为 %q", question.Answer)
	}

	correct, ok := gradeIQAnswer(question, []byte(`{"choices":[{"message":{"content":"21"}}]}`))
	if !ok || !correct.Correct || correct.Answer != "21" {
		t.Fatalf("答 21 应判对, 实际 ok=%v result=%+v", ok, correct)
	}

	wrong, ok := gradeIQAnswer(question, []byte(`{"choices":[{"message":{"content":"22"}}]}`))
	if !ok || wrong.Correct {
		t.Fatalf("答 22 应判错, 实际 ok=%v result=%+v", ok, wrong)
	}
	if wrong.QuestionID != question.ID {
		t.Fatalf("判分结论应带题号 %q, 实际 %q", question.ID, wrong.QuestionID)
	}
}

// TestIQQuestionByID 保证题号能反查回题目, 也保证未知题号被明确拒绝而不是静默取第一题。
func TestIQQuestionByID(t *testing.T) {
	if _, ok := iqQuestionByID(iqDefaultQuestionID); !ok {
		t.Fatalf("iqQuestionByID(%q) 应命中", iqDefaultQuestionID)
	}
	if _, ok := iqQuestionByID("no-such-question"); ok {
		t.Fatal("未知题号不该命中")
	}
}

// TestIQTextOfPrefersContentOverReasoning 守"正文优先于推理"这条取值顺序:
// 有些模型同时给正文与推理, 正文才是它最终表态的地方。
func TestIQTextOfPrefersContentOverReasoning(t *testing.T) {
	body := []byte(`{"choices":[{"message":{"content":"答案是21","reasoning_content":"我算了半天觉得是22"}}]}`)
	if got := iqTextOf(body); got != "答案是21" {
		t.Fatalf("应优先取正文, 实际取到 %q", got)
	}
}

// TestIQTextOfAnthropicBlocks 守 Anthropic 风格的块数组正文。
//
// 这条用例存在是因为它曾经是错的: 顶层 content 在 OpenAI 窄接口下是字符串、在 Anthropic 下是
// [{text}] 块数组, 同一个结构体里写两个 json:"content" 字段时编码器只认外层那个,
// 块数组分支永远不会被走到 —— 症状是 Anthropic 上游的回答整段取不到, 末尾数字自然也就提不出来。
func TestIQTextOfAnthropicBlocks(t *testing.T) {
	body := []byte(`{"content":[{"type":"text","text":"先取 9 个圆"},{"type":"text","text":"再取 12 个星, 最少 21 个。"}]}`)
	if got := iqTextOf(body); got != "先取 9 个圆再取 12 个星, 最少 21 个。" {
		t.Fatalf("块数组应拼接全部 text 块, 实际取到 %q", got)
	}
	// 拼出来的文本同样要能判分: 这才是这条路存在的意义。
	if answer, ok := parseIQAnswer(body); !ok || answer != "21" {
		t.Fatalf("块数组正文应提取出 21, 实际 ok=%v answer=%q", ok, answer)
	}
}

// TestIQTextOfStringContent 守 OpenAI 窄接口的顶层字符串 content(与上面的块数组同名不同型)。
func TestIQTextOfStringContent(t *testing.T) {
	body := []byte(`{"content":"答案是 21"}`)
	if got := iqTextOf(body); got != "答案是 21" {
		t.Fatalf("顶层字符串 content 应原样取用, 实际取到 %q", got)
	}
}
