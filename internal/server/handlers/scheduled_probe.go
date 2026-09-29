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
		).
		AddRoute(
			router.NewRoute("/probe/:id", http.MethodPost).
				Handle(probeScheduledNow),
		).
		AddRoute(
			router.NewRoute("/probe-grant/:grantID", http.MethodPost).
				Handle(probeGrantNow),
		).
		AddRoute(
			router.NewRoute("/credential/:id", http.MethodPost).
				Handle(setScheduledProbeCredential),
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
	resp.Success(c, scheduledProbeViews())
}

// scheduledProbeViews 组装列表响应: 任务配置来自 op, 测活结论来自 relay, 两者在处理器这里合并。
//
// 合并放在这一层是被依赖方向逼出来的: relay 依赖 op(探测要读渠道与授权), op 再反过来读 relay 的结论就成环了。
// 处理器本来就同时依赖两者, 由它拼装是唯一不需要新增抽象的位置。
func scheduledProbeViews() []model.ScheduledProbeView {
	views := op.ScheduledProbeList()
	for i := range views {
		views[i].Rows = probeRows(views[i])
	}
	return views
}

// probeRows 把一条任务展开成界面上的行: 每个目标下的每条凭据各一行, 并带上它最近一次测活的结论。
//
// 逐条凭据出行而不是每个目标一行: 一个目标可能挂多条凭据, 只出一行就既看不到"哪条不通",
// 也点不到那一行的手动测试按钮 —— 而排查时唯一有用的粒度就是单条凭据。
//
// 没有结论的凭据同样出行(probed 为假): 少了它, 用户第一次就无从发起测活。
func probeRows(probe model.ScheduledProbeView) []model.ScheduledProbeRow {
	rows := make([]model.ScheduledProbeRow, 0, len(probe.Targets))
	for _, target := range probe.Targets {
		grantIDs := op.ScheduledProbeGrantIDs(target)
		results := relay.ScheduledProbeResults(grantIDs)
		channelName := op.ChannelNameOf(target.ChannelID)

		if len(grantIDs) == 0 {
			// 该目标当下没有可测凭据: 仍出一行占位, grant_id 为 0。
			// 界面据 grant_id 为 0 把那一行的手动测试按钮置灰并说明原因 ——
			// 整条目标凭空消失, 用户只会以为是自己没配上。
			rows = append(rows, model.ScheduledProbeRow{
				ChannelID:   target.ChannelID,
				ChannelName: channelName,
				ModelName:   target.ModelName,
			})
			continue
		}

		for _, grantID := range grantIDs {
			row := model.ScheduledProbeRow{
				GrantID:     grantID,
				ChannelID:   target.ChannelID,
				ChannelName: channelName,
				ModelName:   target.ModelName,
				KeyName:     op.GrantKeyName(grantID),
			}
			if result, ok := results[grantID]; ok {
				row.Probed = true
				row.OK = result.OK
				row.LatencyMS = result.LatencyMS
				row.Message = result.Message
				row.ProbedAt = result.ProbedAt
			}
			rows = append(rows, row)
		}
	}
	return rows
}

// probeScheduledNow 立即把一条任务的全部目标与凭据测一遍, 返回逐条结论。
// 结论同时落进路由状态与凭据结论表, 但这里仍需返回给调用方: 手动触发要的是即时反馈,
// 等下一次列表轮询才看到结果, 用户会以为按钮没生效。
func probeScheduledNow(c *gin.Context) {
	id, err := strconv.Atoi(c.Param("id"))
	if err != nil {
		resp.Error(c, http.StatusBadRequest, resp.ErrInvalidParam)
		return
	}
	probe, ok := op.ScheduledProbeGet(id)
	if !ok {
		resp.Error(c, http.StatusNotFound, "scheduled probe not found")
		return
	}

	results, err := relay.ProbeScheduledNow(c.Request.Context(), probe)
	if err != nil {
		// 没有可测凭据属于配置状态而非通道故障: 当作请求错误回, 前端直接提示去检查渠道。
		resp.Error(c, http.StatusBadRequest, err.Error())
		return
	}
	resp.Success(c, probeRowsOf(probe, results))
}

// probeGrantNow 立即测一条凭据, 供界面上一行末尾的闪电按钮使用。
// 按凭据而不是按整条任务: 那一行代表的就是这一条凭据, 点它却把整批都测一遍,
// 既多打了上游, 也让"我点的是这一行"这个意图落空。
func probeGrantNow(c *gin.Context) {
	grantID, err := strconv.Atoi(c.Param("grantID"))
	if err != nil {
		resp.Error(c, http.StatusBadRequest, resp.ErrInvalidParam)
		return
	}
	result := relay.ProbeGrantNow(c.Request.Context(), grantID)
	resp.Success(c, result)
}

// probeRowsOf 把刚测出的结论整理成与列表同形状的行, 让前端可以直接就地更新那一行。
// 复用行的形状而不是另造一个响应: 前端拿到之后要做的正是"用新结论替换旧行",
// 形状一致就不必写第二套更新逻辑。
func probeRowsOf(probe model.ScheduledProbe, results []relay.ProbeResult) []model.ScheduledProbeRow {
	rows := make([]model.ScheduledProbeRow, 0, len(results))
	for _, result := range results {
		row := model.ScheduledProbeRow{
			GrantID:   result.ItemID,
			KeyName:   op.GrantKeyName(result.ItemID),
			Probed:    true,
			OK:        result.OK,
			LatencyMS: result.LatencyMS,
			Message:   result.Message,
			ProbedAt:  result.ProbedAt,
		}
		for _, target := range probe.Targets {
			for _, grantID := range op.ScheduledProbeGrantIDs(target) {
				if grantID != result.ItemID {
					continue
				}
				row.ChannelID = target.ChannelID
				row.ChannelName = op.ChannelNameOf(target.ChannelID)
				row.ModelName = target.ModelName
			}
		}
		rows = append(rows, row)
	}
	return rows
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

// scheduledProbeCredentialRequest 是行内隐藏/恢复一条凭据的提交形状。
//
// Excluded 刻意不带 required: 它的假值(false)本身就是一个合法取值 —— "恢复"。
// binding 的 required 会把假值一律判成缺失, 于是"恢复"这条路永远走不通。
// 字段缺省即按恢复处理; 界面上两个动作都会显式带上它, 不依赖这个兜底。
type scheduledProbeCredentialRequest struct {
	ChannelID int    `json:"channel_id" binding:"required"` // 凭据所属的渠道。
	ModelName string `json:"model_name" binding:"required"` // 凭据所属的模型目标。
	KeyName   string `json:"key_name"`                      // 凭据名称; 空串是合法名称, 对应界面上的 #<授权ID>。
	Excluded  bool   `json:"excluded"`                      // 真为隐藏, 假为恢复。
}

// setScheduledProbeCredential 隐藏或恢复某个目标下的一条凭据, 并把该任务最新的行列表回给界面。
//
// 回整份行列表而不是只回一个成功标记: 排除项一变, 该出行的是哪些凭据也跟着变,
// 而这份结果是服务端按同一套规则算出来的, 调用方就不必自己推算"删掉这条之后还剩几行"。
func setScheduledProbeCredential(c *gin.Context) {
	id, err := strconv.Atoi(c.Param("id"))
	if err != nil {
		resp.Error(c, http.StatusBadRequest, resp.ErrInvalidParam)
		return
	}
	var req scheduledProbeCredentialRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		resp.Error(c, http.StatusBadRequest, err.Error())
		return
	}
	probe, err := op.ScheduledProbeCredentialSet(id, req.ChannelID, req.ModelName, req.KeyName, req.Excluded, c.Request.Context())
	if err != nil {
		resp.Error(c, http.StatusBadRequest, err.Error())
		return
	}
	// 可测凭据的集合变了, 旧的轮转进度便不再对应任何一条真实凭据; 丢掉它, 让下一拍按新集合重排。
	// 与配置更新的处理保持一致: 配置动了就不该再按旧节奏走。
	relay.ResetScheduledProbe(id)
	view := model.ScheduledProbeView{ScheduledProbe: probe}
	view.Rows = probeRows(view)
	resp.Success(c, view)
}
