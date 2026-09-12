package model

type StatsMetrics struct {
	InputToken     int64   `json:"input_token" gorm:"bigint"`
	OutputToken    int64   `json:"output_token" gorm:"bigint"`
	InputCost      float64 `json:"input_cost" gorm:"type:real"`
	OutputCost     float64 `json:"output_cost" gorm:"type:real"`
	WaitTime       int64   `json:"wait_time" gorm:"bigint"`
	RequestSuccess int64   `json:"request_success" gorm:"bigint"`
	RequestFailed  int64   `json:"request_failed" gorm:"bigint"`
}

type StatsTotal struct {
	ID int `gorm:"primaryKey"`
	StatsMetrics
}

type StatsHourly struct {
	Hour int    `json:"hour" gorm:"primaryKey"`
	Date string `json:"date" gorm:"not null"` // 记录最后更新日期，格式：20060102
	StatsMetrics
}

type StatsDaily struct {
	Date string `json:"date" gorm:"primaryKey"`
	StatsMetrics
}

type StatsAPIKey struct {
	APIKeyID int `json:"api_key_id" gorm:"primaryKey"`
	StatsMetrics
}

// StatsChannelDaily 是单个渠道在某一天的统计。
// 渠道自身只存累计统计, 无从按时间切片, 故首页按周期切换榜单时需要这一份按日明细。
// 只留最近若干天: 榜单最长看 30 天, 全时段直接读渠道上的累计列, 无限保留只会让表随运行时长增长。
type StatsChannelDaily struct {
	ChannelID    int    `json:"channel_id" gorm:"primaryKey"` // 渠道主键。
	Date         string `json:"date" gorm:"primaryKey;index"` // 统计日期, 格式 20060102; 按日期清理与范围查询都走该索引。
	StatsMetrics        // 该渠道当日的统计。
}

// StatsChannelModelDaily 是单个渠道模型在某一天的统计, 与 StatsChannelDaily 同理。
type StatsChannelModelDaily struct {
	ChannelModelID int    `json:"channel_model_id" gorm:"primaryKey"` // 渠道模型主键。
	Date           string `json:"date" gorm:"primaryKey;index"`       // 统计日期, 格式 20060102。
	StatsMetrics          // 该渠道模型当日的统计。
}

// Add aggregates another StatsMetrics into the current one.
func (s *StatsMetrics) Add(delta StatsMetrics) {
	s.InputToken += delta.InputToken
	s.OutputToken += delta.OutputToken
	s.InputCost += delta.InputCost
	s.OutputCost += delta.OutputCost
	s.WaitTime += delta.WaitTime
	s.RequestSuccess += delta.RequestSuccess
	s.RequestFailed += delta.RequestFailed
}
