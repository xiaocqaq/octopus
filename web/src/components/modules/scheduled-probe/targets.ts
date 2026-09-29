import type { ScheduledProbeTarget } from '@/api/scheduled-probe';

// targetKey 是一条目标的身份，形状与后端去重用的键一致（渠道 + 模型）。
export function targetKey(target: ScheduledProbeTarget): string {
    return `${target.channel_id}\u0000${target.model_name}`;
}

// sameTargetSet 比较两组目标是否等价，只问集合不问顺序：顺序会被展开逻辑重排，
// 但"还是那几条"与"多出来几条"必须分得清 —— 编辑旧任务时正是靠它发现目标被撑大了。
export function sameTargetSet(left: ScheduledProbeTarget[], right: ScheduledProbeTarget[]): boolean {
    if (left.length !== right.length) return false;
    const keys = new Set(left.map(targetKey));
    return right.every((target) => keys.has(targetKey(target)));
}

// distinct 去掉重复值，保留首次出现的顺序：编辑态由既存目标反推渠道与模型时，
// 同一渠道或同一模型会出现很多次，而用户勾选时的顺序是有意义的（预览按它排）。
export function distinct<T>(values: T[]): T[] {
    return [...new Set(values)];
}

// expandTargets 把「已选渠道 × 已选模型」展开成监控目标，只保留该渠道确实提供的模型。
//
// 过滤放在这一步而不是交给后端：后端按 (渠道, 模型) 校验授权，模型不属于该渠道的组合必被拒；
// 让用户先配出一个提交不了的组合、再回来读报错，等于把校验成本推给了用户。
//
// 顺序是「渠道优先」：先按用户勾选渠道的顺序，再按该渠道自己的模型顺序，
// 于是同一个模型被多个渠道提供时它们会挨在一起，预览里一眼能看出重复来自哪几家。
export function expandTargets(
    channelIds: number[],
    modelNames: string[],
    modelsOf: Map<number, string[]>
): ScheduledProbeTarget[] {
    const wanted = new Set(modelNames);
    const seen = new Set<string>();
    const targets: ScheduledProbeTarget[] = [];
    for (const channelId of channelIds) {
        for (const modelName of modelsOf.get(channelId) ?? []) {
            if (!wanted.has(modelName)) continue;
            // 去重是为了预览不说谎：后端提交时也会按 (渠道, 模型) 去重，前端多显示一条就白让用户数一遍。
            const key = `${channelId}\u0000${modelName}`;
            if (seen.has(key)) continue;
            seen.add(key);
            targets.push({ channel_id: channelId, model_name: modelName });
        }
    }
    return targets;
}
