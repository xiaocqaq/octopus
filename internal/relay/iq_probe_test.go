package relay

import "testing"

// TestParseIQAnswer 覆盖"从模型回答里取出它表态的数字"这条链路的各种形态。
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
			// 题面允许"答案是 21"这类少量包装, 但包装不能长到把推理装进来。
			name: "答题式短句仍然认",
			body: `{"choices":[{"message":{"content":"最少需要取出 21 个糖果。"}}]}`,
			want: "21",
			ok:   true,
		},
		{
			// 回归用例: 真实踩过的坑 —— 模型长篇输出后落在 5099 上, 旧口径"取末尾整数"把 5099
			// 当成它的答案摆在界面上。而 5099 只是它算式里的中间结果: 整段是论述、不分行、也没有
			// 自报结论, 三层都不成立 → 如实记"没答出可判读的数字"。
			name: "冗长论述不判分",
			body: `{"choices":[{"message":{"content":"圆苹果7 + 星桃子12 = 19, 但还要考虑西瓜8。若对手把西瓜都摆在圆里, 则需要 9 个圆 12 个星, 合计 5099。"}}]}`,
			want: "",
			ok:   false,
		},
		{
			// 兼容用例(m04864): 模型先长篇推理、最后自报结论时应当判分 —— 题面要求的就是那个数,
			// 格式不合规不等于没作答。取最后一处自报结论: 结论总在推理之后。
			name: "长回答自报答案仍判分",
			body: `{"choices":[{"message":{"content":"圆形: 苹果7 桃子9 西瓜8。\n五角星: 苹果7 桃子6 西瓜4。\n先看圆里最坏情况, 再看星里最坏情况。\n所以答案是 21。"}}]}`,
			want: "21",
			ok:   true,
		},
		{
			// 兼容用例(m04864): 另一种常见收尾 —— 论证分多行, 末行单独给数字。
			name: "末行只给数字仍判分",
			body: `{"choices":[{"message":{"content":"先把圆形里的西瓜都算成干扰项, 再让五角星的苹果桃子各缺一边。\n两种情况取较大者, 加一即是保证数。\n21"}}]}`,
			want: "21",
			ok:   true,
		},
		{
			name: "末尾带句号和换行",
			body: `{"choices":[{"message":{"content":"答案是 21。\n"}}]}`,
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
			// 推理不是作答: 草稿里全是中间数, 拿它判分等于把"想到哪儿了"当成"答了什么"。
			name: "推理字段不算作答",
			body: `{"choices":[{"message":{"content":"","reasoning_content":"let me think... 21"}}]}`,
			want: "",
			ok:   false,
		},
		{
			// 根因修复: 是 JSON 就不能退回原文。旧实现解析未命中时 return string(body),
			// 整段响应(含 usage 的 token 数)于是进入整数匹配 —— 界面上的"答了 5099"就是这么来的。
			name: "合法JSON但无正文不取usage数字",
			body: `{"id":"chatcmpl-x","usage":{"prompt_tokens":17,"total_tokens":5099,"completion_tokens":5082}}`,
			want: "",
			ok:   false,
		},
		{
			// OpenAI Responses API: 回答在 output[] 的 message 项里, 同级的 reasoning 项是草稿。
			name: "Responses取值跳过推理项",
			body: `{"status":"completed","output":[{"type":"reasoning","summary":[{"type":"summary_text","text":"先把西瓜都算成干扰项, 合计 5099"}]},{"type":"message","role":"assistant","content":[{"type":"output_text","text":"29"}]}],"usage":{"output_tokens":2048}}`,
			want: "29",
			ok:   true,
		},
		{
			// 只有推理、没有作答的响应(真实取样里约占四成, 多因触顶被截断): 一个字都不该被读出来。
			name: "Responses只有推理时不取数",
			body: `{"status":"incomplete","output":[{"type":"reasoning","summary":[{"type":"summary_text","text":"Then guarantee n=29. Sum max avoiding =7+9+8+4=28."}]}],"usage":{"total_tokens":5099,"output_tokens":2048}}`,
			want: "",
			ok:   false,
		},
		{
			// Anthropic: thinking 块是草稿, 只有 text 块是作答。
			name: "Anthropic跳过thinking块",
			body: `{"content":[{"type":"thinking","thinking":"... 合计 5099, 所以保守取 29 ..."},{"type":"text","text":"21"}],"stop_reason":"end_turn"}`,
			want: "21",
			ok:   true,
		},
		{
			// 有些上游把 message.content 给成 parts 数组而不是字符串。
			name: "content为parts数组",
			body: `{"choices":[{"message":{"content":[{"type":"text","text":"答案是 "},{"type":"text","text":"21"}]}}]}`,
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

// TestGradeIQAnswer 守"回答里出现标准答案就算过"这条口径: 判的是说没说对, 不是有没有守格式。
func TestGradeIQAnswer(t *testing.T) {
	question, ok := iqQuestionByID(iqDefaultQuestionID)
	if !ok {
		t.Fatalf("默认题 %s 不在题库里", iqDefaultQuestionID)
	}
	if question.Answer == "" {
		t.Fatalf("默认题 %s 没有标准答案", iqDefaultQuestionID)
	}

	correct, ok := gradeIQAnswer(question, []byte(`{"choices":[{"message":{"content":"`+question.Answer+`"}}]}`))
	if !ok || !correct.Correct || correct.Answer != question.Answer {
		t.Fatalf("答 %s 应判对, 实际 ok=%v result=%+v", question.Answer, ok, correct)
	}

	// 话多也算对: 结论夹在解释里同样是一次正确作答(这正是本轮要兼容的形状)。
	verbose := "先看最坏情况, 圆形里只放苹果和桃子, 五角星里只放苹果和西瓜, 这样拿不到目标组合。" +
		"所以要分别取 9 个圆和 12 个星, 最少 " + question.Answer + " 个。"
	graded, ok := gradeIQAnswer(question, []byte(`{"choices":[{"message":{"content":"`+verbose+`"}}]}`))
	if !ok || !graded.Correct {
		t.Fatalf("长篇回答里出现了 %s 应判对, 实际 ok=%v result=%+v", question.Answer, ok, graded)
	}

	wrong, ok := gradeIQAnswer(question, []byte(`{"choices":[{"message":{"content":"999999"}}]}`))
	if !ok || wrong.Correct {
		t.Fatalf("答 999999 应判错, 实际 ok=%v result=%+v", ok, wrong)
	}
	if wrong.QuestionID != question.ID {
		t.Fatalf("判分结论应带题号 %q, 实际 %q", question.ID, wrong.QuestionID)
	}
}

// TestIQAnswerIn 守模糊匹配的边界: 整数答案按整数比, 文字答案按忽略大小写的子串比。
func TestIQAnswerIn(t *testing.T) {
	cases := []struct {
		name   string
		reply  string
		answer string
		want   bool
	}{
		{name: "整数夹在句子里算命中", reply: "最少需要取出 21 个糖果。", answer: "21", want: true},
		{name: "整数按整数比而不是子串", reply: "我算出来是 210 个", answer: "21", want: false},
		{name: "前导零写法仍算命中", reply: "答案是 021", answer: "21", want: true},
		{name: "没提到答案就不算", reply: "这道题我不会", answer: "21", want: false},
		{name: "文字答案忽略大小写", reply: "答案是 apple", answer: "APPLE", want: true},
		{name: "空答案不算通过", reply: "21", answer: "  ", want: false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := iqAnswerIn(tc.reply, tc.answer); got != tc.want {
				t.Fatalf("iqAnswerIn(%q, %q) = %v, want %v", tc.reply, tc.answer, got, tc.want)
			}
		})
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

// TestIQReplyOfPrefersContentOverReasoning 守"正文优先于推理"这条取值顺序:
// 有些模型同时给正文与推理, 正文才是它最终表态的地方。
func TestIQReplyOfPrefersContentOverReasoning(t *testing.T) {
	body := []byte(`{"choices":[{"message":{"content":"答案是21","reasoning_content":"我算了半天觉得是22"}}]}`)
	if got := iqReplyOf(body); got != "答案是21" {
		t.Fatalf("应优先取正文, 实际取到 %q", got)
	}
}

// TestIQReplyOfAnthropicBlocks 守 Anthropic 风格的块数组正文。
//
// 这条用例存在是因为它曾经是错的: 顶层 content 在 OpenAI 窄接口下是字符串、在 Anthropic 下是
// [{text}] 块数组, 同一个结构体里写两个 json:"content" 字段时编码器只认外层那个,
// 块数组分支永远不会被走到 —— 症状是 Anthropic 上游的回答整段取不到, 末尾数字自然也就提不出来。
func TestIQReplyOfAnthropicBlocks(t *testing.T) {
	body := []byte(`{"content":[{"type":"text","text":"先取 9 个圆"},{"type":"text","text":"再取 12 个星, 最少 21 个。"}]}`)
	if got := iqReplyOf(body); got != "先取 9 个圆再取 12 个星, 最少 21 个。" {
		t.Fatalf("块数组应拼接全部 text 块, 实际取到 %q", got)
	}
	// 块数组同样要能走到判分: 合规回答被上游拆成多个块时(Anthropic 常见), 拼接后仍应可判读。
	terse := []byte(`{"content":[{"type":"text","text":"答案是 "},{"type":"text","text":"21"}]}`)
	if answer, ok := parseIQAnswer(terse); !ok || answer != "21" {
		t.Fatalf("块数组里的直接作答应提取出 21, 实际 ok=%v answer=%q", ok, answer)
	}
}

// TestIQReplyOfStringContent 守 OpenAI 窄接口的顶层字符串 content(与上面的块数组同名不同型)。
func TestIQReplyOfStringContent(t *testing.T) {
	body := []byte(`{"content":"答案是 21"}`)
	if got := iqReplyOf(body); got != "答案是 21" {
		t.Fatalf("顶层字符串 content 应原样取用, 实际取到 %q", got)
	}
}

// TestIQReplyOfRejectsJSONWithoutReply 守最后一道闸: 合法 JSON 里认不出正文时返回空串,
// 绝不把整段响应当正文交出去 —— 这正是"usage 的 token 数被当成答案"的入口。
func TestIQReplyOfRejectsJSONWithoutReply(t *testing.T) {
	body := []byte(`{"usage":{"total_tokens":5099}}`)
	if got := iqReplyOf(body); got != "" {
		t.Fatalf("认不出正文时应返回空串, 实际取到 %q", got)
	}
}
