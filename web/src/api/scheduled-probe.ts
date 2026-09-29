import { queryOptions, useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
import { apiRequest } from './client';

// ScheduledProbe 是「定时测活」的一条任务：按固定间隔轮流探测指定渠道下的指定模型。
// 顺序由后端按创建先后维护（轮转），前端不参与排序，故这里也不带任何权重字段。
export type ScheduledProbe = {
    id: number;
    channel_id: number;
    channel_name: string; // 后端补上的渠道名，列表直接展示，无需再拉渠道列表。
    model_name: string;
    interval_minutes: number;
    enabled: boolean;
    // weekdays 是星期掩码（周一为第 0 位，周日为第 6 位），0 表示不限星期。
    // start_hour 与 end_hour 是每天的整点窗口；两者相等视为整天，start > end 表示跨午夜。
    weekdays: number;
    start_hour: number;
    end_hour: number;
    // grant_count 是该渠道模型当前可探的授权数（凭据数）；为 0 说明渠道或凭据被停用，测活会空转。
    grant_count: number;
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
    channel_id: number;
    model_name: string;
    interval_minutes: number;
    enabled: boolean;
    weekdays: number;
    start_hour: number;
    end_hour: number;
};

// scheduledProbeListQueryOptions 供页面查询与启动预取共享定时测活列表定义。
export const scheduledProbeListQueryOptions = queryOptions({
    queryKey: ['scheduled-probes', 'list'],
    queryFn: () => apiRequest<ScheduledProbe[]>('/api/v1/scheduled-probe/list'),
});

// useScheduledProbeList 读取全部定时测活任务。
export function useScheduledProbeList() {
    return useQuery(scheduledProbeListQueryOptions);
}

// useCreateScheduledProbe 新建一条定时测活任务。
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
