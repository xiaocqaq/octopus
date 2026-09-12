package model

import "time"

// DBDump is a full-database JSON export format for Octopus.
// Import uses incremental semantics (insert new rows, and upsert on tables with natural keys).
type DBDump struct {
	Version    int       `json:"version"`
	ExportedAt time.Time `json:"exported_at"`

	Channels      []Channel      `json:"channels,omitempty"`       // 渠道数据。
	ChannelKeys   []ChannelKey   `json:"channel_keys,omitempty"`   // 渠道凭据数据。
	ChannelModels []ChannelModel `json:"channel_models,omitempty"` // 渠道模型数据。
	ChannelGrants []ChannelGrant `json:"channel_grants,omitempty"` // 渠道授权数据, 含统计。
	Groups        []Group        `json:"groups,omitempty"`         // 分组数据。
	GroupItems    []GroupItem    `json:"group_items,omitempty"`    // 分组成员数据。
	LLMInfos      []LLMInfo      `json:"llm_infos,omitempty"`      // 模型价格数据。
	APIKeys       []APIKey       `json:"api_keys,omitempty"`       // API Key 数据。
	Settings      []Setting      `json:"settings,omitempty"`       // 系统设置数据。

	StatsTotal  []StatsTotal  `json:"stats_total,omitempty"`
	StatsDaily  []StatsDaily  `json:"stats_daily,omitempty"`
	StatsHourly []StatsHourly `json:"stats_hourly,omitempty"`
	StatsAPIKey []StatsAPIKey `json:"stats_api_key,omitempty"`

	StatsChannelDaily      []StatsChannelDaily      `json:"stats_channel_daily,omitempty"`       // 渠道按日明细, 供首页榜单按周期统计。
	StatsChannelModelDaily []StatsChannelModelDaily `json:"stats_channel_model_daily,omitempty"` // 渠道模型按日明细。
}

type DBImportResult struct {
	// RowsAffected contains the rows affected for each table operation (insert/upsert depending on table).
	RowsAffected map[string]int64 `json:"rows_affected"`
}
