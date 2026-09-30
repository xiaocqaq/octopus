package handlers

import (
	"encoding/json"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/bestruirui/octopus/internal/model"
	"github.com/bestruirui/octopus/internal/op"
	"github.com/bestruirui/octopus/internal/server/middleware"
	"github.com/bestruirui/octopus/internal/server/resp"
	"github.com/bestruirui/octopus/internal/server/router"
	"github.com/bestruirui/octopus/internal/task"
	"github.com/gin-gonic/gin"
)

func init() {
	router.NewGroupRouter("/api/v1/setting").
		Use(middleware.Auth()).
		AddRoute(
			router.NewRoute("/list", http.MethodGet).
				Handle(getSettingList),
		).
		AddRoute(
			router.NewRoute("/set", http.MethodPost).
				Use(middleware.RequireJSON()).
				Handle(setSetting),
		).
		AddRoute(
			router.NewRoute("/export", http.MethodGet).
				Handle(exportDB),
		).
		AddRoute(
			router.NewRoute("/import", http.MethodPost).
				Handle(importDB),
		)
}

func getSettingList(c *gin.Context) {
	settings, err := op.SettingList(c.Request.Context())
	if err != nil {
		resp.Error(c, http.StatusInternalServerError, err.Error())
		return
	}
	resp.Success(c, settings)
}

func setSetting(c *gin.Context) {
	var setting model.Setting
	if err := c.ShouldBindJSON(&setting); err != nil {
		resp.Error(c, http.StatusBadRequest, err.Error())
		return
	}
	if err := setting.Validate(); err != nil {
		resp.Error(c, http.StatusBadRequest, err.Error())
		return
	}
	if err := op.SettingSetString(setting.Key, setting.Value); err != nil {
		resp.Error(c, http.StatusInternalServerError, err.Error())
		return
	}
	switch setting.Key {
	case model.SettingKeyModelInfoUpdateInterval:
		hours, err := strconv.Atoi(setting.Value)
		if err != nil {
			resp.Error(c, http.StatusBadRequest, err.Error())
			return
		}
		task.Update(string(setting.Key), time.Duration(hours)*time.Hour)
	}
	resp.Success(c, setting)
}

func exportDB(c *gin.Context) {
	dump, err := op.DBExportAll(c.Request.Context())
	if err != nil {
		resp.Error(c, http.StatusInternalServerError, err.Error())
		return
	}

	c.Header("Content-Type", "application/json")
	c.Header("Content-Disposition", "attachment; filename=\"octopus-export-"+time.Now().Format("20060102150405")+".json\"")
	c.JSON(http.StatusOK, dump)
}

func importDB(c *gin.Context) {
	var dump model.DBDump

	contentType := c.GetHeader("Content-Type")
	if strings.Contains(contentType, "multipart/form-data") {
		fh, err := c.FormFile("file")
		if err != nil {
			resp.Error(c, http.StatusBadRequest, "missing upload file field 'file'")
			return
		}
		f, err := fh.Open()
		if err != nil {
			resp.Error(c, http.StatusBadRequest, err.Error())
			return
		}
		defer f.Close()
		body, err := io.ReadAll(f)
		if err != nil {
			resp.Error(c, http.StatusBadRequest, err.Error())
			return
		}
		if err := decodeDBDump(body, &dump); err != nil {
			resp.Error(c, http.StatusBadRequest, err.Error())
			return
		}
	} else {
		body, err := io.ReadAll(c.Request.Body)
		if err != nil {
			resp.Error(c, http.StatusBadRequest, err.Error())
			return
		}
		if err := decodeDBDump(body, &dump); err != nil {
			resp.Error(c, http.StatusBadRequest, err.Error())
			return
		}
	}

	seenLLMNames := make(map[string]struct{}, len(dump.LLMInfos))
	for i := range dump.LLMInfos {
		dump.LLMInfos[i].Name = strings.ToLower(strings.TrimSpace(dump.LLMInfos[i].Name))
		if dump.LLMInfos[i].Name == "" {
			resp.Error(c, http.StatusBadRequest, "model price name cannot be empty")
			return
		}
		if _, ok := seenLLMNames[dump.LLMInfos[i].Name]; ok {
			resp.Error(c, http.StatusBadRequest, "duplicate model price: "+dump.LLMInfos[i].Name)
			return
		}
		seenLLMNames[dump.LLMInfos[i].Name] = struct{}{}
	}
	for i := range dump.Groups {
		if dump.Groups[i].Mode == "" {
			dump.Groups[i].Mode = model.GroupModeManual
		}
		model.NormalizeGroupRelayConfig(&dump.Groups[i].RelayConfig)
		if dump.Groups[i].Mode != model.GroupModeManual && dump.Groups[i].Mode != model.GroupModeFailover {
			resp.Error(c, http.StatusBadRequest, "invalid group relay mode")
			return
		}
	}
	// 定时测活任务只做名字与间隔两项校验: 间隔直接驱动调度器的下一次探测时刻,
	// 手改过的备份带个 0 或负数进来, 会让任务每一拍都被判定到点; 目标引用的渠道与模型
	// 在导入事务内才落库、此时缓存里还没有, 无法校验, 引用了不存在渠道的目标测活时自然无结论。
	for i := range dump.ScheduledProbes {
		dump.ScheduledProbes[i].Name = strings.TrimSpace(dump.ScheduledProbes[i].Name)
		if dump.ScheduledProbes[i].Name == "" {
			resp.Error(c, http.StatusBadRequest, "scheduled probe name cannot be empty")
			return
		}
		if dump.ScheduledProbes[i].IntervalMinutes < model.ScheduledProbeMinIntervalMinutes ||
			dump.ScheduledProbes[i].IntervalMinutes > model.ScheduledProbeMaxIntervalMinutes {
			resp.Error(c, http.StatusBadRequest, "invalid scheduled probe interval")
			return
		}
	}

	result, err := op.DBImportIncremental(c.Request.Context(), &dump)
	if err != nil {
		resp.Error(c, http.StatusBadRequest, err.Error())
		return
	}

	if err := op.InitCache(); err != nil {
		resp.Error(c, http.StatusInternalServerError, err.Error())
		return
	}

	resp.Success(c, result)
}

func decodeDBDump(body []byte, dump *model.DBDump) error {
	if dump == nil {
		return json.Unmarshal(body, &struct{}{})
	}

	if err := json.Unmarshal(body, dump); err != nil {
		return err
	}

	if dump.Version == 0 &&
		len(dump.Channels) == 0 &&
		len(dump.Groups) == 0 &&
		len(dump.ChannelModels) == 0 &&
		len(dump.GroupItems) == 0 &&
		len(dump.Settings) == 0 &&
		len(dump.APIKeys) == 0 &&
		len(dump.LLMInfos) == 0 &&
		len(dump.StatsDaily) == 0 &&
		len(dump.StatsHourly) == 0 &&
		len(dump.StatsTotal) == 0 &&
		len(dump.StatsAPIKey) == 0 &&
		len(dump.ScheduledProbes) == 0 &&
		len(dump.ScheduledProbeTargets) == 0 {
		var wrapper struct {
			Code    int             `json:"code"`
			Message string          `json:"message"`
			Data    json.RawMessage `json:"data"`
		}
		if err := json.Unmarshal(body, &wrapper); err == nil && len(wrapper.Data) > 0 {
			return json.Unmarshal(wrapper.Data, dump)
		}
	}

	return nil
}
