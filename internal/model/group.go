package model

// 分组选择上游成员的模式。
type GroupMode string

const (
	GroupModeManual   GroupMode = "manual"   // 只使用人工选中的成员。
	GroupModeFailover GroupMode = "failover" // 按成员排序选择并在失败时切换。
)

// 分组 Relay 的持久化配置，数据库中以 JSON 存储。
type GroupRelayConfig struct {
	MemberMaxAttempts                     int `json:"member_max_attempts" binding:"omitempty,min=1"`                        // 单个成员包含首次请求的总尝试次数，仅在故障转移模式生效。
	MemberRetryIntervalSeconds            int `json:"member_retry_interval_seconds" binding:"omitempty,min=1"`              // 同一成员相邻两次尝试之间的等待秒数。
	MemberNonStreamResponseTimeoutSeconds int `json:"member_non_stream_response_timeout_seconds" binding:"omitempty,min=1"` // 单个成员返回完整非流式响应的超时秒数。
	MemberStreamFirstEventTimeoutSeconds  int `json:"member_stream_first_event_timeout_seconds" binding:"omitempty,min=1"`  // 单个成员返回首个有效流事件的超时秒数。
	MemberCooldownSeconds                 int `json:"member_cooldown_seconds" binding:"omitempty,min=1"`                    // 单个成员耗尽尝试后被跳过的秒数，仅在故障转移模式生效。
	MemberAffinitySeconds                 int `json:"member_affinity_seconds" binding:"omitempty,min=0"`                    // 成员亲和时间:故障切换成功后继续保持当前成员的秒数;当前成员失败会立即结束亲和,0 表示不保持。
}

// DefaultGroupRelayConfig 返回新分组使用的 Relay 默认配置。
func DefaultGroupRelayConfig() GroupRelayConfig {
	return GroupRelayConfig{
		MemberMaxAttempts:                     2,
		MemberRetryIntervalSeconds:            3,
		MemberNonStreamResponseTimeoutSeconds: 120,
		MemberStreamFirstEventTimeoutSeconds:  30,
		MemberCooldownSeconds:                 60,
		MemberAffinitySeconds:                 300,
	}
}

// NormalizeGroupRelayConfig 补齐分组 Relay 配置中的空值。
func NormalizeGroupRelayConfig(config *GroupRelayConfig) {
	defaults := DefaultGroupRelayConfig()
	if *config == (GroupRelayConfig{}) {
		*config = defaults
		return
	}
	if config.MemberMaxAttempts < 1 {
		config.MemberMaxAttempts = defaults.MemberMaxAttempts
	}
	if config.MemberRetryIntervalSeconds < 1 {
		config.MemberRetryIntervalSeconds = defaults.MemberRetryIntervalSeconds
	}
	if config.MemberNonStreamResponseTimeoutSeconds < 1 {
		config.MemberNonStreamResponseTimeoutSeconds = defaults.MemberNonStreamResponseTimeoutSeconds
	}
	if config.MemberStreamFirstEventTimeoutSeconds < 1 {
		config.MemberStreamFirstEventTimeoutSeconds = defaults.MemberStreamFirstEventTimeoutSeconds
	}
	if config.MemberCooldownSeconds < 1 {
		config.MemberCooldownSeconds = defaults.MemberCooldownSeconds
	}
	if config.MemberAffinitySeconds < 0 {
		config.MemberAffinitySeconds = defaults.MemberAffinitySeconds
	}
}

// 客户端模型名称及其可手动选择或故障转移的上游分组。
type Group struct {
	ID           int              `json:"id" gorm:"primaryKey"`                                                          // 分组主键。
	Name         string           `json:"name" gorm:"unique;not null"`                                                   // 客户端请求使用的模型名称。
	Mode         GroupMode        `json:"mode" gorm:"not null;default:manual" binding:"omitempty,oneof=manual failover"` // 选择成员的模式。
	ActiveItemID int              `json:"active_item_id" gorm:"not null;default:0"`                                      // 手动模式指定的成员, 故障转移模式忽略该值, 0 表示未指定; 写入侧字段, 读取一律用响应中的 runtime.current_item_id, 出 JSON 仅为让备份转储带上它。
	PinnedItemID int              `json:"pinned_item_id" gorm:"not null;default:0"`                                      // 故障转移模式下强制优先使用的成员, 0 表示不强制; 该成员失败时仍按故障转移逻辑切到下一个, 恢复后切回。
	RelayConfig  GroupRelayConfig `json:"relay_config" gorm:"serializer:json"`                                           // 该分组的 Relay 路由配置。
	Items        []GroupItem      `json:"items" gorm:"foreignKey:GroupID;constraint:OnDelete:CASCADE"`                   // 该分组可手动选择或故障转移的分组项; 读取时恒为数组, 空集合也给出以免各消费方各自兜底。
}

// 分组内一个可选择的渠道授权分组项。
// 读取时补齐授权两侧的名称, 所属渠道与可用性: 界面只需展示与排序, 由此无需再按主键回查渠道, 模型与凭据。
// 补齐的字段不含上游凭据本身, 转发所需的完整授权由 Relay 另行按主键取。
type GroupItem struct {
	ID             int           `json:"id" gorm:"primaryKey"`                                                         // 分组项主键。
	GroupID        int           `json:"group_id" gorm:"not null;index:idx_group_grant,unique"`                        // 所属分组 ID。
	ChannelGrantID int           `json:"channel_grant_id" gorm:"not null;index:idx_group_grant,unique"`                // 引用的渠道授权 ID。
	ChannelGrant   *ChannelGrant `json:"-" gorm:"foreignKey:ChannelGrantID;references:ID;constraint:OnDelete:CASCADE"` // 仅用于声明级联外键, 授权被删除时成员随之删除; 读取时不填充, 展示所需字段见下方。
	Priority       int           `json:"priority" gorm:"not null"`                                                     // Priority 决定界面展示和故障转移模式下的成员切换顺序。

	ChannelID   int      `json:"channel_id" gorm:"-"`   // 授权所属渠道 ID。
	ChannelName string   `json:"channel_name" gorm:"-"` // 授权所属渠道名称。
	ModelName   string   `json:"model_name" gorm:"-"`   // 授权引用的上游模型名称。
	KeyName     string   `json:"key_name" gorm:"-"`     // 授权引用的凭据名称。
	Protocols   Protocol `json:"protocols" gorm:"-"`    // 授权支持的协议位掩码。
	Available   bool     `json:"available" gorm:"-"`    // 渠道与凭据均启用且模型, 凭据均存在时为真; 为假表示该成员当前无法转发, 但仍需列出以便移除。
}

// 创建分组请求; 成员顺序即优先级顺序。
// 不收主键与当前成员: 分组主键由数据库分配, 当前成员在创建后另行指定。
type GroupCreateRequest struct {
	Name        string           `json:"name" binding:"required"`                        // 客户端请求使用的模型名称。
	Mode        GroupMode        `json:"mode" binding:"omitempty,oneof=manual failover"` // 选择成员的模式, 留空按手动。
	RelayConfig GroupRelayConfig `json:"relay_config"`                                   // Relay 路由配置, 零值由后端补默认。
	Items       []GroupItemInput `json:"items"`                                          // 初始成员集合。
}

// 分组普通配置, 成员和当前成员的变更请求; 分组主键走路径, 不进请求体。
// 当前成员是分组的一个普通可选字段, 与其余字段共用本请求: 它不需要独立的权限, 审计或并发粒度。
type GroupUpdateRequest struct {
	Name         *string           `json:"name,omitempty"`                                           // Name 仅在名称变更时发送。
	Mode         *GroupMode        `json:"mode,omitempty" binding:"omitempty,oneof=manual failover"` // Mode 仅在选择模式变更时发送。
	RelayConfig  *GroupRelayConfig `json:"relay_config,omitempty"`                                   // RelayConfig 仅在 Relay 配置变更时发送完整配置。
	Items        *[]GroupItemInput `json:"items,omitempty"`                                          // 新的成员集合, 整体替换; 提交顺序即优先级顺序。
	ActiveItemID *int              `json:"active_item_id,omitempty"`                                 // 手动模式指定的当前成员, 0 表示取消选择; 用指针以便与"未提交该字段"区分。
	PinnedItemID *int              `json:"pinned_item_id,omitempty"`                                 // 故障转移模式下强制优先使用的成员, 0 表示取消强制; 同样用指针区分未提交。
}

// 提交分组成员时按渠道授权主键引用。
// 授权在提交前已由渠道页面创建, 主键必然存在, 故成员无需按名称引用;
// 成员自身的主键不参与提交: 整体替换按授权主键匹配, 已有成员的主键与统计由后端保留。
type GroupItemInput struct {
	ChannelGrantID int `json:"channel_grant_id" binding:"required"` // 待引用的渠道授权 ID。
}
