package handlers

import (
	"encoding/json"
	"testing"

	"github.com/bestruirui/octopus/internal/model"
	"github.com/bestruirui/octopus/internal/relay"
)

// TestIQFieldsOf 守"结论 → 行字段"这一处的取值口径。
//
// 这一处很容易在改动中静默退化成"永远为空": nil 判空和 bool 零值恰好都长得像"没问过",
// 于是漏填时界面不会报错, 只会永远不显示结论 —— 那种坏法没有任何报错来提示, 只能靠用例盯住。
//
// 三个返回值里最容易被写错的是第一个: 它区分"没问过"和"问了但答不出数字", 而这两种状态的
// Answer 都是空串 —— 只看 Answer 就把后一种吞成了前一种, 界面上本该显示"降智"的一行会消失。
func TestIQFieldsOf(t *testing.T) {
	cases := []struct {
		name        string
		iq          *relay.IQResult
		wantAsked   bool
		wantAnswer  string
		wantCorrect bool
	}{
		{
			// 没问过: 三个字段都是零值, 界面据此完全不显示智商那一格。
			name: "没问过",
			iq:   nil,
		},
		{
			name:        "答对了",
			iq:          &relay.IQResult{QuestionID: "candy-21", Answer: "21", Correct: true},
			wantAsked:   true,
			wantAnswer:  "21",
			wantCorrect: true,
		},
		{
			// 问了但答错: 有答案、判错, 界面显示"降智"。
			name:        "答错了",
			iq:          &relay.IQResult{QuestionID: "candy-21", Answer: "22", Correct: false},
			wantAsked:   true,
			wantAnswer:  "22",
			wantCorrect: false,
		},
		{
			// 问了但答不出可判分的答案: 答案为空、判错。这是"问过"而不是"没问过" ——
			// 界面必须把它显示成"降智"而不是留空, 否则模型胡言乱语反而在界面上不留痕迹。
			name:       "问了但提不出数字",
			iq:         &relay.IQResult{QuestionID: "candy-21", Answer: "", Correct: false},
			wantAsked:  true,
			wantAnswer: "",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			asked, answer, correct := iqFieldsOf(relay.ProbeResult{IQ: tc.iq})
			if asked != tc.wantAsked || answer != tc.wantAnswer || correct != tc.wantCorrect {
				t.Fatalf("iqFieldsOf = (%v, %q, %v), want (%v, %q, %v)",
					asked, answer, correct, tc.wantAsked, tc.wantAnswer, tc.wantCorrect)
			}
		})
	}
}

// TestScheduledProbeRowIQJSON 守字段名与前端一致。
//
// 前端按 iq_asked / iq_answer / iq_correct 取值, 名字一旦被改名或漏掉 json 标签, 前端只会拿到
// undefined, 页面照常渲染、只是永远不出结论 —— 跨语言的那一半协议没有编译器帮忙看, 只能在这里钉住。
func TestScheduledProbeRowIQJSON(t *testing.T) {
	asked, answer, correct := iqFieldsOf(relay.ProbeResult{IQ: &relay.IQResult{Answer: "21", Correct: true}})
	row := model.ScheduledProbeRow{IQAsked: asked, IQAnswer: answer, IQCorrect: correct}
	encoded, err := json.Marshal(row)
	if err != nil {
		t.Fatal(err)
	}
	var decoded map[string]any
	if err := json.Unmarshal(encoded, &decoded); err != nil {
		t.Fatal(err)
	}
	if decoded["iq_asked"] != true {
		t.Fatalf("JSON 里 iq_asked 应为 true, 实际 %#v", decoded["iq_asked"])
	}
	if decoded["iq_answer"] != "21" {
		t.Fatalf("JSON 里 iq_answer 应为 \"21\", 实际 %#v", decoded["iq_answer"])
	}
	if decoded["iq_correct"] != true {
		t.Fatalf("JSON 里 iq_correct 应为 true, 实际 %#v", decoded["iq_correct"])
	}
	// 没问过时这三个键仍必须存在(而不是被 omitempty 抹掉):
	// 前端按固定形状读数, 键时有时无会让"没问过"和"字段丢失"在界面上长得一模一样。
	emptyRow := model.ScheduledProbeRow{}
	encodedEmpty, err := json.Marshal(emptyRow)
	if err != nil {
		t.Fatal(err)
	}
	var decodedEmpty map[string]any
	if err := json.Unmarshal(encodedEmpty, &decodedEmpty); err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{"iq_asked", "iq_answer", "iq_correct"} {
		if _, ok := decodedEmpty[key]; !ok {
			t.Fatalf("没问过时 JSON 里也必须带 %s 键", key)
		}
	}
}
