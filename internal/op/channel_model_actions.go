package op

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"

	"github.com/bestruirui/octopus/internal/db"
	"github.com/bestruirui/octopus/internal/model"
	"gorm.io/gorm"
)

// GroupAssignChannelModels 将当前渠道选中的模型追加到目标分组, 不替换分组既有成员。
func GroupAssignChannelModels(req *model.GroupAssignChannelModelsRequest, ctx context.Context) ([]model.GroupAssignChannelModelResult, error) {
	if req == nil || req.ChannelID == 0 {
		return nil, fmt.Errorf("channel id is required")
	}
	if _, err := ChannelGet(req.ChannelID); err != nil {
		return nil, err
	}
	if err := ensureAssignmentGrants(ctx, req.ChannelID, req.Assignments); err != nil {
		return nil, err
	}
	keyName := strings.TrimSpace(req.KeyName)
	results := make([]model.GroupAssignChannelModelResult, 0, len(req.Assignments))
	for _, assignment := range req.Assignments {
		name := strings.TrimSpace(assignment.ModelName)
		result := model.GroupAssignChannelModelResult{ModelName: name, GroupID: assignment.GroupID}
		if name == "" || assignment.GroupID == 0 {
			result.Skipped = true
			result.Reason = "no_target_group"
			results = append(results, result)
			continue
		}
		group, err := GroupGet(assignment.GroupID)
		if err != nil {
			result.Skipped = true
			result.Reason = "group_not_found"
			results = append(results, result)
			continue
		}
		result.GroupName = group.Name
		added, reason, err := appendChannelModelToGroup(ctx, req.ChannelID, name, keyName, assignment.GroupID)
		if err != nil {
			return nil, err
		}
		result.GrantCount = added
		if added == 0 {
			result.Skipped = true
			result.Reason = reason
		}
		results = append(results, result)
	}
	return results, nil
}

// ensureAssignmentGrants 把本次分配带上的授权补进渠道, 只增改不删除。
// 模型页可以先分组再点保存; 只读已落库授权会把未保存模型全部跳过, 界面却仍能显示成功。
func ensureAssignmentGrants(ctx context.Context, channelID int, assignments []model.ChannelModelGroupAssignment) error {
	if _, err := ChannelGet(channelID); err != nil {
		return err
	}
	changed := false
	err := db.GetDB().WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var keys []model.ChannelKey
		if err := tx.Where("channel_id = ?", channelID).Find(&keys).Error; err != nil {
			return fmt.Errorf("failed to load channel keys: %w", err)
		}
		keyIDByName := make(map[string]int, len(keys))
		for _, key := range keys {
			keyIDByName[key.Name] = key.ID
		}
		var models []model.ChannelModel
		if err := tx.Where("channel_id = ?", channelID).Find(&models).Error; err != nil {
			return fmt.Errorf("failed to load channel models: %w", err)
		}
		modelIDByName := make(map[string]int, len(models))
		for _, channelModel := range models {
			modelIDByName[channelModel.Name] = channelModel.ID
		}
		for _, assignment := range assignments {
			modelName := strings.TrimSpace(assignment.ModelName)
			if modelName == "" {
				continue
			}
			for _, grant := range assignment.Grants {
				if strings.TrimSpace(grant.ModelName) != modelName || grant.Protocols == 0 || grant.Protocols&^definedProtocols != 0 {
					continue
				}
				keyID, ok := keyIDByName[strings.TrimSpace(grant.KeyName)]
				if !ok {
					continue
				}
				modelID, ok := modelIDByName[modelName]
				if !ok {
					channelModel := model.ChannelModel{ChannelID: channelID, Name: modelName}
					if err := tx.Create(&channelModel).Error; err != nil {
						return fmt.Errorf("failed to create channel model: %w", err)
					}
					modelID = channelModel.ID
					modelIDByName[modelName] = modelID
					changed = true
				}
				var existing model.ChannelGrant
				err := tx.Where("channel_model_id = ? AND channel_key_id = ?", modelID, keyID).First(&existing).Error
				if err == nil {
					if existing.Protocols != grant.Protocols {
						if err := tx.Model(&model.ChannelGrant{}).Where("id = ?", existing.ID).Update("protocols", grant.Protocols).Error; err != nil {
							return fmt.Errorf("failed to update channel grant: %w", err)
						}
						changed = true
					}
					continue
				}
				if !errors.Is(err, gorm.ErrRecordNotFound) {
					return fmt.Errorf("failed to load channel grant: %w", err)
				}
				if err := tx.Create(&model.ChannelGrant{ChannelModelID: modelID, ChannelKeyID: keyID, Protocols: grant.Protocols}).Error; err != nil {
					return fmt.Errorf("failed to create channel grant: %w", err)
				}
				changed = true
			}
		}
		return nil
	})
	if err != nil {
		return err
	}
	if !changed {
		return nil
	}
	if err := reloadChannelChildren(ctx, channelID); err != nil {
		return err
	}
	return nil
}

func appendChannelModelToGroup(ctx context.Context, channelID int, modelName, keyName string, groupID int) (int, string, error) {
	var added, matched, unavailable, already int
	err := db.GetDB().WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var group model.Group
		if err := tx.Preload("Items").First(&group, groupID).Error; err != nil {
			return err
		}
		existing := make(map[int]struct{}, len(group.Items))
		for _, item := range group.Items {
			existing[item.ChannelGrantID] = struct{}{}
		}
		candidates := channelGrantCandidatesOfChannel(channelID)
		sort.SliceStable(candidates, func(i, j int) bool {
			if candidates[i].ModelName != candidates[j].ModelName {
				return candidates[i].ModelName < candidates[j].ModelName
			}
			return candidates[i].KeyName < candidates[j].KeyName
		})
		for _, candidate := range candidates {
			if !strings.EqualFold(strings.TrimSpace(candidate.ModelName), modelName) {
				continue
			}
			if keyName != "" && candidate.KeyName != keyName {
				continue
			}
			matched++
			if !candidate.Available {
				unavailable++
				continue
			}
			if _, ok := existing[candidate.ID]; ok {
				already++
				continue
			}
			item := model.GroupItem{GroupID: groupID, ChannelGrantID: candidate.ID, Priority: len(group.Items) + 1}
			if err := tx.Create(&item).Error; err != nil {
				return err
			}
			group.Items = append(group.Items, item)
			existing[candidate.ID] = struct{}{}
			added++
		}
		return nil
	})
	if err != nil {
		return 0, "", err
	}
	if added > 0 {
		if err := groupRefreshCache(ctx); err != nil {
			return 0, "", err
		}
		return added, "", nil
	}
	switch {
	case matched == 0:
		return 0, "model_not_saved", nil
	case already > 0 && unavailable == 0:
		return 0, "already_in_group", nil
	default:
		return 0, "no_available_grants", nil
	}
}
