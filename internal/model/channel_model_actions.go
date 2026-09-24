package model

// GroupAssignChannelModelsRequest 将当前渠道选中的模型追加到已有分组。
type GroupAssignChannelModelsRequest struct {
	ChannelID   int                           `json:"channel_id" binding:"required"`
	Assignments []ChannelModelGroupAssignment `json:"assignments" binding:"required,min=1"`
}

// ChannelModelGroupAssignment 是一个模型及其目标分组; group_id 为 0 表示跳过。
type ChannelModelGroupAssignment struct {
	ModelName string `json:"model_name" binding:"required"`
	GroupID   int    `json:"group_id"`
}

// GroupAssignChannelModelResult 是单个模型的分配结果。
type GroupAssignChannelModelResult struct {
	ModelName  string `json:"model_name"`
	GroupID    int    `json:"group_id"`
	GrantCount int    `json:"grant_count"`
	Skipped    bool   `json:"skipped"`
	Reason     string `json:"reason,omitempty"`
}

// ChannelModelProbeRequest 是模型页测活请求。KeyName 为空表示该模型的全部启用凭据。
type ChannelModelProbeRequest struct {
	ChannelID  int      `json:"channel_id" binding:"required"`
	ModelNames []string `json:"model_names" binding:"required,min=1"`
	KeyName    string   `json:"key_name"`
	Streaming  bool     `json:"streaming"`
}

// ChannelModelProbeResult 是模型页一次测活的结果。
type ChannelModelProbeResult struct {
	ModelName string `json:"model_name"`
	KeyName   string `json:"key_name"`
	OK        bool   `json:"ok"`
	LatencyMS int64  `json:"latency_ms"`
	Message   string `json:"message,omitempty"`
}
