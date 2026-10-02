package relay

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/bestruirui/octopus/internal/model"
	"github.com/bestruirui/octopus/internal/op"
)

// 智商探针: 用一道有唯一数值答案的题替代 probePrompt 的 "hi", 把模型的回答与标准答案比对,
// 得到一个可比较的分数。它复用测活的整条链路(取授权, 构造请求, 发送, 落结论),
// 只把"提示词"和"判分"两处换掉, 因此成本、超时、轮转、界面呈现都与普通测活一致。
//
// 为什么用数值题而不是选择题: 选择题可以被"猜", 数值题不能; 且数值答案在自由文本里
// 容易被可靠地提取出来。
//
// 但"可靠地提取"有个前提 —— 得先认出上游把回答放在哪儿。三家协议放的位置各不相同
// (见 iqReplyOf), 推理模型还会把草稿一并吐出来; 取错位置的代价不是"少判一分", 而是把 token 数
// 当成答案摆到界面上(真实踩过, 见 parseIQAnswer 的注释)。

// iqAnswerPattern 匹配文本里的整数(可带负号)。
var iqAnswerPattern = regexp.MustCompile(`-?\d+`)

// iqAnswerMarkerPattern 匹配"模型自报答案"的说法, 取最后一处: 结论总落在推理之后。
//
// 题面要求只给数字, 但总有不听话的模型先写一段解释再落结论。对这种回答, 认它自报的那句
// ("答案是 21"、"最少需要 21 个"、"answer is 21")比按位置瞎猜可靠得多 —— 中间允许几个
// 非数字字符, 让"最少需要取出 21 个"这类正常说法也能命中, 但不跨行、不超过 10 个字符,
// 免得把两句不相干的话接在一起。
//
// 这里只收"宣告结论"的词, 刻意不收"合计/总共/一共/total": 那些是求和词, 推理中间到处是
// 合计, 而它们算出来的往往是中途的错数 —— 真实踩过的 5099 就是这么冒出来的("…12 个星, 合计 5099")。
var iqAnswerMarkerPattern = regexp.MustCompile(`(?i)(?:答案|answer|最少|至少|minimum|at least)[^\d\n]{0,10}?(-?\d+)`)

// iqTerseAnswerRunes 是"整段就是一次直接作答"的长度上限。
//
// 题面已明确"不需要任何思考, 只要答案的数字", 因此合规回答就是一个数字; 这条上限只用来
// 容忍少量包装("答案是 21"、"21 个")。超出上限的回答不是"作答"而是"论述", 改走自报答案与
// 末行判断 —— 确认它有没有交代结论, 而不是从字里行间随便挑一个数。
const iqTerseAnswerRunes = 24

// iqProbeMaxTokens 是智商探针的响应上限。合规回答只有几个 token, 但这个上限不是按合规回答定的:
// 思考型模型未必听话, 仍会在内部推理上花掉一截预算。真实响应取样里约四成的回答在 512 上限处
// 被截断(stop_reason=max_tokens), 草稿写了一半、答案还没出口, 于是它们全被记成"没答出" ——
// 那不是笨, 是预算不够, 结论失真。2048 让这类模型有机会把话说完, 同时仍封住单次探针的成本。
const iqProbeMaxTokens = 2048

// iqProbeTimeout 是智商探针的硬上限。
// 比 probeTimeout(45s) 宽松一截: 45 秒是为"通道假死要快速失败"定的, 而思考型模型即便被要求
// 直接给答案也可能先想一会儿, 45 秒内未必收尾。90 秒既容得下这段思考, 又不至于让一次探针无限占着连接。
const iqProbeTimeout = 90 * time.Second

// DefaultIQQuestionID 返回当前默认抽的那道题。
// 导出给处理器: 题号属于题库的内部约定, 处理器只该转述"测的是哪道题", 不该自己写死一个字符串 ——
// 将来题库变长、出题策略改成轮换时, 只有这里要动。
func DefaultIQQuestionID() string { return iqDefaultQuestionID }

// IQQuestion 是一道智商探针题。
//
// 题目, 答案与判分口径放在同一条记录里, 新增题目只需往题库追加一条, 不必改判分逻辑:
// 这是"预留题库结构"的最小形态 —— 一个切片加一个字段。
type IQQuestion struct {
	// ID 是题目的稳定标识(用作结论里的题号), 题库重排也不应改变它。
	ID string `json:"id"`
	// Prompt 是发给模型的完整题面。含"只输出数字"的指令, 使提取环节尽量少歧义。
	Prompt string `json:"prompt"`
	// Answer 是标准答案, 以字符串存放: 判分是文本层面的精确匹配, 用它做同口径的比较。
	Answer string `json:"answer"`
}

// iqQuestions 是内置题库。目前只有一道题 —— 用户原始那道"糖果题":
// 黑色袋子里三种口味(苹果/桃子/西瓜)各有圆与五角星两种形状, 形状可凭手感分辨,
// 问最少取几个才能保证同时拿到"不同形状的苹果味和桃子味"。
//
// 答案 21 的推导(供后来者核对, 也是这道题唯一的判分依据):
// 因为形状摸得出来, 取的人可以**分别决定**取几个圆形、几个五角星, 所以要从"对手最优地摆放"
// 的角度找最小可行对 (a, b), 使任意 a 个圆形 + b 个五角星里都必然出现"圆苹果+星桃子"或"圆桃子+星苹果"。
// 可证 (a,b) = (9,12) 可行: 三个口味合计 24 圆 17 星, 取 9 圆 12 星时,
// 若拿不到目标组合, 则"有圆苹果 ⟹ 无星桃子"且"有圆桃子 ⟹ 无星苹果";
// 星桃子与星苹果都缺时星至多只有 4 个西瓜, 与取了 12 个星矛盾, 故必然出现目标组合。
// 而 (9,11) 不可行: 对手可以摆成"圆里只有苹果桃子"(7+9=16≥9, 且不给西瓜)、
// "星里只有苹果西瓜"(7+4=11), 此时圆苹果在手却无星桃子, 圆桃子在手却无星苹果。
// 穷举验证脚本见 .probe-test/candy.js(模型B: 选 a 圆 + b 星, 最小可行 n = 21, a=9, b=12)。
//
// 注意 29 这个常见错答, 不要把标准答案改成它: 29 是"一次盲抓"读法的答案 —— 题面里
// "不同的形状靠手感可以分辨"正是为了排除这个读法(摸得出形状就能分别取圆的与五角星的)。
// 实测确实有模型按盲抓读法答 29, 那是它没读进形状可辨这个条件, 正是本题要区分的地方;
// 若把答案改成 29, 读得仔细的模型反而判错(脚本 .probe-test/candy29.py 是按盲抓读法算的, 得 29)。
var iqQuestions = []IQQuestion{
	{
		ID: "candy-21",
		Prompt: "在一个黑色的袋子里放有三种口味的糖果, 每种糖果有两种不同的形状(圆形和五角星形, 不同的形状靠手感可以分辨)。" +
			"现已知不同口味的糖和不同形状的数量统计如下表。参赛者需要在活动前决定摸出的糖果数目, 那么, 最少取出多少个糖果才能保证手中同时拥有不同形状的苹果味和桃子味的糖?" +
			"(圆形苹果配五角星桃子, 或圆形桃子配五角星苹果, 均满足要求)" +
			"圆形糖果: 苹果味 7 个, 桃子味 9 个, 西瓜味 8 个; 五角星形糖果: 苹果味 7 个, 桃子味 6 个, 西瓜味 4 个。" +
			"只要给我答案, 你不需要任何思考, 我只需要答案的数字。",
		Answer: "21",
	},
}

// iqQuestionByID 按 ID 取题, 题库里没有该 ID 时返回 false。
// 结论里记的是题号而非题面: 题面可能随措辞调整, 题号不会, 拿题号才能把历史结论与当时的题对上。
func iqQuestionByID(id string) (IQQuestion, bool) {
	for _, question := range iqQuestions {
		if question.ID == id {
			return question, true
		}
	}
	return IQQuestion{}, false
}

// parseIQAnswer 从模型回答里提取它作答的数字。
//
// 要取的是"模型表态的那个数", 不是"文本里出现过的某个数"。因此取值分三层, 一层比一层保守:
//
//  1. 整段回答很短(≤ iqTerseAnswerRunes): 它就是一次直接作答, 取其中的整数;
//  2. 回答很长: 认它自报的结论("答案是 21"这类), 取最后一处 —— 结论总在推理之后;
//  3. 回答很长但分了多行, 且末行本身很短: 按"最后一行只输出数字"的惯例取末行。
//
// 三层都不成立就返回 ("", false), 如实记"没答出可判读的数字"。
//
// 这里曾经是"取全篇最后一个整数", 实测被咬过一次: 模型长篇输出后落在 5099 上, 界面把 5099
// 当成"模型的答案"摆出来。而 5099 根本不是模型说的话, 是它算式里的一个中间结果 —— 题面自己
// 就给足了原料(7、9、8、6、4 随便一算就是一堆数字), 按位置取数等于碰运气。判错的结论不变,
// 但理由必须是真的: "它没照要求作答"与"它答了 5099"是两件事。
//
// 正文的外壳由 iqReplyOf 负责剥掉: 不先认信封就直接做整数匹配, 读出来的是 usage 里的 token 数。
func parseIQAnswer(body []byte) (string, bool) {
	text := strings.TrimSpace(iqReplyOf(body))
	if text == "" {
		return "", false
	}
	// 按字符数而不是字节数比较: 中文一个字三个字节, 按字节算会把"答案是 21"这种合规回答误杀。
	if utf8.RuneCountInString(text) <= iqTerseAnswerRunes {
		return iqLastNumber(text)
	}
	if value, ok := iqLastMarkerNumber(text); ok {
		return value, true
	}
	lines := strings.Split(text, "\n")
	last := strings.TrimSpace(lines[len(lines)-1])
	if len(lines) > 1 && last != "" && utf8.RuneCountInString(last) <= iqTerseAnswerRunes {
		if value, ok := iqLastNumber(last); ok {
			return value, true
		}
	}
	return "", false
}

// iqLastNumber 取文本里最后一个整数。顺带规范化: "-0"、"007" 这类写法统一成标准整数字面量,
// 使比对不受前导零影响。
func iqLastNumber(text string) (string, bool) {
	matches := iqAnswerPattern.FindAllString(text, -1)
	if len(matches) == 0 {
		return "", false
	}
	value, err := strconv.ParseInt(matches[len(matches)-1], 10, 64)
	if err != nil {
		return "", false
	}
	return strconv.FormatInt(value, 10), true
}

// iqLastMarkerNumber 取最后一处"自报答案"里的数字: 推理在前、结论在后, 所以取最后一处。
func iqLastMarkerNumber(text string) (string, bool) {
	matches := iqAnswerMarkerPattern.FindAllStringSubmatch(text, -1)
	if len(matches) == 0 {
		return "", false
	}
	return iqLastNumber(matches[len(matches)-1][1])
}

// iqReplyOf 取出"模型给出的可见作答", 认不出就返回空串。
//
// 三种信封在真实上游里都出现过, 回答放的位置各不相同:
//   - OpenAI chat completions: choices[].message.content(有的上游给字符串, 有的给 parts 数组)
//     以及 choices[].text;
//   - OpenAI Responses API:   output[] 里 type != "reasoning" 的项, 取其 content[].text;
//   - Anthropic messages:     content[] 块数组里非 thinking 的 text 块;
//   - 若干窄接口把正文直接放在顶层 content / text。
//
// 两条铁律:
//   - 推理不是作答。reasoning_content、output[].summary、thinking 块都是草稿, 一律不取 ——
//     草稿里全是中间数, 取它等于把"想到哪儿了"当成"答了什么"(实测: 推理里从不出现宣告结论的话)。
//   - 是 JSON 就绝不退回原文。旧实现在解析未命中时 return string(body), 整段响应(含 usage)
//     于是进入整数匹配, 读出 token 数当答案 —— 界面上的"答了 5099"就是这么来的。
//     只有压根不是 JSON 的透传正文才按原文处理。
func iqReplyOf(body []byte) string {
	if !json.Valid(body) {
		// 透传路径下正文未必是 JSON, 退回原文总比丢掉回答好。
		return string(body)
	}
	// 合法 JSON 的字符串本身就是正文, 例如 `"21"`。
	var bare string
	if err := json.Unmarshal(body, &bare); err == nil {
		return bare
	}
	var chat struct {
		Content *string `json:"content"`
		Text    *string `json:"text"`
		Choices []struct {
			Message struct {
				Content json.RawMessage `json:"content"`
			} `json:"message"`
			Text *string `json:"text"`
		} `json:"choices"`
	}
	if err := json.Unmarshal(body, &chat); err == nil {
		for _, choice := range chat.Choices {
			if text := iqContentOf(choice.Message.Content); text != "" {
				return text
			}
			if choice.Text != nil && strings.TrimSpace(*choice.Text) != "" {
				return *choice.Text
			}
		}
		if chat.Content != nil && strings.TrimSpace(*chat.Content) != "" {
			return *chat.Content
		}
		if chat.Text != nil && strings.TrimSpace(*chat.Text) != "" {
			return *chat.Text
		}
	}
	// Anthropic 风格: content 是块数组, 拼接其中非 thinking 的 text 块。
	var anthropic struct {
		Content json.RawMessage `json:"content"`
	}
	if err := json.Unmarshal(body, &anthropic); err == nil {
		if text := iqBlockTextOf(anthropic.Content); text != "" {
			return text
		}
	}
	// OpenAI Responses API: output[] 里的 reasoning 项整项跳过(它的 summary 是推理摘要)。
	var responses struct {
		Output []struct {
			Type    string `json:"type"`
			Content []struct {
				Text *string `json:"text"`
			} `json:"content"`
		} `json:"output"`
	}
	if err := json.Unmarshal(body, &responses); err == nil {
		var builder strings.Builder
		for _, item := range responses.Output {
			if item.Type == "reasoning" {
				continue
			}
			for _, part := range item.Content {
				if part.Text != nil {
					builder.WriteString(*part.Text)
				}
			}
		}
		if builder.Len() > 0 {
			return builder.String()
		}
	}
	// 是 JSON 但认不出正文: 返回空串, 绝不把整段响应(usage 等)当成模型说的话。
	return ""
}

// iqContentOf 处理 message.content 的两种类型: 字符串, 或 parts 数组(其中的推理 part 跳过)。
func iqContentOf(raw json.RawMessage) string {
	if len(raw) == 0 {
		return ""
	}
	var text string
	if err := json.Unmarshal(raw, &text); err == nil {
		return text
	}
	var parts []struct {
		Type string  `json:"type"`
		Text *string `json:"text"`
	}
	if err := json.Unmarshal(raw, &parts); err != nil {
		return ""
	}
	var builder strings.Builder
	for _, part := range parts {
		if part.Type == "thinking" || part.Type == "reasoning" {
			continue
		}
		if part.Text != nil {
			builder.WriteString(*part.Text)
		}
	}
	return builder.String()
}

// iqBlockTextOf 拼接 Anthropic 风格块数组里的 text 块, 跳过 thinking / redacted_thinking。
func iqBlockTextOf(raw json.RawMessage) string {
	if len(raw) == 0 {
		return ""
	}
	var blocks []struct {
		Type string  `json:"type"`
		Text *string `json:"text"`
	}
	if err := json.Unmarshal(raw, &blocks); err != nil {
		return ""
	}
	var builder strings.Builder
	for _, block := range blocks {
		if block.Type == "thinking" || block.Type == "redacted_thinking" {
			continue
		}
		if block.Text != nil {
			builder.WriteString(*block.Text)
		}
	}
	return builder.String()
}

// gradeIQAnswer 判定一次回答是否正确。
// 判分口径就是用户选定的"末尾数字精确匹配": 提取末尾整数, 与标准答案做字符串比较(两边都已规范化)。
func gradeIQAnswer(question IQQuestion, body []byte) (IQResult, bool) {
	extracted, ok := parseIQAnswer(body)
	if !ok {
		return IQResult{}, false
	}
	return IQResult{QuestionID: question.ID, Answer: extracted, Correct: extracted == question.Answer}, true
}

// ProbeIQGrant 对一条渠道授权出一道智商题并判分。
//
// 为什么另起一个函数而不是给 ProbeChannelGrant 加分支: 两者在"要什么"上分道扬镳 ——
// 测活问的是"通不通, 多快", 智商探针问的是"答对没有", 后者需要读回正文并判分,
// 且题面与 token 上限都不同。共用的部分(取授权 → 取渠道 → 建出站 → 建请求 → 定时 → 发送)
// 逐行相同, 因此这里复刻同一套前置校验, 只在"发什么"和"怎么判"两处换实现。
//
// 返回的 ProbeResult 与测活同构: OK 表示"这次请求成功了"(不代表答对), IQ 携带判分结论。
// 答错与答不出都算 OK —— 通道是好的, 只是模型没答对, 把这两件事混进 OK 会让选路把
// "模型笨"当成"通道坏"而降档, 那是两回事。
func ProbeIQGrant(ctx context.Context, grantID int, questionID string) ProbeResult {
	question, ok := iqQuestionByID(questionID)
	if !ok {
		return ProbeResult{ItemID: grantID, Message: "unknown iq question: " + questionID, ProbedAt: time.Now().UnixMilli()}
	}
	grant, err := op.ChannelGrantGet(grantID)
	if err != nil {
		return ProbeResult{ItemID: grantID, Message: err.Error(), ProbedAt: time.Now().UnixMilli()}
	}
	channelModel := grant.ChannelModel
	channelKey := grant.ChannelKey
	if channelModel == nil || channelKey == nil {
		return ProbeResult{ItemID: grantID, Message: "channel grant is incomplete", ProbedAt: time.Now().UnixMilli()}
	}
	channel, err := op.ChannelGet(channelModel.ChannelID)
	if err != nil {
		return ProbeResult{ItemID: grantID, Message: err.Error(), ProbedAt: time.Now().UnixMilli()}
	}
	if !channel.Enabled {
		return ProbeResult{ItemID: grantID, Message: "channel is disabled", ProbedAt: time.Now().UnixMilli()}
	}
	outbound, _, passthrough, err := buildOutbound(channel, grant, *channelKey, model.ProtocolOpenAIChatCompletion)
	if err != nil {
		return ProbeResult{ItemID: grantID, Message: err.Error(), ProbedAt: time.Now().UnixMilli()}
	}
	// 非流式: 要判分就得拿到完整的回答正文, 非流式一次响应就是全文。
	// 走流式则要拼齐整个事件流, 平白多一层解析, 换不来任何东西。
	request, err := buildProbeRequest(ctx, outbound, channel, channelModel.Name, question.Prompt, iqProbeMaxTokens, false)
	if err != nil {
		return ProbeResult{ItemID: grantID, Message: err.Error(), ProbedAt: time.Now().UnixMilli()}
	}
	// 超时比测活宽松: 测活只要上游"出声", 45 秒的硬上限是给"假死"用的;
	// 智商题要模型完整作答(含一段推理), 拿同一把尺子会把慢而正确的模型判成超时。
	timeout := time.Duration(model.DefaultGroupRelayConfig().MemberNonStreamResponseTimeoutSeconds) * time.Second
	if timeout > iqProbeTimeout {
		timeout = iqProbeTimeout
	}
	probeCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	startedAt := time.Now()
	body, probeErr := runProbeBody(probeCtx, outbound, channel, channelModel.Name, request, passthrough, false)
	latency := time.Since(startedAt).Milliseconds()
	if probeErr != nil {
		message := probeErr.Error()
		if errors.Is(probeCtx.Err(), context.DeadlineExceeded) {
			message = fmt.Sprintf("probe timeout after %s", timeout)
		}
		return ProbeResult{ItemID: grantID, LatencyMS: latency, Message: message, ProbedAt: time.Now().UnixMilli()}
	}
	result := ProbeResult{ItemID: grantID, OK: true, LatencyMS: latency, ProbedAt: time.Now().UnixMilli()}
	if graded, ok := gradeIQAnswer(question, body); ok {
		result.IQ = &graded
	} else {
		// 拿到了回答但里面没有可判分的整数(模型只写了文字, 或回答被截断在数字之前)。
		// 记一条答案为空、判错的结论, 而不是留 nil: nil 表示"没问", 而这里是"问了, 没答对"。
		result.IQ = &IQResult{QuestionID: question.ID, Correct: false}
	}
	return result
}

// ProbeItemIQ 对分组内的单个成员出一道智商题, 并把结论落进路由状态。
//
// 与 ProbeItem 并列而不是给它加个"要不要出题"的开关: 两者问的是两件事 ——
// 那条问"这条通道此刻通不通"(结论是通/不通, 快不快), 这条问"这个模型此刻笨不笨"(结论是对/错)。
// 合成一个入口就得让调用方先想清楚自己要哪种语义, 而界面上的两个按钮本来就已经想清楚了。
//
// 落点复用 landProbe(..., false): 与人工测活同一条路径 —— 结论进 route.Probes 供界面与选路消费,
// 但失败不冷却。一次答错不该把成员关进小黑屋, 那是"模型能力"而不是"通道故障"。
func ProbeItemIQ(ctx context.Context, groupID, itemID int, questionID string) (ProbeResult, error) {
	group, err := op.GroupGet(groupID)
	if err != nil {
		return ProbeResult{}, err
	}
	item := itemOf(group, itemID)
	if item.ID == 0 {
		return ProbeResult{}, fmt.Errorf("group item not found")
	}

	result := ProbeIQGrant(ctx, item.ChannelGrantID, questionID)
	result.GroupID = group.ID
	result.ItemID = itemID
	// 有效期与人工测活同一条规则: 有任务在监控这条授权时挂到下一轮复测之前, 否则用兜底值。
	return landProbe(group, itemID, withProbeExpiry(result, item.ChannelGrantID), false), nil
}

// ProbeGroupIQ 一键把分组内全部成员(或指定子集)各问一道智商题, 顺序与目标顺序一致。
//
// 与 ProbeGroup 走同一条并发约束(probeByChannel): 智商题的响应体是测活的上百倍,
// 一个分组几十个成员全并发打出去, 上游那边看到的就是一次突发。
// 单个成员失败不影响其余成员: 一个模型答不出来, 不该让整轮测试没有结论。
func ProbeGroupIQ(ctx context.Context, groupID int, itemIDs []int, questionID string) ([]ProbeResult, error) {
	group, err := op.GroupGet(groupID)
	if err != nil {
		return nil, err
	}
	targets := probeTargetsOf(group, itemIDs)
	if len(targets) == 0 {
		return []ProbeResult{}, nil
	}
	return probeByChannel(probeChannelIDsOf(group, targets), func(index int) ProbeResult {
		itemID := targets[index]
		result, err := ProbeItemIQ(ctx, groupID, itemID, questionID)
		if err != nil {
			result = ProbeResult{GroupID: groupID, ItemID: itemID, Message: err.Error(), ProbedAt: time.Now().UnixMilli()}
		}
		return result
	}), nil
}
