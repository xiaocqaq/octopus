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
func probeRows(probe model.ScheduledProbeView) []model.ScheduledProbeRow {
	credits := op.ScheduledProbeCredits(probe.ScheduledProbe)
	results := relay.ScheduledProbeResults(creditGrantIDs(credits))

	rows := make([]model.ScheduledProbeRow, 0, len(credits)+len(probe.Targets))
	for _, credit := range credits {
		rows = append(rows, withProbeResult(scheduledProbeRowOf(credit), results))
	}
	return append(rows, placeholderRows(probe.Targets)...)
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
	return row
}

// placeholderRows 为"当下没有可测凭据"的目标补一行占位, grant_id 为 0。
// 界面据 grant_id 为 0 把那一行的手动测试与删除按钮置灰并说明原因 ——
// 整条目标凭空消失, 用户只会以为是自己没配上。
//
// 这些行排在所有凭据行之后, 而不是插在凭据行之间: 它不对应任何一条凭据,
// 排不进用户拖出来的那个顺序里, 硬插进去会让拖动后的落位莫名其妙。
func placeholderRows(targets []model.ScheduledProbeTarget) []model.ScheduledProbeRow {
	rows := make([]model.ScheduledProbeRow, 0, len(targets))
	for _, target := range targets {
		if len(op.ScheduledProbeGrantIDs(target)) > 0 {
			continue
		}
		rows = append(rows, model.ScheduledProbeRow{
			ChannelID:   target.ChannelID,
			ChannelName: op.ChannelNameOf(target.ChannelID),
			ModelName:   target.ModelName,
		})
	}
	return rows
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
