import type { ScheduledProbeTarget } from '@/api/scheduled-probe';
import type { ChannelGrantCandidate } from '@/api/channel';

// 多选值把三个字段都编进去：渠道、凭据、模型。
// 后端的目标粒度只到 (渠道, 模型)，凭据那一段不进提交体，而是在提交前折算成
// excluded_keys —— 用户勾选的是"哪把钥匙"，目标仍然是"哪条通道的哪个模型"。
//
// 分隔符用不可见字符而不是 ':' 之类：模型名里带冒号、斜杠的都不罕见，
// 拿可见字符拼接迟早会撞键，把两个不同的目标解码成同一个。
const SEP = '\u0000';

// ParsedTargetOption 是一条选项解码后的全部信息。
export type ParsedTargetOption = {
    channelId: number;
    keyName: string;
    modelName: string;
    target: ScheduledProbeTarget;
};

// targetKey 生成多选值。keyName 为空表示"整条目标"（按渠道与模型复原，不带凭据筛选）。
export function targetKey(channelId: number, keyName: string, modelName: string): string {
    return `${channelId}${SEP}${keyName}${SEP}${modelName}`;
}

// targetFromKey 解码多选值。
//
// 与后端 targetKey（internal/op/scheduled_probe.go:333）同名但不是同一个东西：那个是 (渠道, 模型) 两段，
// 用于目标去重；这里是三段，多一段凭据名供界面使用。
//
// 校验渠道号必须是正整数、模型名非空；凭据名允许为空，但要能解码出第三段
// —— 少一段说明这不是本界面产生的值，宁可当作无效（返回 null）也不要猜。
export function targetFromKey(value: string): ParsedTargetOption | null {
    const parts = value.split(SEP);
    if (parts.length !== 3) return null;
    const [channel, keyName, modelName] = parts;
    const channelId = Number(channel);
    if (!/^[1-9]\d*$/.test(channel) || !Number.isSafeInteger(channelId)) return null;
    if (!modelName.trim()) return null;
    return { channelId, keyName, modelName, target: { channel_id: channelId, model_name: modelName } };
}

// targetValueOf 把后端的 (渠道, 模型) 目标还原成界面上的多选值，用于编辑时回填。
//
// 目标上不记凭据，故这里只能填一个空凭据段：回填出来的选项不带 key 名，
// 调用方必须再用 targetValueForKey 换成一条真实存在的凭据 —— 否则
// 用户在编辑框里会看到一个"空的凭据名"，而目标本身其实是有 key 的。
export function targetValueOf(target: ScheduledProbeTarget): string {
    return targetKey(target.channel_id, '', target.model_name);
}

// buildTargetOptions 把候选授权摊成多选选项，每个 (渠道, 凭据, 模型) 一条。
//
// 候选里同一 (渠道, 模型) 可以有多条凭据，各自一条；同名凭据去重（后端按名字定位凭据，
// 重复的名字会指向同一条，渲染两遍只会让用户以为漏勾了其中一条）。
export function buildTargetOptions(candidates: ChannelGrantCandidate[]): {
    value: string;
    channelId: number;
    keyName: string;
    modelName: string;
    label: string;
}[] {
    const seen = new Set<string>();
    const options: {
        value: string;
        channelId: number;
        keyName: string;
        modelName: string;
        label: string;
    }[] = [];
    for (const candidate of candidates) {
        const value = targetKey(candidate.channel_id, candidate.key_name, candidate.model_name);
        if (seen.has(value)) continue;
        seen.add(value);
        options.push({
            value,
            channelId: candidate.channel_id,
            keyName: candidate.key_name,
            modelName: candidate.model_name,
            // 三段用斜杠分隔，与卡片行(RowList)的「渠道名/凭据名/模型名」保持同一副面孔：
            // 下拉里勾的东西，就该和勾完之后卡片上显示的东西长得一样。
            // 斜杠有歧义（渠道名本身可能带斜杠），但标签只用于展示与搜索；
            // 选项的身份是上面那个 \u0000 分隔的 value，展示上的歧义不会串到数据里。
            label: `${candidate.channel_name}/${candidate.key_name}/${candidate.model_name}`,
        });
    }
    return options;
}

// targetValueForKey 在候选里给一个目标挑一条真实存在的凭据，用于回填。
//
// 优先挑该目标下第一条未被 excluded_keys 排除的凭据：编辑框默认展示的应该是
// "这条目标实际在测的那把钥匙"，而不是恰好排在最前、却早已被排除掉的那条。
// 全都排除光了就退回第一条，让用户至少看得到目标还在、只是没有任何凭据在测。
export function targetValueForKey(
    target: ScheduledProbeTarget,
    candidates: ChannelGrantCandidate[],
): string | null {
    const keys = candidates
        .filter((candidate) => candidate.channel_id === target.channel_id && candidate.model_name === target.model_name)
        .map((candidate) => candidate.key_name);
    if (keys.length === 0) return targetValueOf(target);
    const excluded = target.excluded_keys ?? [];
    const first = keys.find((keyName) => !excluded.includes(keyName)) ?? keys[0];
    return targetKey(target.channel_id, first, target.model_name);
}

// targetsOf 把界面上的多选值折算成后端提交体。
//
// 关键：同一个 (渠道, 模型) 下的多条凭据必须折成一个目标，没勾到的凭据进 excluded_keys。
// 目标粒度就是 (渠道, 模型)，直接按 (渠道, 凭据, 模型) 一条条提交会提交出重复目标，
// 后端的校验会拒绝，或者把同一对渠道模型拆成两条互相覆盖的目标。
// 勾到该 (渠道, 模型) 下的全部凭据时留空 excluded_keys 而不是列出所有名字：
// 空表示"没有排除项"，将来渠道新增凭据也会自动纳入监控；列全了则把将来的新凭据也一并挡在门外。
export function targetsOf(values: string[], candidates: ChannelGrantCandidate[]): ScheduledProbeTarget[] {
    const allKeys = new Map<string, string[]>();
    for (const candidate of candidates) {
        const pair = `${candidate.channel_id}${SEP}${candidate.model_name}`;
        const keys = allKeys.get(pair) ?? [];
        if (!keys.includes(candidate.key_name)) keys.push(candidate.key_name);
        allKeys.set(pair, keys);
    }

    const order: { channelId: number; modelName: string }[] = [];
    const picked = new Map<string, Set<string>>();
    for (const value of values) {
        const parsed = targetFromKey(value);
        if (!parsed) continue;
        const pair = `${parsed.channelId}${SEP}${parsed.modelName}`;
        if (!picked.has(pair)) {
            picked.set(pair, new Set());
            order.push({ channelId: parsed.channelId, modelName: parsed.modelName });
        }
        picked.get(pair)?.add(parsed.keyName);
    }

    return order.map(({ channelId, modelName }) => {
        const pair = `${channelId}${SEP}${modelName}`;
        const chosen = picked.get(pair) ?? new Set<string>();
        const keys = allKeys.get(pair) ?? [];
        // 候选里查不到这一对（渠道刚被停用或删除）时保持空排除项：宁可不排除，
        // 也不要凭一份过期的候选列表给出一条错误的监控范围。
        const excluded = keys.filter((keyName) => !chosen.has(keyName));
        return { channel_id: channelId, model_name: modelName, excluded_keys: excluded };
    });
}
