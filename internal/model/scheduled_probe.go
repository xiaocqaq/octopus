package model

import "time"

// 定时测活时间窗的星期掩码位: 第 0 位为周一, 依次排到第 6 位为周日。
// 与 time.Weekday(周日为 0)刻意错开, 而与界面上的星期顺序一致: 界面从周一开始列,
// 掩码按同一个顺序排位, 前后端都不必为"周日排在最前"多写一次翻译。
const (
	WeekdayMonday    = 1 << 0
	WeekdayTuesday   = 1 << 1
	WeekdayWednesday = 1 << 2
	WeekdayThursday  = 1 << 3
	WeekdayFriday    = 1 << 4
	WeekdaySaturday  = 1 << 5
	WeekdaySunday    = 1 << 6
	// WeekdayAll 是七天全选, 供界面上的"全选"按钮使用。
	WeekdayAll = WeekdayMonday | WeekdayTuesday | WeekdayWednesday |
		WeekdayThursday | WeekdayFriday | WeekdaySaturday | WeekdaySunday
)

// 定时测活的间隔边界与默认值。
// 下界取 1 分钟而非 0: 0 分钟意味着每一拍都测, 那是压测而不是"定时"监控;
// 上界取一天: 比一天更长的间隔已经等于不再监控, 用启用开关表达更直白。
const (
	ScheduledProbeMinIntervalMinutes     = 1
	ScheduledProbeMaxIntervalMinutes     = 1440
	ScheduledProbeDefaultIntervalMinutes = 10
)

// ScheduledProbe 是一条定时测活任务: 按固定间隔对指定渠道下的指定模型打一次真实上游请求。
//
// 任务只记 (渠道, 模型), 不记凭据。同一模型在同一渠道下可能挂多条凭据(授权), 那是"怎么转发"的细节,
// 而用户要盯的是"这个渠道的这个模型此刻还活着吗"。多条凭据由调度器按轮转逐次探测, 一拍只测一条授权:
// 测活打的是真实上游, 一拍把全部凭据一起打会在上游留下一次突发流量, 结论也不再是同一时刻的快照。
//
// 时间窗留空(星期掩码为 0)表示不限制; 填了掩码才按窗口跳过窗口之外的时间。
type ScheduledProbe struct {
	ID        int `json:"id" gorm:"primaryKey"`             // 任务主键。
	ChannelID int `json:"channel_id" gorm:"not null;index"` // 被监控的渠道 ID。
	// ModelName 是被监控的上游模型名称, 必须属于 ChannelID 指向的渠道; 归属关系在提交时校验。
	// 存名称而不是渠道模型主键: 渠道页整体替换模型集合时会重建主键, 按名称存的任务不会因此失效。
	ModelName       string `json:"model_name" gorm:"not null"`
	IntervalMinutes int    `json:"interval_minutes" gorm:"not null;default:10"` // 两次探测之间的间隔分钟数。
	Enabled         bool   `json:"enabled" gorm:"not null;default:true"`        // 是否参与调度; 停用后不再探测, 配置保留。
	// Weekdays 是时间窗的星期掩码, 0 表示不限制星期(全天可测)。
	Weekdays int `json:"weekdays" gorm:"not null;default:0"`
	// StartHour 与 EndHour 是时间窗的起止整点(0-23), 只在 Weekdays 非 0 时有意义。
	// 起止相同视为全天: 界面上把 09:00-09:00 读成"只在 9 点这一小时"会让人意外,
	// 而"整天"更自然, 也让"选了星期但不限时段"有地方可表达。
	StartHour int `json:"start_hour" gorm:"not null;default:0"`
	EndHour   int `json:"end_hour" gorm:"not null;default:0"`
	// 时间戳交给 GORM 自动维护, 用毫秒精度与本项目其余时间字段(如测活结论的 ProbedAt)对齐。
	// 不用默认的秒级: 前端按毫秒渲染, 秒级会在界面上显示成 1970 年附近的时刻。
	CreatedAt int64 `json:"created_at" gorm:"autoCreateTime:milli"`
	UpdatedAt int64 `json:"updated_at" gorm:"autoUpdateTime:milli"`
}

// WindowOpen 判断给定时刻是否落在该任务的时间窗内。
//
// 跨午夜的窗口(如 22:00-05:00)横跨两天, 掩码记的是"窗口开始的那一天":
// 选了周一至周五的 22:00-05:00, 指的是周一到周五每天晚上十点开始的那一段,
// 于是周六凌晨 0-5 点仍算在周五那次窗口内 —— 这是排班语义的自然读法,
// 否则用户得把周六也勾上才能覆盖周五夜里, 反而要在界面上解释半天。
func (probe ScheduledProbe) WindowOpen(at time.Time) bool {
	// 掩码为 0 表示没有设时间窗: 全天可测。用 0 而不是另加一个开关位, 是为了让"没选任何星期"与
	// "不限制星期"在存储上就是同一件事, 不必再多一个可能与掩码打架的字段。
	if probe.Weekdays == 0 {
		return true
	}
	// time.Weekday 以周日为 0, 掩码以周一为第 0 位, 故先加 6 取模对齐。
	day := (int(at.Weekday()) + 6) % 7
	hour := at.Hour()

	// 起点与终点相同视为整天: 界面上 09:00-09:00 若读成"只在 9 点这一小时"会让人意外。
	if probe.StartHour == probe.EndHour {
		return probe.hasWeekday(day)
	}
	// 跨午夜: 前半段(深夜)仍属窗口开始的那一天, 后半段(凌晨)按前一天查掩码。
	if probe.StartHour > probe.EndHour {
		return probe.crossMidnightOpen(day, hour)
	}
	return hour >= probe.StartHour && hour < probe.EndHour && probe.hasWeekday(day)
}

// hasWeekday 判断第 day 位(周一为 0)是否被选中。
func (probe ScheduledProbe) hasWeekday(day int) bool {
	return probe.Weekdays&(1<<uint(day)) != 0
}

// crossMidnightOpen 判断跨午夜窗口在给定星期与整点是否开放。
// 拆出来只为让 WindowOpen 的三种情形各自成段, 判定规则仍只有这一处。
func (probe ScheduledProbe) crossMidnightOpen(day, hour int) bool {
	if hour >= probe.StartHour {
		return probe.hasWeekday(day)
	}
	// 凌晨段属于前一天开始的那次窗口: 选了周一至周五的 22:00-05:00, 周六凌晨仍算周五那一次。
	return hour < probe.EndHour && probe.hasWeekday((day+6)%7)
}

// ScheduledProbeView 是定时测活任务的列表项: 任务配置加两处现算的展示字段。
// 这两个字段都随渠道配置变动, 不落库: 存下来就得在每次渠道改动后跟着刷新一遍。
type ScheduledProbeView struct {
	ScheduledProbe
	ChannelName string `json:"channel_name"` // 渠道名称; 渠道已删除时为空, 界面据此显示主键。
	GrantCount  int    `json:"grant_count"`  // 当前可轮转的授权条数; 0 表示这条任务此刻没有可测的凭据。
}

// ScheduledProbeRequest 是创建与更新共用的提交形状。
// 定时测活字段少且一次整体提交, 无需另建更新类型; 主键走路径, 不进请求体。
type ScheduledProbeRequest struct {
	ChannelID       int    `json:"channel_id" binding:"required"`                      // 被监控的渠道。
	ModelName       string `json:"model_name" binding:"required"`                      // 被监控的模型, 必须属于该渠道。
	IntervalMinutes int    `json:"interval_minutes" binding:"required,min=1,max=1440"` // 探测间隔分钟数。
	Enabled         bool   `json:"enabled"`                                            // 是否启用; 创建时忽略该字段, 新建任务一律先启用。
	Weekdays        int    `json:"weekdays" binding:"min=0,max=127"`                   // 时间窗星期掩码, 0 表示不限制。
	StartHour       int    `json:"start_hour" binding:"min=0,max=23"`                  // 时间窗起始整点。
	EndHour         int    `json:"end_hour" binding:"min=0,max=23"`                    // 时间窗结束整点。
}
