package op

import (
	"context"
	"fmt"
	"sort"

	"github.com/bestruirui/octopus/internal/db"
	"github.com/bestruirui/octopus/internal/model"
	"github.com/bestruirui/octopus/internal/utils/cache"
)

var apiKeyCache = cache.New[int, model.APIKey](16)
var apiKeyIDMap = cache.New[string, int](16)

func APIKeyCreate(key *model.APIKey, ctx context.Context) error {
	if err := db.GetDB().WithContext(ctx).Create(key).Error; err != nil {
		return fmt.Errorf("failed to create API key: %w", err)
	}
	apiKeyCache.Set(key.ID, *key)
	apiKeyIDMap.Set(key.APIKey, key.ID)
	return nil
}

func APIKeyUpdate(key *model.APIKey, ctx context.Context) error {
	existing, ok := apiKeyCache.Get(key.ID)
	if !ok {
		return fmt.Errorf("API key not found")
	}
	if key.APIKey == "" {
		key.APIKey = existing.APIKey
	}
	if err := db.GetDB().WithContext(ctx).Save(key).Error; err != nil {
		return fmt.Errorf("failed to update API key: %w", err)
	}
	if key.APIKey != existing.APIKey {
		apiKeyIDMap.Del(existing.APIKey)
		apiKeyIDMap.Set(key.APIKey, key.ID)
	}
	apiKeyCache.Set(key.ID, *key)
	return nil
}

// APIKeyList 返回全部 API Key, 按主键升序定序。
// 设置页不提供排序开关, 而缓存遍历顺序随机, 故顺序须由此处定稿。
func APIKeyList(ctx context.Context) ([]model.APIKey, error) {
	keys := make([]model.APIKey, 0, apiKeyCache.Len())
	for _, apiKey := range apiKeyCache.GetAll() {
		keys = append(keys, apiKey)
	}
	sort.Slice(keys, func(i, j int) bool { return keys[i].ID < keys[j].ID })
	return keys, nil
}

func APIKeyGet(id int, ctx context.Context) (model.APIKey, error) {
	apiKey, ok := apiKeyCache.Get(id)
	if !ok {
		return model.APIKey{}, fmt.Errorf("API key not found")
	}
	return apiKey, nil
}

func APIKeyGetByAPIKey(apiKey string, ctx context.Context) (model.APIKey, error) {
	id, ok := apiKeyIDMap.Get(apiKey)
	if !ok {
		return model.APIKey{}, fmt.Errorf("API key not found")
	}
	return APIKeyGet(id, ctx)
}

// APIKeyDelete 删除指定 API Key。先从缓存取回密钥字符串: 删除要连"字符串→ID"的索引一起清掉,
// 漏了它, 已撤销的旧密钥在导入按原 ID 插回新 Key 后会以新 Key 的身份继续通过鉴权。
// 统计在库内删除成功后才动: 数据库出错时整个操作视为未发生, 不会出现 Key 还在、统计先没的半删状态。
func APIKeyDelete(id int, ctx context.Context) error {
	existing, ok := apiKeyCache.Get(id)
	if !ok {
		return fmt.Errorf("API key not found")
	}
	k := model.APIKey{
		ID: id,
	}
	result := db.GetDB().WithContext(ctx).Delete(&k)
	if result.Error != nil {
		return fmt.Errorf("failed to delete API key: %w", result.Error)
	}
	// GORM 出错时 RowsAffected 也为 0, 故错误必须先于行数判断, 否则瞬时故障会被误报成不存在。
	if result.RowsAffected == 0 {
		return fmt.Errorf("API key not found")
	}
	if err := StatsAPIKeyDel(id); err != nil {
		return fmt.Errorf("failed to delete stats API key: %v", err)
	}
	apiKeyCache.Del(id)
	apiKeyIDMap.Del(existing.APIKey)
	return nil
}

func apiKeyRefreshCache(ctx context.Context) error {
	apiKeys := []model.APIKey{}
	if err := db.GetDB().WithContext(ctx).Find(&apiKeys).Error; err != nil {
		return err
	}
	// 先清再灌: 导入后的刷新若不删旧映射, 库里已不存在的密钥字符串会带着旧 ID 留在索引里。
	apiKeyCache.Clear()
	apiKeyIDMap.Clear()
	for _, apiKey := range apiKeys {
		apiKeyCache.Set(apiKey.ID, apiKey)
		apiKeyIDMap.Set(apiKey.APIKey, apiKey.ID)
	}
	return nil
}
