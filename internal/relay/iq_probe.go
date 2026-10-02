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

	"github.com/bestruirui/octopus/internal/model"
	"github.com/bestruirui/octopus/internal/op"
)

// 智商探针: 用一道有唯一数值答案的题替代 probePrompt 的 "hi", 把模型的回答与标准答案比对,
// 得到一个可比较的分数。它复用测活的整条链路(取授权, 构造请求, 发送, 落结论),
// 只把"提示词"和"判分"两处换掉, 因此成本、超时、轮转、界面呈现都与普通测活一致。
//
// 为什么用数值题而不是选择题: 选择题可以被"猜", 数值题不能; 且数值答案在自由文本里
// 容易被可靠地提取出来 —— 模型常常在结尾单独给出一个数字, 即使前面写了一大段推理。

// iqAnswerPattern 从回答里提取最后一个整数(可带负号)。
//
// 只取最后一个而不是第一个: 题目要求"只给数字", 但模型常常把推理解释一遍再在结尾落答案,
// 推理过程中会先冒出若干中间数字(比如"圆苹果7 + 星桃子12"), 取第一个必然误判。
// 末尾的整数最接近模型最终表态 —— 这也是我们把"末尾数字精确匹配"定为判分方式的原因。
var iqAnswerPattern = regexp.MustCompile(`-?\d+`)

// iqProbeMaxTokens 是智商探针的响应上限。测活要"尽快出声", 所以默认只给 16 个 token;
// 智商题要模型完整作答, 16 个 token 会让它在推理中途被截断, 于是所有模型都判错, 分数失去区分度。
// 512 足以容纳一段简短推理加结尾数字, 又不至于让成本失控 —— 一次探针最多几百 token。
const iqProbeMaxTokens = 512

// iqProbeTimeout 是智商探针的硬上限。
// 比 probeTimeout(45s) 宽松一截: 45 秒是为"通道假死要快速失败"定的, 而智商题要求模型做一小段
// 推理再作答, 思考型模型在 45 秒内未必收尾。90 秒既容得下推理, 又不至于让一次探针无限占着连接。
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
var iqQuestions = []IQQuestion{
	{
		ID: "candy-21",
		Prompt: "一个黑色袋子里装着三种口味的糖果: 苹果、桃子、西瓜, 每种口味都有圆形和五角星两种形状。" +
			"具体的数量是 —— 圆形: 苹果 7 个、桃子 9 个、西瓜 8 个; 五角星: 苹果 7 个、桃子 6 个、西瓜 4 个。" +
			"糖果的形状靠手感可以分辨(摸得出是圆的还是五角星), 但味道摸不出来, 只能取出来才知道。" +
			"问: 最少要取出多少个糖果, 才能保证手里同时拥有「不同形状的苹果味和桃子味」" +
			"(即圆苹果配五角星桃子, 或者圆桃子配五角星苹果)?" +
			"请给出简要推理过程, 并在最后一行只输出这个数字。",
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

// parseIQAnswer 从模型回答里提取末尾整数。
// 提取不到(纯文字回答, 空回答)时返回 false —— 那属于"没答出可判分的答案", 记判错而不是记零分以外的语义。
//
// 先剥掉 JSON 外壳再提取: 部分上游把正文包成 {"content":"..."} 之类的结构,
// 直接对整段做整数匹配会把结构里的数字(如 usage 的 token 数)当成答案。
func parseIQAnswer(body []byte) (string, bool) {
	text := iqTextOf(body)
	matches := iqAnswerPattern.FindAllString(text, -1)
	if len(matches) == 0 {
		return "", false
	}
	// 规范化: "-0" 与 "007" 这类写法统一成标准整数字面量, 使比对不受前导零影响。
	value, err := strconv.ParseInt(matches[len(matches)-1], 10, 64)
	if err != nil {
		return "", false
	}
	return strconv.FormatInt(value, 10), true
}

// iqTextOf 尽力从响应正文里取出"模型说的话"。
//
// 先尝试按 JSON 解析并挑出常见的内容字段(OpenAI 的 choices[].message.content,
// Anthropic 风格的 content[].text, 以及若干窄接口的 content/text 字段);
// 任一步失败就退回原始正文 —— 透传路径下正文未必是 JSON, 退回原文总比丢掉回答好。
func iqTextOf(body []byte) string {
	// Content 在两种常见形状里类型不同, 所以不能共用一个字段名直接映射:
	// OpenAI 风格是 choices[].message.content 这类嵌套字符串, Anthropic 风格是顶层
	// content 为 [{type:"text",text:"..."}] 这样的块数组。同名同标签写在同一个结构体里时,
	// 编码器只认最外层那一个, 另一形状的分支永远不会被走到 —— 于是这里拆成两次解析。
	var openAIShape struct {
		Content *string `json:"content"`
		Text    *string `json:"text"`
		Choices []struct {
			Message struct {
				Content   *string `json:"content"`
				Reasoning *string `json:"reasoning_content"`
			} `json:"message"`
			Text *string `json:"text"`
		} `json:"choices"`
	}
	if err := json.Unmarshal(body, &openAIShape); err == nil {
		if len(openAIShape.Choices) > 0 {
			choice := openAIShape.Choices[0]
			// 正文优先, 正文为空时退回推理字段: 有些推理模型把全部输出放进 reasoning_content,
			// 而它里面同样会落到末尾那个数字上。
			if choice.Message.Content != nil && strings.TrimSpace(*choice.Message.Content) != "" {
				return *choice.Message.Content
			}
			if choice.Text != nil && strings.TrimSpace(*choice.Text) != "" {
				return *choice.Text
			}
			if choice.Message.Reasoning != nil {
				return *choice.Message.Reasoning
			}
		}
		if openAIShape.Content != nil {
			return *openAIShape.Content
		}
		if openAIShape.Text != nil {
			return *openAIShape.Text
		}
	}
	// Anthropic 风格: content 是块数组, 拼接其中的 text 块。
	var anthropicShape struct {
		Content []struct {
			Text *string `json:"text"`
		} `json:"content"`
	}
	if err := json.Unmarshal(body, &anthropicShape); err == nil {
		var builder strings.Builder
		for _, block := range anthropicShape.Content {
			if block.Text != nil {
				builder.WriteString(*block.Text)
			}
		}
		if builder.Len() > 0 {
			return builder.String()
		}
	}
	// 透传路径下正文未必是 JSON, 退回原文总比丢掉回答好。
	return string(body)
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
