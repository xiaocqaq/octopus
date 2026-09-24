package handlers

import (
	"net/http"
	"strings"
	"sync"

	"github.com/bestruirui/octopus/internal/model"
	"github.com/bestruirui/octopus/internal/op"
	"github.com/bestruirui/octopus/internal/relay"
	"github.com/bestruirui/octopus/internal/server/resp"
	"github.com/gin-gonic/gin"
)

// assignChannelModels 把模型页勾选的当前渠道模型追加到各自目标分组。
func assignChannelModels(c *gin.Context) {
	var req model.GroupAssignChannelModelsRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		resp.Error(c, http.StatusBadRequest, err.Error())
		return
	}
	results, err := op.GroupAssignChannelModels(&req, c.Request.Context())
	if err != nil {
		resp.Error(c, http.StatusBadRequest, err.Error())
		return
	}
	seen := make(map[int]struct{})
	for _, result := range results {
		if result.Skipped || result.GroupID == 0 {
			continue
		}
		if _, ok := seen[result.GroupID]; ok {
			continue
		}
		seen[result.GroupID] = struct{}{}
		if group, err := op.GroupGet(result.GroupID); err == nil {
			publishGroupEvent(groupEvent{Name: "changed", Data: groupResponse{Group: group, Runtime: relay.RouteStateOf(group)}})
		}
	}
	resp.Success(c, results)
}

// probeChannelModels 只测当前渠道、当前模型页筛选范围内的启用授权。
// key_name 为空表示全部凭据; 非空时只测该凭据。并发上限与分组一键测活保持一致为 4。
func probeChannelModels(c *gin.Context) {
	var req model.ChannelModelProbeRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		resp.Error(c, http.StatusBadRequest, err.Error())
		return
	}
	want := make(map[string]struct{}, len(req.ModelNames))
	for _, name := range req.ModelNames {
		if name = strings.TrimSpace(name); name != "" {
			want[name] = struct{}{}
		}
	}
	if len(want) == 0 {
		resp.Error(c, http.StatusBadRequest, "model names are required")
		return
	}
	candidates := op.ChannelGrantCandidates()
	type target struct {
		candidate model.ChannelGrantCandidate
		index     int
	}
	targets := make([]target, 0)
	for _, candidate := range candidates {
		if candidate.ChannelID != req.ChannelID || !candidate.Available {
			continue
		}
		if _, ok := want[candidate.ModelName]; !ok {
			continue
		}
		if req.KeyName != "" && candidate.KeyName != req.KeyName {
			continue
		}
		targets = append(targets, target{candidate: candidate, index: len(targets)})
	}
	results := make([]model.ChannelModelProbeResult, len(targets))
	semaphore := make(chan struct{}, 4)
	var wait sync.WaitGroup
	for i, item := range targets {
		wait.Add(1)
		go func(i int, item target) {
			defer wait.Done()
			semaphore <- struct{}{}
			defer func() { <-semaphore }()
			probe := relay.ProbeChannelGrant(c.Request.Context(), item.candidate.ID, req.Streaming)
			results[i] = model.ChannelModelProbeResult{
				ModelName: item.candidate.ModelName,
				KeyName:   item.candidate.KeyName,
				OK:        probe.OK,
				LatencyMS: probe.LatencyMS,
				Message:   probe.Message,
			}
		}(i, item)
	}
	wait.Wait()
	resp.Success(c, results)
}
