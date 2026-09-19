package handlers

import (
	"net/http"
	"strconv"
	"sync"
	"time"

	"github.com/bestruirui/octopus/internal/model"
	"github.com/bestruirui/octopus/internal/op"
	"github.com/bestruirui/octopus/internal/server/middleware"
	"github.com/bestruirui/octopus/internal/server/resp"
	"github.com/bestruirui/octopus/internal/server/router"
	"github.com/gin-gonic/gin"
)

var activityMaxRequestCount int64     // 最近 54 周每日请求量的最大值。
var activityMaxCalculatedAt time.Time // 最大值上次计算时间。
var activityMaxMu sync.Mutex          // 保护最大值及计算时间的并发更新。

type statsDailyResponse struct {
	MaxRequestCount int64              `json:"max_request_count"` // 最近 54 周每日请求量的最大值。
	Items           []model.StatsDaily `json:"items"`             // 每日原始统计数据。
}

func init() {
	router.NewGroupRouter("/api/v1/stats").
		Use(middleware.Auth()).
		AddRoute(
			router.NewRoute("/daily", http.MethodGet).
				Handle(getStatsDaily),
		).
		AddRoute(
			router.NewRoute("/hourly", http.MethodGet).
				Handle(getStatsHourly),
		).
		AddRoute(
			router.NewRoute("/total", http.MethodGet).
				Handle(getStatsTotal),
		).
		AddRoute(
			router.NewRoute("/apikey", http.MethodGet).
				Handle(getStatsAPIKey),
		).
		AddRoute(
			router.NewRoute("/group", http.MethodGet).
				Handle(getStatsGroup),
		)
}

func getStatsDaily(c *gin.Context) {
	now := time.Now()
	since := now.AddDate(0, 0, -(int(now.Weekday()) + 53*7)).Format("20060102")
	statsDaily, err := op.StatsGetDaily(c.Request.Context(), since)
	if err != nil {
		resp.Error(c, http.StatusInternalServerError, err.Error())
		return
	}

	activityMaxMu.Lock()
	if activityMaxCalculatedAt.IsZero() || now.Sub(activityMaxCalculatedAt) >= 24*time.Hour {
		maxRequestCount := int64(0)
		for _, daily := range statsDaily {
			requestCount := daily.RequestSuccess + daily.RequestFailed
			if requestCount > maxRequestCount {
				maxRequestCount = requestCount
			}
		}
		activityMaxRequestCount = maxRequestCount
		activityMaxCalculatedAt = now
	}
	maxRequestCount := activityMaxRequestCount
	activityMaxMu.Unlock()

	resp.Success(c, statsDailyResponse{
		MaxRequestCount: maxRequestCount,
		Items:           statsDaily,
	})
}

func getStatsHourly(c *gin.Context) {
	resp.Success(c, op.StatsHourlyGet())
}

func getStatsTotal(c *gin.Context) {
	resp.Success(c, op.StatsTotalGet())
}

func getStatsAPIKey(c *gin.Context) {
	resp.Success(c, op.StatsAPIKeyList())
}

// getStatsGroup 返回当前全部分组在查询窗口内的统计。
// days 与渠道榜相同: 1/7/30 为日历日窗口, 缺省或非正数给出累计。
func getStatsGroup(c *gin.Context) {
	days, err := strconv.Atoi(c.DefaultQuery("days", "0"))
	if err != nil {
		resp.Error(c, http.StatusBadRequest, "invalid days")
		return
	}
	rows, err := op.StatsGroupList(c.Request.Context(), days)
	if err != nil {
		resp.Error(c, http.StatusInternalServerError, err.Error())
		return
	}
	resp.Success(c, rows)
}
