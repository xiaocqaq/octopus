package relay

import (
	"sync"
	"time"
)

// scheduledProbeResults 是各条渠道凭据最近一次定时测活的结论, 按授权主键索引。
//
// 与 route.Probes 并存而不合并: 那份按分组成员 ID 索引, 模型没被任何分组引用时就无处可落,
// 而"这个渠道的这个模型此刻通不通"是任务本身要回答的问题, 与它有没有进分组无关 ——
// 把结论只落进分组, 用户建了任务却在界面上看不到任何结论, 监控也就失去了可见形态。
//
// 只存进程内, 不落库: 结论有有效期, 重启后本来就该全部作废(与 route.Probes 同一口径),
// 落库反而要再写一套过期清理与"停机期间的空窗怎么表示"。
var (
	scheduledProbeResultMu sync.Mutex
	scheduledProbeResults  = make(map[int]ProbeResult)
)

// recordScheduledProbeResult 记下一条凭据的测活结论, 并顺手清掉已经过期的旧结论。
// 清理放在写入路径上而不是另起一个定时器: 授权是几十条的量级, 一次全表扫描比多养一个后台任务便宜,
// 也让这张表的大小天然收敛在"有效期内的结论数"。
func recordScheduledProbeResult(grantID int, result ProbeResult) {
	scheduledProbeResultMu.Lock()
	defer scheduledProbeResultMu.Unlock()

	for id, existing := range scheduledProbeResults {
		if !probeFresh(existing, result.ProbedAt) {
			delete(scheduledProbeResults, id)
		}
	}
	scheduledProbeResults[grantID] = result
}

// ScheduledProbeResults 返回给定授权中仍在有效期内的定时测活结论, 按授权主键索引。
// 过期的结论一律当没有: 与分组侧同一口径(见 freshProbes), 界面读到空结果的含义就是"该重新测一次了"。
func ScheduledProbeResults(grantIDs []int) map[int]ProbeResult {
	now := time.Now().UnixMilli()
	scheduledProbeResultMu.Lock()
	defer scheduledProbeResultMu.Unlock()

	results := make(map[int]ProbeResult, len(grantIDs))
	for _, grantID := range grantIDs {
		if result, ok := scheduledProbeResults[grantID]; ok && probeFresh(result, now) {
			results[grantID] = result
		}
	}
	return results
}
