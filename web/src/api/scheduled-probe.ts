import { queryOptions, useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
import { apiRequest } from './client';
import { channelGrantListQueryOptions } from './channel';

// ScheduledProbeRow 是卡片里的一行，对应一条渠道凭据（渠道名 + 凭据名）。
// 按凭据出行而不是按目标：一个目标可能挂多条凭据，只出一行就既看不到「哪条不通」，
// 也点不到那一行的手动测试按钮，而排查时唯一有用的粒度就是单条凭据。
export type ScheduledProbeRow = {
    grant_id: number; // 手动测试按它发起；每行都是真实可测凭据。
    channel_id: number;
    channel_name: string;
    model_name: string;
    key_name: string;
    // probed 为假表示还没有结论，此时下面几个字段都无意义。结论不设有效期，
    // 只会被下一次测活覆盖，界面按 probed_at 照实显示"多久以前"。
    probed: boolean;
    ok: boolean;
    latency_ms: number;
    message: string;
    probed_at: number;
    // iq_asked 标记这一行有没有被问过智商题。它必须与 iq_answer 分开看：
    // 「没问过」和「问了但答不出数字」的 iq_answer 都是空串，只看答案会把后一种吞成前一种。
    iq_asked: boolean;
    // iq_answer 是最近一次智商探针里模型给出的答案（后端已提取为末尾整数）；
    // iq_correct 标记它是否与标准答案一致。二者只在 iq_asked 为真时有意义。
    // 结论只在划到出题的那一拍或手动点糖果测试时刷新，因此它可能比 probed_at 显示的时间旧——这是正常的。
    iq_answer: string;
    iq_correct: boolean;
};

// ScheduledProbeTarget 是一个被监控目标：某个渠道下的某个模型。
export type ScheduledProbeTarget = {
    channel_id: number;
    model_name: string;
    // excluded_keys 是该目标下被逐行删除、不再监控的凭据名。
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

// 新建任务默认按"工作日 08:00–20:00"起手: 监控多半是为了盯住工作时段的上游可用性,
// 默认给一个能直接用的窗口, 比默认"不限时段"再让用户自己收窄更省事。
export const SCHEDULED_PROBE_DEFAULT_WEEKDAYS =
    WeekdayMonday | WeekdayTuesday | WeekdayWednesday | WeekdayThursday | WeekdayFriday;
export const SCHEDULED_PROBE_DEFAULT_START_HOUR = 8;
export const SCHEDULED_PROBE_DEFAULT_END_HOUR = 20;

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
// 定时测活在后台持续产生新结论，页面必须自己轮询才能看到：结论落点后没有针对监控页的推送，
// 只靠 mutation 后的失效刷新，用户开着页面不动就看不到后台测出的新结果。
export const scheduledProbeListQueryOptions = queryOptions({
    queryKey: ['scheduled-probes', 'list'],
    queryFn: () => apiRequest<ScheduledProbe[]>('/api/v1/scheduled-probe/list'),
    refetchInterval: 30000,
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

// useProbeScheduledIQNow 手动把一条任务的每条凭据都问一遍智商题（卡片上的糖果按钮）。
//
// 与 useProbeScheduledNow 分成两个 mutation 而不是一个带开关的：两者返回的行形状相同，
// 但点按钮的意图不同（一个问「通不通」，一个问「笨不笨」），按钮自己的 pending 状态也要各自独立——
// 共用一个 isPending 会让糖果测试转圈时闪电按钮也跟着变灰。
// body 同样必须传空对象，理由见上。
export function useProbeScheduledIQNow() {
    const queryClient = useQueryClient();

    return useMutation({
        mutationFn: (id: number) =>
            apiRequest<ScheduledProbeRow[]>(`/api/v1/scheduled-probe/iq/${id}`, { method: 'POST', body: {} }),
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

// ScheduledProbeOrderInput 是拖动排序后的提交形状：按界面上的先后依次给出凭据。
export type ScheduledProbeOrderInput = {
    id: number;
    credits: { channel_id: number; model_name: string; key_name: string }[];
};

// useSetScheduledProbeOrder 记下用户拖出来的行顺序。
//
// 这个顺序不只是好看：后端按同一份顺序轮转，拖到最前的那条就是下一拍最先被测的那条。
// 所以拖完必须回传服务端——只改本地渲染等于给用户一个假的次序。
export function useSetScheduledProbeOrder() {
    const queryClient = useQueryClient();

    return useMutation({
        mutationFn: (data: ScheduledProbeOrderInput) =>
            apiRequest<ScheduledProbe>(`/api/v1/scheduled-probe/order/${data.id}`, {
                method: 'POST',
                body: { credits: data.credits },
            }),
        onSuccess: () => queryClient.invalidateQueries({ queryKey: scheduledProbeListQueryOptions.queryKey }),
    });
}

// GroupMonitorItem 是分组里的一条成员，只取折算监控目标需要的三个字段。
export type GroupMonitorItem = { channel_id: number; model_name: string; key_name: string };

// GrantMonitorCandidate 是渠道里的一条授权候选，用来知道某个 (渠道, 模型) 下到底有哪些凭据。
export type GrantMonitorCandidate = { channel_id: number; model_name: string; key_name: string };

// pairKeyOf 生成目标的去重键，与后端 targetKey 同构（用不可见字符分隔，避免拼接撞键）。
const pairKeyOf = (channelID: number, modelName: string) => `${channelID}\u0000${modelName}`;

// monitorTargetsOfGroup 把分组里的成员折算成监控目标，并把分组没用到的凭据排除掉。
//
// 目标粒度是 (渠道, 模型)，但同一个 (渠道, 模型) 下可以挂多条凭据(key)。分组只用了其中一条时，
// 整条目标照搬会把没用到的凭据一并拖进监控 —— 实际踩过：某渠道下同一模型有 2 个 key，
// 分组里只加了 1 个，一键监控却把 2 个都测上了，用户看到的监控范围比他自己配的还大。
// 所以这里按分组实际用到的 key 反算 excluded_keys，让监控范围与分组一致。
//
// 归并按 (渠道, 模型)：同一对在分组里出现多次（不同优先级、不同 key）要折成一个目标，
// 但 key 要合并统计，不能只看第一条。也不按模型名跨渠道归并：同一模型名可能由多个渠道提供，
// 那是各自独立的通道。
export function monitorTargetsOfGroup(items: GroupMonitorItem[], candidates: GrantMonitorCandidate[]): ScheduledProbeTarget[] {
    const order: { channel_id: number; model_name: string }[] = [];
    const usedKeys = new Map<string, Set<string>>();
    for (const item of items) {
        const key = pairKeyOf(item.channel_id, item.model_name);
        if (!usedKeys.has(key)) {
            usedKeys.set(key, new Set());
            order.push({ channel_id: item.channel_id, model_name: item.model_name });
        }
        usedKeys.get(key)?.add(item.key_name);
    }

    const allKeys = new Map<string, string[]>();
    for (const candidate of candidates) {
        const key = pairKeyOf(candidate.channel_id, candidate.model_name);
        const keys = allKeys.get(key) ?? [];
        if (!keys.includes(candidate.key_name)) keys.push(candidate.key_name);
        allKeys.set(key, keys);
    }

    return order.map((pair) => {
        const key = pairKeyOf(pair.channel_id, pair.model_name);
        const used = usedKeys.get(key) ?? new Set<string>();
        // 候选里查不到这一对（渠道已停用等）时保持空排除项：宁可不排除，也不要凭空给出一条错误的范围。
        const excluded = (allKeys.get(key) ?? []).filter((keyName) => !used.has(keyName));
        return { channel_id: pair.channel_id, model_name: pair.model_name, excluded_keys: excluded };
    });
}

// excludeUnusedKeys 把分组没用到的凭据追加进既有目标的排除项。
//
// 只做加法，从不删除已有的排除项：用户在监控页上逐行删掉某条凭据是一次明确的取舍，
// 分组页的又一次点击不该把它复活。反过来"收窄"是安全的 —— 它只是让监控范围不再超出。
function excludeUnusedKeys(target: ScheduledProbeTarget, unused: string[]): ScheduledProbeTarget {
    const current = target.excluded_keys ?? [];
    const added = unused.filter((keyName) => !current.includes(keyName));
    if (added.length === 0) return target;
    return { ...target, excluded_keys: [...current, ...added] };
}

// ScheduledProbeMonitorResult 汇报"开启模型监控"这一步实际做了什么。
// 区分新建 / 新增目标 / 仅收窄：三种情况用户看到的都是"加上了"，但各自做了什么必须说清楚，
// 否则"没有新目标可加"会被误解成点击没生效。
export type ScheduledProbeMonitorResult = {
    name: string;
    created: boolean; // 真为新建了一条任务，假为并入既有任务。
    addedTargets: number; // 本次新增的目标数。
    narrowedTargets: number; // 本次仅收窄了监控范围的目标数（补了排除项）。
    existing: boolean; // 真表示这条任务此前已在监控里。
};

// useMonitorGroup 用一条分组开启模型监控：标题取分组名，配置取默认值。
//
// 同名任务已存在时并入而不是再建一条：分组卡片上那个图标点两次是很自然的动作，
// 而两条同名任务会让界面上出现两张一模一样的卡片，用户也无从判断该删哪一条。
// 并入时补齐缺的目标，并把分组没用到的凭据补进排除项；已有排除项一概不动。
export function useMonitorGroup() {
    const queryClient = useQueryClient();

    return useMutation({
        mutationFn: async ({ name, items }: { name: string; items: GroupMonitorItem[] }): Promise<ScheduledProbeMonitorResult> => {
            if (items.length === 0) throw new Error('empty-targets');

            // 候选授权列表决定"某 (渠道, 模型) 下有哪些凭据"，缺了它就算不出该排除谁。
            // 分组页的成员选择器本来就在用同一份查询, 这里通常直接命中缓存。
            const candidates = await queryClient.fetchQuery(channelGrantListQueryOptions);
            const targets = monitorTargetsOfGroup(items, candidates);

            const tasks = await apiRequest<ScheduledProbe[]>('/api/v1/scheduled-probe/list');
            const existing = tasks.find((task) => task.name === name);
            if (!existing) {
                await apiRequest<ScheduledProbe>('/api/v1/scheduled-probe/create', {
                    method: 'POST',
                    body: {
                        name,
                        targets,
                        interval_minutes: SCHEDULED_PROBE_DEFAULT_INTERVAL,
                        enabled: true,
                        weekdays: SCHEDULED_PROBE_DEFAULT_WEEKDAYS,
                        start_hour: SCHEDULED_PROBE_DEFAULT_START_HOUR,
                        end_hour: SCHEDULED_PROBE_DEFAULT_END_HOUR,
                    },
                });
                return { name, created: true, addedTargets: targets.length, narrowedTargets: 0, existing: false };
            }

            const wanted = new Map(targets.map((target) => [pairKeyOf(target.channel_id, target.model_name), target]));
            let narrowed = 0;
            const kept = existing.targets.map((target) => {
                const match = wanted.get(pairKeyOf(target.channel_id, target.model_name));
                if (!match) return target;
                const next = excludeUnusedKeys(target, match.excluded_keys ?? []);
                if (next !== target) narrowed += 1;
                return next;
            });
            const known = new Set(existing.targets.map((target) => pairKeyOf(target.channel_id, target.model_name)));
            const missing = targets.filter((target) => !known.has(pairKeyOf(target.channel_id, target.model_name)));

            if (missing.length === 0 && narrowed === 0) {
                return { name, created: false, addedTargets: 0, narrowedTargets: 0, existing: true };
            }

            await apiRequest<ScheduledProbe>(`/api/v1/scheduled-probe/update/${existing.id}`, {
                method: 'POST',
                body: {
                    name: existing.name,
                    targets: [...kept, ...missing],
                    interval_minutes: existing.interval_minutes,
                    enabled: existing.enabled,
                    weekdays: existing.weekdays,
                    start_hour: existing.start_hour,
                    end_hour: existing.end_hour,
                },
            });
            return { name, created: false, addedTargets: missing.length, narrowedTargets: narrowed, existing: true };
        },
        onSuccess: () => queryClient.invalidateQueries({ queryKey: scheduledProbeListQueryOptions.queryKey }),
    });
}
