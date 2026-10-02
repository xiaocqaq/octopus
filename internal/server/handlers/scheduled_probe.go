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
			router.NewRoute("/iq/:id", http.MethodPost).
				Handle(probeScheduledIQNow),
		).
		AddRoute(
			router.NewRoute("/credential/:id", http.MethodPost).
				Handle(setScheduledProbeCredential),
		).
		AddRoute(
			router.NewRoute("/order/:id", http.MethodPost).
				Handle(setScheduledProbeOrder),
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

// probeScheduledIQNow 手动把整条任务的每条凭据都问一遍智商题, 供卡片上的糖果按钮使用。
//
// 与 probeScheduledNow 同形(入参、错误码、返回的行形状都一致), 差别只在发出去的请求:
// 那一处发 "hi" 要一个可用性结论, 这一处发糖果题要一个能力结论。返回整行而不是只返回对错,
// 是因为界面上要更新的正是那一行的智商那一格 —— 形状一致, 前端不必写第二套更新逻辑。
func probeScheduledIQNow(c *gin.Context) {
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

	results, err := relay.ProbeScheduledIQNow(c.Request.Context(), probe)
	if err != nil {
		// 与立即测活同一口径: 没有可测凭据是配置状态而不是通道故障, 回请求错误让前端提示去检查渠道。
		resp.Error(c, http.StatusBadRequest, err.Error())
		return
	}
	resp.Success(c, probeRowsOf(probe, results))
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
	// 删之前先取出它监控过哪些凭据: 结论表按授权主键索引, 任务一删就再也推不出这些主键了。
	// 结论不设有效期, 不主动丢掉的话, 这些凭据会永远挂着最后一次的绿或红 ——
	// 而"这条通道还通不通"已经没有人再负责回答了。
	if probe, ok := op.ScheduledProbeGet(id); ok {
		relay.ForgetScheduledProbeResults(creditGrantIDs(op.ScheduledProbeCredits(probe)))
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

// scheduledProbeCreditRef 定位一条凭据: 渠道 + 模型 + 凭据名。
// 与行内隐藏用同一套定位方式, 界面不必为排序另算一份标识。
type scheduledProbeCreditRef struct {
	ChannelID int    `json:"channel_id" binding:"required"` // 凭据所属的渠道。
	ModelName string `json:"model_name" binding:"required"` // 凭据所属的模型目标。
	KeyName   string `json:"key_name"`                      // 凭据名称; 空串是合法名称, 对应界面上的 #<授权ID>。
}

// scheduledProbeOrderRequest 是拖动排序后的提交: 按界面上的先后依次给出凭据。
//
// 收定位三元组而不是服务端内部的顺序键: 顺序键里的分隔符是实现细节,
// 让它漏进接口就等于把它固化成契约, 以后换一种拼法都成了破坏性变更。
type scheduledProbeOrderRequest struct {
	Credits []scheduledProbeCreditRef `json:"credits" binding:"required,min=1,dive"`
}

// setScheduledProbeOrder 记下用户拖出来的行顺序, 并把该任务最新的行列表回给界面。
//
// 整体替换而不是增量维护: 界面送来的是它此刻看得见的全部行, 那本就是一个完整的顺序答案。
func setScheduledProbeOrder(c *gin.Context) {
	id, err := strconv.Atoi(c.Param("id"))
	if err != nil {
		resp.Error(c, http.StatusBadRequest, resp.ErrInvalidParam)
		return
	}
	var req scheduledProbeOrderRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		resp.Error(c, http.StatusBadRequest, err.Error())
		return
	}

	keys := make([]string, 0, len(req.Credits))
	for _, credit := range req.Credits {
		keys = append(keys, op.CreditKey(credit.ChannelID, credit.ModelName, credit.KeyName))
	}
	probe, err := op.ScheduledProbeCreditOrderSet(id, keys, c.Request.Context())
	if err != nil {
		resp.Error(c, http.StatusBadRequest, err.Error())
		return
	}
	// 顺序即轮转顺序, 故顺序一变进度也得丢: 不丢的话游标还停在上一次的下标上,
	// 用户把某条拖到最前, 下一拍测的却是别的一条 —— 拖动看起来没生效。
	relay.ResetScheduledProbe(id)
	view := model.ScheduledProbeView{ScheduledProbe: probe}
	view.Rows = probeRows(view)
	resp.Success(c, view)
}
