package handlers

import (
	"net/http"
	"strconv"
	"sync"
	"time"

	"github.com/bestruirui/octopus/internal/model"
	"github.com/bestruirui/octopus/internal/op"
	"github.com/bestruirui/octopus/internal/relay"
	"github.com/bestruirui/octopus/internal/server/middleware"
	"github.com/bestruirui/octopus/internal/server/resp"
	"github.com/bestruirui/octopus/internal/server/router"
	"github.com/gin-contrib/sse"
	"github.com/gin-gonic/gin"
)

func init() {
	router.NewGroupRouter("/api/v1/group").
		Use(middleware.Auth()).
		Use(middleware.RequireJSON()).
		AddRoute(
			router.NewRoute("/list", http.MethodGet).
				Handle(getGroupList),
		).
		AddRoute(
			router.NewRoute("/get/:id", http.MethodGet).
				Handle(getGroup),
		).
		AddRoute(
			router.NewRoute("/events", http.MethodGet).
				Handle(streamGroupEvents),
		).
		AddRoute(
			router.NewRoute("/create", http.MethodPost).
				Handle(createGroup),
		).
		AddRoute(
			router.NewRoute("/update/:id", http.MethodPost).
				Handle(updateGroup),
		).
		AddRoute(
			router.NewRoute("/delete/:id", http.MethodDelete).
				Handle(deleteGroup),
		)
}

// groupResponse 是分组读取响应: 分组配置加其当前实时路由状态。
// 路由状态由 Relay 持有且不落库, 不属于分组配置, 故在此拼接而不作为 model.Group 的字段;
// 嵌入的分组字段在 JSON 中展平, 前端看到的仍是一层对象。
type groupResponse struct {
	model.Group
	// ActiveItemID 恒为零值, 只为遮蔽嵌入分组的同名字段: 当前成员一律从 runtime.current_item_id 读,
	// 出两份会让消费方无从选择, 而故障转移模式下嵌入的那份只是写入侧的陈旧值。
	// model.Group 上的 tag 供数据库转储使用不能删, 遮蔽也不能用 json:"-" ——
	// 带该 tag 的字段被 encoding/json 直接跳过, 不进候选集也就不参与同名冲突消解, 嵌入的那份仍会输出;
	// 同名加 omitempty 才既胜出又因零值被省略。
	ActiveItemID int              `json:"active_item_id,omitempty"`
	Runtime      relay.RouteState `json:"runtime"` // 分组当前的实时路由状态。
}

// 分组变更事件, SSE 收到后按事件名原样转发。
type groupEvent struct {
	Name string // SSE 事件名: changed 表示分组配置或成员发生变更, deleted 表示分组已删除。
	Data any    // changed 携带完整的 groupResponse, deleted 携带分组 ID。
}

const groupEventBuffer = 16 // 单个分组事件流连接的非阻塞消息缓冲容量。

var (
	groupEventMu      sync.Mutex                           // groupEventMu 保护全部分组事件流连接。
	groupEventStreams = make(map[chan groupEvent]struct{}) // 全部分组事件流 SSE 连接。
)

// publishGroupEvent 非阻塞发布一条分组变更事件, 连接拥塞时关闭它并交给客户端重连后重新拉取对齐。
// 发布点在此而非 op: 事件携带的运行状态取自 Relay, 而 Relay 依赖 op, 放进 op 会形成循环依赖。
func publishGroupEvent(event groupEvent) {
	groupEventMu.Lock()
	defer groupEventMu.Unlock()

	for stream := range groupEventStreams {
		select {
		case stream <- event:
		default:
			delete(groupEventStreams, stream)
			close(stream)
		}
	}
}

// streamGroupEvents 向前端发送分组的变更事件与实时运行状态。
// 一条连接承载两个来源: 分组的增删改由本包发布, 故障转移模式的运行状态增量由 Relay 的路由自身发布。
// 不发初始快照: 分组读取接口已随分组带回当前状态, 前端由此拿到的初始值即全量。
func streamGroupEvents(c *gin.Context) {
	prepareSSE(c)
	routeUpdates := relay.OpenRouteStream()
	defer relay.CloseRouteStream(routeUpdates)

	events := make(chan groupEvent, groupEventBuffer)
	groupEventMu.Lock()
	groupEventStreams[events] = struct{}{}
	groupEventMu.Unlock()
	defer func() {
		groupEventMu.Lock()
		defer groupEventMu.Unlock()
		// 通道可能已被拥塞时的发布方关闭, 重复关闭会 panic。
		if _, exists := groupEventStreams[events]; exists {
			delete(groupEventStreams, events)
			close(events)
		}
	}()

	heartbeat := time.NewTicker(15 * time.Second)
	defer heartbeat.Stop()
	for {
		select {
		case <-c.Request.Context().Done():
			return
		case <-heartbeat.C:
			if _, err := c.Writer.Write([]byte(": ping\n\n")); err != nil {
				return
			}
			c.Writer.Flush()
		case update, ok := <-routeUpdates:
			if !ok {
				return
			}
			if err := sse.Encode(c.Writer, sse.Event{Event: "runtime", Data: update}); err != nil {
				return
			}
			c.Writer.Flush()
		case event, ok := <-events:
			if !ok {
				return
			}
			if err := sse.Encode(c.Writer, sse.Event{Event: event.Name, Data: event.Data}); err != nil {
				return
			}
			c.Writer.Flush()
		}
	}
}

// getGroupList 返回全部分组, 并为每个分组补齐当前的实时路由状态。
// 路由状态由 Relay 持有而 Relay 依赖 op, 故补齐点放在此处而非 op.GroupList 内。
func getGroupList(c *gin.Context) {
	groups := op.GroupList()
	responses := make([]groupResponse, len(groups))
	for i, group := range groups {
		responses[i] = groupResponse{Group: group, Runtime: relay.RouteStateOf(group)}
	}
	resp.Success(c, responses)
}

// getGroup 返回单个分组及其当前实时路由状态。
// 日志详情只关心承载该请求的那一个分组, 由此无需拉取整份分组列表。
func getGroup(c *gin.Context) {
	id, err := strconv.Atoi(c.Param("id"))
	if err != nil {
		resp.Error(c, http.StatusBadRequest, err.Error())
		return
	}
	group, err := op.GroupGet(id)
	if err != nil {
		resp.Error(c, http.StatusNotFound, err.Error())
		return
	}
	resp.Success(c, groupResponse{Group: group, Runtime: relay.RouteStateOf(group)})
}

func createGroup(c *gin.Context) {
	var req model.GroupCreateRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		resp.Error(c, http.StatusBadRequest, err.Error())
		return
	}
	group, err := op.GroupCreate(&req, c.Request.Context())
	if err != nil {
		resp.Error(c, http.StatusInternalServerError, err.Error())
		return
	}
	response := groupResponse{Group: *group, Runtime: relay.RouteStateOf(*group)}
	publishGroupEvent(groupEvent{Name: "changed", Data: response})
	resp.Success(c, response)
}

// updateGroup 更新分组配置, 成员和手动模式的当前成员。
// 变更后的完整分组一并推入事件流: 其他会话由此同步到新的成员与配置, 无需各自重新拉取列表;
// 手动模式的当前成员也随之出去, 它由分组配置定稿, Relay 自身不会为它发运行状态增量。
func updateGroup(c *gin.Context) {
	id, err := strconv.Atoi(c.Param("id"))
	if err != nil {
		resp.Error(c, http.StatusBadRequest, err.Error())
		return
	}
	var req model.GroupUpdateRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		resp.Error(c, http.StatusBadRequest, err.Error())
		return
	}
	oldGroup, err := op.GroupGet(id)
	if err != nil {
		resp.Error(c, http.StatusNotFound, err.Error())
		return
	}
	group, err := op.GroupUpdate(id, &req, c.Request.Context())
	if err != nil {
		resp.Error(c, http.StatusInternalServerError, err.Error())
		return
	}
	// 选择模式变化后进程内路由不再适用, 丢弃它以免旧的冷却与亲和在切回故障转移时复活。
	if oldGroup.Mode != group.Mode {
		relay.ResetRouteState(id)
	}
	// 人工新指定强制成员时解除它的冷却: 指定即"现在就用它", 不该被残留的冷却挡住。
	if req.PinnedItemID != nil && *req.PinnedItemID != 0 {
		relay.ReleaseItemCooldown(id, *req.PinnedItemID)
	}
	response := groupResponse{Group: *group, Runtime: relay.RouteStateOf(*group)}
	publishGroupEvent(groupEvent{Name: "changed", Data: response})
	resp.Success(c, response)
}

func deleteGroup(c *gin.Context) {
	id, err := strconv.Atoi(c.Param("id"))
	if err != nil {
		resp.Error(c, http.StatusBadRequest, err.Error())
		return
	}
	if err := op.GroupDel(id, c.Request.Context()); err != nil {
		resp.Error(c, http.StatusInternalServerError, err.Error())
		return
	}
	relay.ResetRouteState(id)
	publishGroupEvent(groupEvent{Name: "deleted", Data: id})
	resp.Success(c, "group deleted successfully")
}
