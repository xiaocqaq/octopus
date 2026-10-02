package handlers

import (
	"github.com/bestruirui/octopus/internal/model"
	"github.com/bestruirui/octopus/internal/op"
	"github.com/bestruirui/octopus/internal/relay"
)

// 本文件负责把一条任务摊成界面上的行。
//
// 单列一个文件是因为"行的顺序"本身就是一条需要与调度对齐的约定, 散在处理器里容易被顺手改坏:
// 行的顺序与轮转顺序同源(都取 op.ScheduledProbeCredits), 界面上拖到最前的那条,
// 就是下一拍最先被测的那条。两处各排一次序, 迟早会分岔成两份不一样的顺序,
// 届时界面上的次序就只剩装饰作用。

// probeRows 把一条任务摊成界面上的行, 并带上各条凭据当下的测活结论。
// 仅摊已有凭据, 不再补占位行: 凭据被删后那条目标就应该从行里消失,
// 不留"灰掉还能恢复"的痕迹——用户点 × 就是真的删除, 不来回倒腾。
func probeRows(probe model.ScheduledProbeView) []model.ScheduledProbeRow {
	credits := op.ScheduledProbeCredits(probe.ScheduledProbe)
	results := relay.ScheduledProbeResults(creditGrantIDs(credits))

	rows := make([]model.ScheduledProbeRow, 0, len(credits))
	for _, credit := range credits {
		rows = append(rows, withProbeResult(scheduledProbeRowOf(credit), results))
	}
	return rows
}

// probeRowsOf 把刚测出的结论整理成与列表同形状的行, 让前端可以直接就地更新那一行。
// 复用行的形状而不是另造一个响应: 前端拿到之后要做的正是"用新结论替换旧行",
// 形状一致就不必写第二套更新逻辑。
func probeRowsOf(probe model.ScheduledProbe, results []relay.ProbeResult) []model.ScheduledProbeRow {
	index := creditIndex(probe)
	rows := make([]model.ScheduledProbeRow, 0, len(results))
	for _, result := range results {
		credit, ok := index[result.ItemID]
		if !ok {
			// 结论来自一条此刻已不在任务里的凭据(刚被删掉或被排除): 出这一行只会让界面多出一行孤儿。
			continue
		}
		row := scheduledProbeRowOf(credit)
		row.Probed = true
		row.OK = result.OK
		row.LatencyMS = result.LatencyMS
		row.Message = result.Message
		row.ProbedAt = result.ProbedAt
		row.IQAsked, row.IQAnswer, row.IQCorrect = iqFieldsOf(result)
		rows = append(rows, row)
	}
	return rows
}

// scheduledProbeRowOf 把一条凭据摊成一行, 此时还不带测活结论。
func scheduledProbeRowOf(credit op.ScheduledProbeCredit) model.ScheduledProbeRow {
	return model.ScheduledProbeRow{
		GrantID:     credit.GrantID,
		ChannelID:   credit.Target.ChannelID,
		ChannelName: op.ChannelNameOf(credit.Target.ChannelID),
		ModelName:   credit.Target.ModelName,
		KeyName:     credit.KeyName,
	}
}

// withProbeResult 把结论填进行里; 没有结论时原样返回, 界面据此显示"尚未测出结果"。
func withProbeResult(row model.ScheduledProbeRow, results map[int]relay.ProbeResult) model.ScheduledProbeRow {
	result, ok := results[row.GrantID]
	if !ok {
		return row
	}
	row.Probed = true
	row.OK = result.OK
	row.LatencyMS = result.LatencyMS
	row.Message = result.Message
	row.ProbedAt = result.ProbedAt
	row.IQAsked, row.IQAnswer, row.IQCorrect = iqFieldsOf(result)
	return row
}

// iqFieldsOf 把智商结论摊成三个平铺字段。
//
// 没问过(IQ 为 nil)与"问了但答不出可判分的答案"(IQ 非 nil、Answer 为空)在界面上是两种状态:
// 前者这一格整个不出现, 后者要显示成"降智"。两者的 Answer 都是空串, 因此"问没问过"必须由
// 单独一个布尔承担, 而不是靠 Answer 是否为空反推 —— 反推会把后一种状态静默吞成"没问过"。
func iqFieldsOf(result relay.ProbeResult) (bool, string, bool) {
	if result.IQ == nil {
		return false, "", false
	}
	return true, result.IQ.Answer, result.IQ.Correct
}

// creditGrantIDs 取出凭据列表里的授权主键, 供一次性批量查结论。
func creditGrantIDs(credits []op.ScheduledProbeCredit) []int {
	grantIDs := make([]int, 0, len(credits))
	for _, credit := range credits {
		grantIDs = append(grantIDs, credit.GrantID)
	}
	return grantIDs
}

// creditIndex 是"授权主键 → 它所在的凭据"的索引, 供落点回填渠道与模型。
// 结论本身只记授权主键, 要再查一次才知道它属于哪个渠道的哪个模型。
func creditIndex(probe model.ScheduledProbe) map[int]op.ScheduledProbeCredit {
	credits := op.ScheduledProbeCredits(probe)
	index := make(map[int]op.ScheduledProbeCredit, len(credits))
	for _, credit := range credits {
		index[credit.GrantID] = credit
	}
	return index
}
