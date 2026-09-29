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

// ScheduledProbe 是一条定时测活任务: 一个自定义名字下面挂若干个被监控的目标。
//
// 任务与目标分两张表: 用户要的是"把几个模型放在一个标题下统一看",
// 而"测哪一条"的最小单位是 (渠道, 模型) —— 一个任务挂多个目标是常态,
// 把目标塞进任务的列里就得为一个任务存 N 行重复的间隔与时段配置。
//
// 名字由用户自定, 通常就是模型名; 不拿目标反推名字: 一个任务挂了三个模型时,
// 界面上需要一个能概括它们的称呼, 而这个称呼只有用户知道该怎么起。
type ScheduledProbe struct {
	ID   int    `json:"id" gorm:"primaryKey"`                  // 任务主键。
	Name string `json:"name" gorm:"not null;default:''"`       // 任务的自定义名字, 界面上通常就是模型名。
	IntervalMinutes int `json:"interval_minutes" gorm:"not null;default:10"` // 两次探测之间的间隔分钟数。
	Enabled         bool `json:"enabled" gorm:"not null;default:true"`       // 是否参与调度; 停用后不再探测, 配置保留。
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

	// Targets 是该任务要监控的目标集合。读取顺序即轮转顺序, 见 op.ScheduledProbeTargets 的定序说明。
	// 级联删除: 任务没了, 它挂的目标不该留在库里成为孤儿行。
	Targets []ScheduledProbeTarget `json:"targets" gorm:"foreignKey:ProbeID;constraint:OnDelete:CASCADE"`

	// CreditOrder 是用户拖出来的凭据顺序, 每一项形如 "渠道ID\x00模型名\x00凭据名"。
	//
	// 顺序按凭据记而不是按目标记: 轮转是扁平的(每一拍测下一条凭据), 界面上也是一行一条,
	// 两者必须读同一份顺序 —— 否则"把这条拖到最前"在界面上成立、在调度里却不成立。
	//
	// 没列进来的凭据排在列过的之后, 并保持默认顺序(目标主键、凭据名):
	// 用户后来新增的目标或凭据会自然接在末尾, 不必回头再拖一次; 记录里已经消失的项留着也无害 ——
	// 读的时候只认此刻仍然存在的那些, 不必在删除凭据时回头维护这张表。
	CreditOrder []string `json:"credit_order" gorm:"serializer:json"`
}

// ScheduledProbeTarget 是任务下的一个被监控目标: 某个渠道下的某个模型。
//
// 只记 (渠道, 模型), 不记凭据。同一模型在同一渠道下可能挂多条凭据(授权), 那是"怎么转发"的细节,
// 而用户要盯的是"这个渠道的这个模型此刻还活着吗"。多条凭据由调度器按轮转逐次探测, 一拍只测一条授权:
// 测活打的是真实上游, 一拍把全部凭据一起打会在上游留下一次突发流量, 结论也不再是同一时刻的快照。
//
// 模型存名称而不是渠道模型主键: 渠道页整体替换模型集合时会重建主键, 按名称存的目标不会因此失效。
type ScheduledProbeTarget struct {
	ID        int    `json:"id" gorm:"primaryKey"`                     // 目标主键。
	ProbeID   int    `json:"probe_id" gorm:"not null;index"`           // 所属任务 ID。
	ChannelID int    `json:"channel_id" gorm:"not null"`               // 被监控的渠道 ID。
	ModelName string `json:"model_name" gorm:"not null"`               // 被监控的上游模型名称, 必须属于该渠道。
	// ExcludedKeys 记录用户在这个目标下逐行删掉的凭据名称。
	//
	// 记"排除谁"而不是"只留谁": 空值天然等于"全都要", 新建的任务与从没删过行的任务因此落在同一个默认上;
	// 而且以后往渠道里加了新凭据, 它会自动进入监控范围, 不必回头再改一遍每条任务。
	//
	// 按名称记而不是按授权主键: 渠道一经重新保存, 授权行会被整体重建、主键全变, 按主键记的排除项会集体失效,
	// 用户删掉的那些行又会自己长回来。名称是用户点那一行时唯一看得见的东西, 也是渠道重存后仍然成立的标识。
	//
	// 用 json 序列化而不是另起一张表: 这个集合只随目标整体读写, 从不单独查询, 单独建表只会多一次连接。
	ExcludedKeys []string `json:"excluded_keys" gorm:"serializer:json"`
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

// ScheduledProbeRow 是界面上的一行, 对应一条渠道凭据。
//
// 按凭据而不是按目标出行: 一个目标可能挂着多条凭据, 只出一行就没法逐条看结论、也没法逐条手动测;
// 而"哪条凭据不通"恰恰是排查时唯一有用的粒度。
//
// 结论字段就地平铺而不嵌套一个可空结构: 前端要判断的是"这一行有没有结论",
// 一个 probed 布尔比一个空对象少一层判空, 少一层判空就少一处可能写错的地方。
type ScheduledProbeRow struct {
	GrantID     int    `json:"grant_id"`     // 渠道授权主键, 手动测试按它发起。
	ChannelID   int    `json:"channel_id"`   // 所属渠道 ID。
	ChannelName string `json:"channel_name"` // 渠道名称; 渠道已删除时为空。
	ModelName   string `json:"model_name"`   // 目标模型名称。
	KeyName     string `json:"key_name"`     // 该授权所用凭据的名称。
	Probed      bool   `json:"probed"`       // 是否有仍在有效期内的结论; 为假时下面的字段全部无意义。
	OK          bool   `json:"ok"`           // 该凭据本轮是否调通。
	LatencyMS   int64  `json:"latency_ms"`   // 从发起到收到有效响应的耗时毫秒数。
	Message     string `json:"message"`      // 成功时为空, 失败时为上游错误正文或本地配置错误。
	ProbedAt    int64  `json:"probed_at"`    // 结论产生时间, Unix 毫秒。
}

// ScheduledProbeView 是定时测活任务的列表项: 任务配置加两处现算的展示字段。
// 这些字段都随渠道配置与探测进展变动, 不落库: 存下来就得在每次渠道改动、每次探测后跟着刷新一遍。
type ScheduledProbeView struct {
	ScheduledProbe
	// Rows 是各条凭据的当前状态, 也是界面上一行一条的渲染依据。
	// 尚无结论的凭据同样出行(probed 为假): 不给它出行, 用户就点不到那一行的手动测试按钮,
	// 第一次测活也就无从发起 —— 那正是最需要这个按钮的时候。
	Rows []ScheduledProbeRow `json:"rows"`
}

// ScheduledProbeTargetRequest 提交一个被监控目标。
type ScheduledProbeTargetRequest struct {
	ChannelID int    `json:"channel_id" binding:"required"` // 被监控的渠道。
	ModelName string `json:"model_name" binding:"required"` // 被监控的模型, 必须属于该渠道。
	// ExcludedKeys 是该目标下不再监控的凭据名称。
	//
	// 用 nil 与空切片区分两种意图, 更新时据此决定保留还是清空:
	// 字段缺省(nil)表示"这次提交不涉及排除项", 服务端沿用既有值 —— 编辑表单只知道 (渠道, 模型),
	// 不带这个字段, 若按空值处理, 用户每编辑一次任务, 删过的凭据就会全部复活。
	// 显式传空数组([])才是"清空排除项", 界面上那个「全部恢复」走的就是这条路径。
	ExcludedKeys []string `json:"excluded_keys"`
}

// ScheduledProbeRequest 是创建与更新共用的提交形状。
// 定时测活字段少且一次整体提交, 无需另建更新类型; 主键走路径, 不进请求体。
type ScheduledProbeRequest struct {
	Name string `json:"name" binding:"required"` // 任务的自定义名字, 界面上通常就是模型名。
	// Targets 整体替换: 目标集合是一次编辑里定稿的, 逐个增删要额外定义"没传的目标算不算删除"。
	// dive 让校验下沉到每个元素: 少一个渠道 ID 也能定位到是哪一个目标写错了。
	Targets         []ScheduledProbeTargetRequest `json:"targets" binding:"required,min=1,dive"`
	IntervalMinutes int                           `json:"interval_minutes" binding:"required,min=1,max=1440"` // 探测间隔分钟数。
	Enabled         bool                          `json:"enabled"`                                            // 是否启用; 创建时忽略该字段, 新建任务一律先启用。
	Weekdays        int                           `json:"weekdays" binding:"min=0,max=127"`                   // 时间窗星期掩码, 0 表示不限制。
	StartHour       int                           `json:"start_hour" binding:"min=0,max=23"`                  // 时间窗起始整点。
	EndHour         int                           `json:"end_hour" binding:"min=0,max=23"`                    // 时间窗结束整点。
}
