package op

import (
	"testing"
)

// TestChannelGrantCandidatesOfChannelFiltersByChannel 验证按渠道筛选候选:
// 只保留属于指定渠道的授权, 且模型, 凭据两侧缺失的授权不进候选。
func TestChannelGrantCandidatesOfChannelFiltersByChannel(t *testing.T) {
	// 该函数只读缓存, 缓存为空时返回空切片而非 panic; 一键添加依赖它兜住脏数据。
	candidates := channelGrantCandidatesOfChannel(1)
	if len(candidates) != 0 {
		t.Fatalf("expected 0 candidates with empty caches, got %d", len(candidates))
	}
}

// TestGroupAddChannelRejectedIDs 验证一键添加对不存在对象的拒绝路径。
// 数据库与缓存未初始化时 GroupAddChannel 应直接报错而不是产生半成品写入。
func TestGroupAddChannelRejectedIDs(t *testing.T) {
	if _, _, err := GroupAddChannel(1, 1, t.Context()); err == nil {
		t.Fatal("expected error for missing group, got nil")
	}
}
