import { useMutation, useQuery } from '@tanstack/react-query';
import { useEffect } from 'react';
import { apiRequest } from './client';
import { queryClient } from './client';
import { groupListQueryOptions } from './queries';

// GroupMode 表示分组的手动或故障转移路由模式。
export type GroupMode = 'manual' | 'failover';

// GroupRelayConfig 保存分组 Relay 配置。
export interface GroupRelayConfig {
    member_max_attempts: number;
    member_retry_interval_seconds: number;
    member_non_stream_response_timeout_seconds: number;
    member_stream_first_event_timeout_seconds: number;
    // member_stream_total_timeout_seconds 是一轮流式响应从发起到收到终止事件的总时长上限。
    // 首事件超时只管到首帧，首帧之后上游长时间不发新事件同样要掐断，故另设这一道闸门。
    member_stream_total_timeout_seconds: number;
    member_cooldown_seconds: number;
    member_affinity_seconds: number;
    reasoning_filter: boolean; // 报错重发时按可移植标准清洗历史; 加密思维链按分组开, 因同一供应商下并非每个模型都签发。
}

// GroupItem 是分组内一条可路由的成员，对应一条渠道授权。
// 名称、所属渠道与可用性由后端补齐：授权是 (模型, 凭据) 的组合，界面只需展示与排序，无需再按主键回查。
export interface GroupItem {
    id: number;
    group_id: number;
    channel_grant_id: number;
    priority: number;
    channel_id: number;
    channel_name: string;
    model_name: string;
    key_name: string;
    protocols: number; // 该授权支持的 Protocol 位掩码。
    available: boolean; // 为假表示该成员当前无法转发，但仍会列出以便移除。
}

// GroupRuntime 是分组的实时路由状态。
// current_item_id 两种模式共用：手动模式下即人工指定的成员，故障转移模式下由 Relay 的路由决定。
export interface GroupRuntime {
    group_id: number;
    current_item_id: number;
    probe_item_id: number;
    affinity_until: number;
    cooldowns: Record<number, number>;
    // scores 是成员健康分：正分在选路时上浮、负分下沉，0 表示按配置优先级。仅故障转移模式会累积。
    // 它是“已按时间衰减到收到这份数据那一刻”的值（调用结果累积的基础分），不含测活加权；
    // 加权由前端按 PROBE_SCORE_MAX / PROBE_VOTE_DOWN 现算（见 probeScoreVote）。
    scores: Record<number, number>;
    // score_at 是每个分数的计时起点（Unix 毫秒）：从该时刻起每过 SCORE_STEP_TTL_MS 就再少一档。
    // 后端只给当前有效的分数，没有条目的成员即无偏移；前端据此把剩下的档位在页面内自己走完。
    score_at: Record<number, number>;
    // probes 是成员最近一次人工测活（体检）的结论，仅由测活写入。
    // 与 scores 分开：scores 还会被真实调用结果升降，由此界面能区分“这条结论来自我的体检”还是“来自线上调用”。
    // 结论只有 PROBE_RESULT_TTL_MS 的有效期，过期的结论后端不会再返回，前端也不用展示。
    probes: Record<number, GroupProbeResult>;
}

// PROBE_RESULT_TTL_MS 是一次人工测活结论的有效期，与后端 probeResultTTL（internal/relay/probe.go）必须一致。
// 两侧各管一段：后端保证“读出来就已经没有过期的结论”（刷新、换设备、新标签页都一致），
// 前端保证“页面开着不动时，到点那个徽标自己消失”（不依赖后端再推一条消息）。
// 有效期一过，排名提升与体检徽标一起消失——想看就重新测活。
export const PROBE_RESULT_TTL_MS = 5 * 60 * 1000;

// PROBE_SCORE_MAX 与 PROBE_VOTE_DOWN 是测活结论对健康分的加权档位，
// 必须与后端 routeScoreMax / probeVoteDown（internal/relay/route.go、probe.go）保持一致。
// 后端发布出去的 scores 只是调用结果累积的基础分，测活加权由两侧按同一条规则现算：
// 后端选路时算，前端展示时算——结论一过期，两边同时不再把它算进去。
export const PROBE_SCORE_MAX = 3;
export const PROBE_VOTE_DOWN = -1;

// probeScoreVote 是一条仍在有效期内的测活结论对健康分的加权（传入前先过 freshProbe）。
export function probeScoreVote(probe: GroupProbeResult): number {
    return probe.ok ? PROBE_SCORE_MAX : PROBE_VOTE_DOWN;
}

// SCORE_STEP_TTL_MS 是一档健康分的寿命，必须与后端 routeScoreStepTTL（internal/relay/route.go）一致。
// 上游的限流、余额、网络都会恢复，分数不该是永久判决：从计时起点起每过这段时间就有一档向 0 走，
// 最重的 -3 最多 3 个周期回到中性，成员随即按配置优先级重新排队。
export const SCORE_STEP_TTL_MS = 5 * 60 * 1000;

// activeScore 把后端给的分数按时间起点继续走完剩下的档位，返回此刻应展示的值。
// at 为 0 表示这份分数不带时效（旧数据），直接按原值显示。
export function activeScore(score: number, at: number, now: number): number {
    if (score === 0 || !at) return score;
    const steps = Math.floor((now - at) / SCORE_STEP_TTL_MS);
    if (steps <= 0) return score;
    // 过期的档位一次走完，但不越过 0：分只会回到中性，不会反过来变成反向的分。
    return score > 0 ? Math.max(score - steps, 0) : Math.min(score + steps, 0);
}

// scoreDeadline 是这份分数彻底归零的时刻（Unix 毫秒），供页面的共享时钟决定要走到什么时候。
export function scoreDeadline(score: number, at: number): number {
    if (score === 0 || !at) return 0;
    return at + Math.abs(score) * SCORE_STEP_TTL_MS;
}

// GroupProbeResult 是一次人工测活的结论。
export interface GroupProbeResult {
    group_id: number;
    item_id: number;
    ok: boolean;
    latency_ms: number;
    message: string; // 成功时为空，失败时为上游错误正文或本地配置错误。
    probed_at: number; // 结论产生时间，Unix 毫秒。
}

// Group 是客户端模型名称对应的渠道分组。
export interface Group {
    id: number;
    name: string;
    mode: GroupMode;
    // pinned_item_id 是故障转移模式下强制优先使用的成员，0 表示不强制。
    // 与 active_item_id 不同，它不被响应遮蔽：强制是写入侧的持久配置，界面要据此高亮那一个成员。
    pinned_item_id: number;
    relay_config: GroupRelayConfig;
    items: GroupItem[]; // 恒为数组，后端读取侧承诺不为 null。
    runtime: GroupRuntime; // 随分组一并返回；当前成员一律读 runtime.current_item_id。
}

// GroupItemInput 是提交的成员，按渠道授权主键引用；提交顺序即优先级顺序。
export interface GroupItemInput {
    channel_grant_id: number;
}

// GroupCreateRequest 是创建分组的请求。
export interface GroupCreateRequest {
    name: string;
    mode: GroupMode;
    relay_config: GroupRelayConfig;
    items: GroupItemInput[];
}

// GroupUpdateRequest 是分组配置、成员与当前成员的变更；items 为整体替换，按授权主键匹配保留已有成员。
// 只提交发生变化的字段；当前成员是分组的普通字段，与其余变更共用本请求。
export interface GroupUpdateRequest {
    name?: string;
    mode?: GroupMode;
    relay_config?: GroupRelayConfig;
    items?: GroupItemInput[];
    active_item_id?: number; // 手动模式指定的当前成员，0 表示取消选择。
    pinned_item_id?: number; // 故障转移模式下强制优先使用的成员，0 表示取消强制。
}

// writeGroupCache 把一份分组写回列表与详情两处缓存，已存在则替换，不存在则插入。
// 写操作的响应与事件流都经由此处收敛，两处读的是同一份数据，无需再重新拉取列表。
function writeGroupCache(group: Group) {
    queryClient.setQueryData(groupListQueryOptions.queryKey, (current: Group[] | undefined) => {
        if (!current) return current;
        const next = current.filter((item) => item.id !== group.id);
        next.push(group);
        // 与后端 op.GroupList 的定序保持一致：API Key 表单的模型选择器没有排序开关，依赖列表自带的名称顺序。
        next.sort((a, b) => (a.name < b.name ? -1 : a.name > b.name ? 1 : 0));
        return next;
    });
    queryClient.setQueryData(['groups', 'detail', group.id], group);
}

// removeGroupCache 从列表与详情两处缓存移除一个分组。
function removeGroupCache(id: number) {
    queryClient.setQueryData(groupListQueryOptions.queryKey, (current: Group[] | undefined) =>
        current?.filter((item) => item.id !== id)
    );
    queryClient.removeQueries({ queryKey: ['groups', 'detail', id] });
}

// useGroupList 获取全部分组，并由明确需要实时状态的页面控制是否订阅事件流。
export function useGroupList(enabled = true, eventsEnabled = false) {
    const query = useQuery({ ...groupListQueryOptions, enabled });

    useGroupEventStream(enabled && eventsEnabled);

    return query;
}

// useGroup 获取单个分组，供只关心一个分组的页面使用；实时状态按需订阅。
export function useGroup(id: number | undefined, enabled = true, eventsEnabled = false) {
    const query = useQuery({
        queryKey: ['groups', 'detail', id],
        queryFn: () => apiRequest<Group>(`/api/v1/group/get/${id}`),
        enabled: enabled && id !== undefined,
    });

    useGroupEventStream(enabled && eventsEnabled);

    return query;
}

// groupEventSource 是全应用共享的一条分组事件连接，由订阅方引用计数维持。
// 列表页与日志详情可能同时订阅，各自建连会白占浏览器对同域的连接数。
let groupEventSource: EventSource | null = null;
let groupEventRefCount = 0; // 当前订阅该连接的组件数量，归零时关闭连接。

// useGroupEventStream 订阅分组的变更事件与运行状态增量，并写回列表与详情两处缓存。
function useGroupEventStream(enabled: boolean) {
    useEffect(() => {
        if (!enabled) return;

        groupEventRefCount++;
        if (!groupEventSource) {
            const source = new EventSource('/api/v1/group/events', { withCredentials: true });
            groupEventSource = source;
            source.addEventListener('changed', (event) => {
                writeGroupCache(JSON.parse((event as MessageEvent<string>).data) as Group);
            });
            source.addEventListener('deleted', (event) => {
                removeGroupCache(Number((event as MessageEvent<string>).data));
            });
            source.addEventListener('runtime', (event) => {
                const update = JSON.parse((event as MessageEvent<string>).data) as GroupRuntime;
                queryClient.setQueryData(groupListQueryOptions.queryKey, (current: Group[] | undefined) =>
                    current?.map((group) => group.id === update.group_id ? { ...group, runtime: update } : group)
                );
                queryClient.setQueryData(['groups', 'detail', update.group_id], (current: Group | undefined) =>
                    current && { ...current, runtime: update }
                );
            });
            // 后端不留事件历史，连接断开期间的变更无从补发，故每次连上都重新拉取一次对齐。
            // 拉取放在 onopen 而非 onerror: EventSource 自行重连，onerror 在每次失败时都会触发，
            // 写在那里会让断网期间反复全量重拉。
            source.onopen = () => {
                queryClient.invalidateQueries({ queryKey: groupListQueryOptions.queryKey });
                queryClient.invalidateQueries({ queryKey: ['groups', 'detail'] });
            };
        }

        return () => {
            groupEventRefCount--;
            if (groupEventRefCount > 0) return;
            groupEventSource?.close();
            groupEventSource = null;
        };
    }, [enabled]);
}

// useCreateGroup 创建分组。
export function useCreateGroup() {
    return useMutation({
        mutationFn: (data: GroupCreateRequest) =>
            apiRequest<Group>('/api/v1/group/create', { method: 'POST', body: data }),
        onSuccess: writeGroupCache,
    });
}

// useUpdateGroup 更新分组配置、成员或当前成员，响应即变更后的完整分组。
export function useUpdateGroup() {
    return useMutation({
        mutationFn: ({ id, ...data }: GroupUpdateRequest & { id: number }) =>
            apiRequest<Group>(`/api/v1/group/update/${id}`, { method: 'POST', body: data }),
        onSuccess: writeGroupCache,
    });
}

// useDeleteGroup 删除分组。
export function useDeleteGroup() {
    return useMutation({
        mutationFn: (id: number) =>
            apiRequest<null>(`/api/v1/group/delete/${id}`, { method: 'DELETE' }),
        onSuccess: (_, id) => removeGroupCache(id),
    });
}

// GroupAddChannelResponse 是一键把渠道授权追加为分组成员的响应。
// added 是本次实际新增的成员数(已在分组内的授权不重复计入), group 是更新后的完整分组。
export interface GroupAddChannelResponse {
    added: number;
    group: Group;
}

// useAddChannelToGroup 一键把指定渠道的全部有效授权追加为分组成员。
// 响应与事件流都带完整分组, 经 writeGroupCache 收敛进缓存; 事件流随后的 changed 事件会写入同一份数据, 幂等。
export function useAddChannelToGroup() {
    return useMutation({
        mutationFn: ({ groupId, channelId }: { groupId: number; channelId: number }) =>
            apiRequest<GroupAddChannelResponse>(`/api/v1/group/add-channel/${groupId}/${channelId}`, {
                method: 'POST',
                body: {},
            }),
        onSuccess: (data) => writeGroupCache(data.group),
    });
}

// useProbeGroupItem 测活单个成员（体检一条）。
// 结论由后端写进路由状态并经事件流广播，故此处不写缓存：重复写会让本地的乐观值与随后到达的事件互相覆盖。
export function useProbeGroupItem() {
    return useMutation({
        mutationFn: ({ groupId, itemId, streaming }: { groupId: number; itemId: number; streaming?: boolean }) =>
            apiRequest<GroupProbeResult>(`/api/v1/group/probe/${groupId}/${itemId}`, {
                method: 'POST',
                body: { streaming: streaming ?? false },
            }),
    });
}

// useProbeGroup 一键测活：不传 itemIds 时测分组内全部成员。
export function useProbeGroup() {
    return useMutation({
        mutationFn: ({ groupId, itemIds, streaming }: { groupId: number; itemIds?: number[]; streaming?: boolean }) =>
            apiRequest<GroupProbeResult[]>(`/api/v1/group/probe/${groupId}`, {
                method: 'POST',
                body: { item_ids: itemIds ?? [], streaming: streaming ?? false },
            }),
    });
}
