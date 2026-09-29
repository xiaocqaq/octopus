package handlers

import (
	"net/http"
	"strconv"

	"github.com/bestruirui/octopus/internal/model"
	"github.com/bestruirui/octopus/internal/op"
	"github.com/bestruirui/octopus/internal/relay"
	"github.com/bestruirui/octopus/internal/server/middleware"
	"github.com/bestruirui/octopus/internal/server/resp"
	"github.com/bestruirui/octopus/internal/server/router"
	"github.com/gin-gonic/gin"
)

func init() {
	router.NewGroupRouter("/api/v1/scheduled-probe").
		Use(middleware.Auth()).
		Use(middleware.RequireJSON()).
		AddRoute(
			router.NewRoute("/list", http.MethodGet).
				Handle(listScheduledProbe),
		).
		AddRoute(
			router.NewRoute("/create", http.MethodPost).
				Handle(createScheduledProbe),
		).
		AddRoute(
			router.NewRoute("/update/:id", http.MethodPost).
				Handle(updateScheduledProbe),
		).
		AddRoute(
			router.NewRoute("/delete/:id", http.MethodDelete).
				Handle(deleteScheduledProbe),
		)

	// 定时测活跑在后台, 没有请求上下文可以捎带事件; 结论落点后由这里补推一次分组事件,
	// 界面上的体检徽标才会在探测发生时自己变绿或变红, 而不必等用户刷新。
	relay.SetProbeLandedHook(publishProbeEvents)
}

// publishProbeEvents 把若干分组的最新路由状态推给事件流。
// 复用单条测活的推送逻辑: 结论落在同一处状态里, 广播形状也就该一致。
func publishProbeEvents(groupIDs []int) {
	for _, groupID := range groupIDs {
		publishProbeEvent(groupID)
	}
}

func listScheduledProbe(c *gin.Context) {
	resp.Success(c, op.ScheduledProbeList())
}

func createScheduledProbe(c *gin.Context) {
	var req model.ScheduledProbeRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		resp.Error(c, http.StatusBadRequest, err.Error())
		return
	}
	probe, err := op.ScheduledProbeCreate(req, c.Request.Context())
	if err != nil {
		resp.Error(c, http.StatusBadRequest, err.Error())
		return
	}
	// 让新任务在下一拍就被选中: 不重置的话它会带着"尚未调度"的状态等到下一拍, 用户看不到即时反应。
	relay.ResetScheduledProbe(probe.ID)
	resp.Success(c, probe)
}

func updateScheduledProbe(c *gin.Context) {
	id, err := strconv.Atoi(c.Param("id"))
	if err != nil {
		resp.Error(c, http.StatusBadRequest, resp.ErrInvalidParam)
		return
	}
	var req model.ScheduledProbeRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		resp.Error(c, http.StatusBadRequest, err.Error())
		return
	}
	probe, err := op.ScheduledProbeUpdate(id, req, c.Request.Context())
	if err != nil {
		resp.Error(c, http.StatusBadRequest, err.Error())
		return
	}
	// 配置变更后丢弃旧的调度进度: 间隔改小或从停用改回启用时, 任务若继续按旧配置等下去,
	// 用户看到的就是"改了配置但半天没反应"。
	relay.ResetScheduledProbe(id)
	resp.Success(c, probe)
}

func deleteScheduledProbe(c *gin.Context) {
	id, err := strconv.Atoi(c.Param("id"))
	if err != nil {
		resp.Error(c, http.StatusBadRequest, resp.ErrInvalidParam)
		return
	}
	if err := op.ScheduledProbeDelete(id, c.Request.Context()); err != nil {
		resp.Error(c, http.StatusInternalServerError, err.Error())
		return
	}
	relay.ResetScheduledProbe(id)
	resp.Success(c, nil)
}
