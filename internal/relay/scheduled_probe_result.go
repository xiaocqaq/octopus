package relay

import "sync"

// scheduledProbeResults 是各条渠道凭据最近一次定时测活的结论, 按授权主键索引。
//
// 与 route.Probes 并存而不合并: 那份按分组成员 ID 索引, 模型没被任何分组引用时就无处可落,
// 而"这个渠道的这个模型此刻通不通"是任务本身要回答的问题, 与它有没有进分组无关 ——
// 把结论只落进分组, 用户建了任务却在界面上看不到任何结论, 监控也就失去了可见形态。
//
// 只存进程内, 不落库: 重启后本来就该全部作废(与 route.Probes 同一口径),
// 落库反而要再写一套过期清理与"停机期间的空窗怎么表示"。
var (
	scheduledProbeResultMu sync.Mutex
	scheduledProbeResults  = make(map[int]ProbeResult)
)

// recordScheduledProbeResult 记下一条凭据的测活结论, 新的直接覆盖旧的。
//
// 不再按有效期清理旧结论: 结论要一直留到下次测出新的为止(见 ScheduledProbeResults)。
// 表的规模因此收敛在"被监控过的凭据数"上 —— 那是几十条的量级, 不值得为它养一个清理器,
// 何况清理器一停, 用户看到的就是徽标莫名其妙地变空。
func recordScheduledProbeResult(grantID int, result ProbeResult) {
	scheduledProbeResultMu.Lock()
	defer scheduledProbeResultMu.Unlock()

	scheduledProbeResults[grantID] = result
}

// ScheduledProbeResults 返回给定授权的定时测活结论, 按授权主键索引。
//
// 结论不设有效期: 要显示的是"最近一次测出来是什么", 而不是一块按固定时限自己变空的牌子。
// 固定 10 分钟在"一拍只测一条凭据"的节奏下本来就不够用 —— 6 条凭据的任务配 10 分钟间隔,
// 每条要等 60 分钟才轮到一次, 徽标有 50 分钟是空白, 看起来像监控没在跑。
// 结论本身带着 ProbedAt, 界面照实显示"多久以前"; 该不该采信这块牌子由看的人判断,
// 不该由一条写死的时限替他决定。
//
// 这不影响选路: 选路读的是 route.Probes 且另有 probeResultTTL 把关(见 probeVote) ——
// "让一块牌子消失"与"让一条通道失去加权"是两件事, 此前把它们绑在同一个常量上,
// 于是想留住牌子就只能连加权一起留住。
func ScheduledProbeResults(grantIDs []int) map[int]ProbeResult {
	scheduledProbeResultMu.Lock()
	defer scheduledProbeResultMu.Unlock()

	results := make(map[int]ProbeResult, len(grantIDs))
	for _, grantID := range grantIDs {
		if result, ok := scheduledProbeResults[grantID]; ok {
			results[grantID] = result
		}
	}
	return results
}

// ForgetScheduledProbeResults 丢掉这些授权上的定时测活结论。
//
// 任务被删掉时调用: 结论不设有效期之后, 没人再测的凭据会永远挂着最后一次的绿或红,
// 而"这条通道还通不通"这件事已经没有人负责回答了 —— 留着比空着更容易误导。
func ForgetScheduledProbeResults(grantIDs []int) {
	scheduledProbeResultMu.Lock()
	defer scheduledProbeResultMu.Unlock()

	for _, grantID := range grantIDs {
		delete(scheduledProbeResults, grantID)
	}
}
