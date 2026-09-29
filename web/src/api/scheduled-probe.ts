import { queryOptions, useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
import { apiRequest } from './client';

// ScheduledProbeRow 是卡片里的一行，对应一条渠道凭据（渠道名 + 凭据名）。
// 按凭据出行而不是按目标：一个目标可能挂多条凭据，只出一行就既看不到「哪条不通」，
// 也点不到那一行的手动测试按钮，而排查时唯一有用的粒度就是单条凭据。
export type ScheduledProbeRow = {
    grant_id: number; // 手动测试按它发起；为 0 表示该目标当下没有可测凭据。
    channel_id: number;
    channel_name: string;
    model_name: string;
    key_name: string;
    // probed 为假表示还没有仍在有效期内的结论，此时下面几个字段都无意义。
    probed: boolean;
    ok: boolean;
    latency_ms: number;
    message: string;
    probed_at: number;
};

// ScheduledProbeTarget 是一个被监控目标：某个渠道下的某个模型。
export type ScheduledProbeTarget = {
    channel_id: number;
    model_name: string;
    // excluded_keys 是该目标下被逐行删掉、不再监控的凭据名。
    //
    // 这个字段刻意可选，而且提交时「不传」与「传空数组」是两种意思：
    // 不传（undefined）表示这次提交不涉及排除项，后端沿用既有值——编辑表单只知道 (渠道, 模型)，
    // 走的正是这条路，所以编辑一次任务不会让删掉的凭据集体复活；
    // 传 [] 才是清空排除项，卡片里的「全部恢复」发的就是它。
    excluded_keys?: string[];
};

// ScheduledProbe 是「模型监控」的一条任务：一个自定义名字下面挂若干被监控目标。
// 名字通常就是模型名，但不拿目标反推：一个任务挂了多个模型时，需要一个能概括它们的称呼。
export type ScheduledProbe = {
    id: number;
    name: string;
    targets: ScheduledProbeTarget[];
    interval_minutes: number;
    enabled: boolean;
    // weekdays 是星期掩码（周一为第 0 位，周日为第 6 位），0 表示不限星期。
    // start_hour 与 end_hour 是每天的整点窗口；两者相等视为整天，start > end 表示跨午夜。
    weekdays: number;
    start_hour: number;
    end_hour: number;
    // rows 是各条凭据的当前状态，也是卡片上一行一条的渲染依据；无结论的凭据同样出行。
    rows: ScheduledProbeRow[];
    created_at: number;
    updated_at: number;
};

// WeekdayMonday 等位值必须与后端 model.Weekday* 一致：掩码是前后端共同读写的协议。
export const WeekdayMonday = 1 << 0;
export const WeekdayTuesday = 1 << 1;
export const WeekdayWednesday = 1 << 2;
export const WeekdayThursday = 1 << 3;
export const WeekdayFriday = 1 << 4;
export const WeekdaySaturday = 1 << 5;
export const WeekdaySunday = 1 << 6;
export const WeekdayAll = 0b1111111;

// SCHEDULED_PROBE_DEFAULT_INTERVAL 与后端 ScheduledProbeDefaultIntervalMinutes 一致。
export const SCHEDULED_PROBE_DEFAULT_INTERVAL = 10;

// SCHEDULED_PROBE_HOURS 是时间窗的可选整点，供下拉使用。
export const SCHEDULED_PROBE_HOURS = Array.from({ length: 24 }, (_, hour) => hour);

// ScheduledProbeInput 是创建与更新共用的提交体，读写同构。
export type ScheduledProbeInput = {
    name: string;
    targets: ScheduledProbeTarget[];
    interval_minutes: number;
    enabled: boolean;
    weekdays: number;
    start_hour: number;
    end_hour: number;
};

// scheduledProbeListQueryOptions 供页面查询与启动预取共享模型监控列表定义。
export const scheduledProbeListQueryOptions = queryOptions({
    queryKey: ['scheduled-probes', 'list'],
    queryFn: () => apiRequest<ScheduledProbe[]>('/api/v1/scheduled-probe/list'),
});

// useScheduledProbeList 读取全部模型监控任务。
export function useScheduledProbeList() {
    return useQuery(scheduledProbeListQueryOptions);
}

// useCreateScheduledProbe 新建一条模型监控任务。
// 后端在写入后立即唤醒调度器，故这里只需失效列表：无需重启即生效是后端的职责。
export function useCreateScheduledProbe() {
    const queryClient = useQueryClient();

    return useMutation({
        mutationFn: (data: ScheduledProbeInput) =>
            apiRequest<ScheduledProbe>('/api/v1/scheduled-probe/create', { method: 'POST', body: data }),
        onSuccess: () => queryClient.invalidateQueries({ queryKey: scheduledProbeListQueryOptions.queryKey }),
    });
}

// useUpdateScheduledProbe 整体替换一条任务；启停开关也走这里，避免再开一个只改 enabled 的接口。
export function useUpdateScheduledProbe() {
    const queryClient = useQueryClient();

    return useMutation({
        mutationFn: (data: ScheduledProbeInput & { id: number }) =>
            apiRequest<ScheduledProbe>(`/api/v1/scheduled-probe/update/${data.id}`, { method: 'POST', body: data }),
        onSuccess: () => queryClient.invalidateQueries({ queryKey: scheduledProbeListQueryOptions.queryKey }),
    });
}

// useDeleteScheduledProbe 删除一条任务。
export function useDeleteScheduledProbe() {
    const queryClient = useQueryClient();

    return useMutation({
        mutationFn: (id: number) =>
            apiRequest<null>(`/api/v1/scheduled-probe/delete/${id}`, { method: 'DELETE' }),
        onSuccess: () => queryClient.invalidateQueries({ queryKey: scheduledProbeListQueryOptions.queryKey }),
    });
}

// useProbeScheduledNow 把一条任务的全部目标与凭据测一遍。
// 必须传一个空对象当 body 而不是省略它：省略后 apiRequest 不会设 Content-Type，
// 而后端的 RequireJSON 会以 415 拒绝任何不带 application/json 的 POST —— 这个按钮就会永远点不动。
export function useProbeScheduledNow() {
    const queryClient = useQueryClient();

    return useMutation({
        mutationFn: (id: number) =>
            apiRequest<ScheduledProbeRow[]>(`/api/v1/scheduled-probe/probe/${id}`, { method: 'POST', body: {} }),
        onSuccess: () => queryClient.invalidateQueries({ queryKey: scheduledProbeListQueryOptions.queryKey }),
    });
}

// useProbeGrantNow 只测一条凭据，供卡片里那一行末尾的闪电按钮使用。
// 同样必须带 body，理由同上。
export function useProbeGrantNow() {
    const queryClient = useQueryClient();

    return useMutation({
        mutationFn: (grantId: number) =>
            apiRequest<unknown>(`/api/v1/scheduled-probe/probe-grant/${grantId}`, { method: 'POST', body: {} }),
        onSuccess: () => queryClient.invalidateQueries({ queryKey: scheduledProbeListQueryOptions.queryKey }),
    });
}

// ScheduledProbeCredentialInput 是隐藏/恢复一条凭据的提交形状。
export type ScheduledProbeCredentialInput = {
    id: number;
    channelId: number;
    modelName: string;
    keyName: string;
    // excluded 为真表示不再监控这条凭据，为假表示恢复监控。
    excluded: boolean;
};

// useSetScheduledProbeCredential 隐藏或恢复某个目标下的一条凭据，供卡片里那一行末尾的 × 使用。
//
// 单独一个接口而不是提交整条任务：那一行只知道自己的渠道、模型与凭据名，走整条提交就得把任务其余字段
// 也一并回传，一次行内删除会变成一次全量覆盖——用户此刻只想动一行，不该顺带承担覆盖别处的风险。
export function useSetScheduledProbeCredential() {
    const queryClient = useQueryClient();

    return useMutation({
        mutationFn: (data: ScheduledProbeCredentialInput) =>
            apiRequest<ScheduledProbe>(`/api/v1/scheduled-probe/credential/${data.id}`, {
                method: 'POST',
                body: {
                    channel_id: data.channelId,
                    model_name: data.modelName,
                    key_name: data.keyName,
                    excluded: data.excluded,
                },
            }),
        onSuccess: () => queryClient.invalidateQueries({ queryKey: scheduledProbeListQueryOptions.queryKey }),
    });
}
