import type { ScheduledProbeTarget } from '@/api/scheduled-probe';

// 组合身份只用于多选值；不把渠道、模型分别展开成笛卡尔积。
export function targetKey(target: ScheduledProbeTarget): string {
    return `${target.channel_id}\u0000${target.model_name}`;
}

export function targetFromKey(value: string): ScheduledProbeTarget | null {
    const separator = value.indexOf('\u0000');
    if (separator <= 0) return null;
    const channel = value.slice(0, separator);
    const channelId = Number(channel);
    const modelName = value.slice(separator + 1);
    if (!/^[1-9]\d*$/.test(channel) || !Number.isSafeInteger(channelId) || !modelName.trim()) return null;
    return { channel_id: channelId, model_name: modelName };
}
