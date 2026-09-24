package op

import (
	"context"
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
	results := make([]model.GroupAssignChannelModelResult, 0, len(req.Assignments))
	for _, assignment := range req.Assignments {
		name := strings.TrimSpace(assignment.ModelName)
		result := model.GroupAssignChannelModelResult{ModelName: name, GroupID: assignment.GroupID}
		if name == "" || assignment.GroupID == 0 {
			result.Skipped = true
			result.Reason = "no target group"
			results = append(results, result)
			continue
		}
		if _, err := GroupGet(assignment.GroupID); err != nil {
			result.Skipped = true
			result.Reason = "group not found"
			results = append(results, result)
			continue
		}
		added, err := appendChannelModelToGroup(ctx, req.ChannelID, name, assignment.GroupID)
		if err != nil {
			return nil, err
		}
		result.GrantCount = added
		if added == 0 {
			result.Skipped = true
			result.Reason = "no available grants"
		}
		results = append(results, result)
	}
	return results, nil
}

func appendChannelModelToGroup(ctx context.Context, channelID int, modelName string, groupID int) (int, error) {
	var added int
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
			if !candidate.Available || !strings.EqualFold(strings.TrimSpace(candidate.ModelName), modelName) {
				continue
			}
			if _, ok := existing[candidate.ID]; ok {
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
		return 0, err
	}
	if err := groupRefreshCache(ctx); err != nil {
		return 0, err
	}
	return added, nil
}
