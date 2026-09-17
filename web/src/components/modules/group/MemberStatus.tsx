import { useEffect, useState } from 'react';
import { ArrowDown, ArrowUp, CircleCheck, HeartCrack, HeartPulse, Pin } from 'lucide-react';
import { useTranslations } from 'use-intl';
import { Badge } from '@/components/ui/badge';
import { cn } from '@/lib/utils';
import { PROBE_RESULT_TTL_MS, probeScoreVote, type Group, type GroupProbeResult } from '@/api/group';

// MemberStatusProps 描述成员的冷却和亲和状态。
interface MemberStatusProps {
    group: Group; // group 提供当前路由和成员冷却时间戳。
    itemId?: number; // itemId 是待展示状态的成员 ID。
    now: number; // now 是所属列表共享的当前 Unix 毫秒时间。
    active?: boolean; // active 表示该成员当前正在使用。
    activeClassName?: string; // activeClassName 调整原有选中图标在不同列表中的间距。
}

// useRuntimeClock 为一个或多个分组提供共享倒计时当前时间。
export function useRuntimeClock(source?: Group | Group[]) {
    const [now, setNow] = useState(() => Date.now());
    const groups = source === undefined ? [] : Array.isArray(source) ? source : [source];
    let enabled = false;
    let lastDeadline = 0;
    for (const group of groups) {
        // 体检结论的有效期同样要走这个时钟：手动模式没有冷却倒计时，但结论到点也要自己消失。
        // 少了这一项，页面开着不动时徽标会一直挂着，直到某次重新拉取数据才消失。
        for (const probe of Object.values(group.runtime.probes ?? {})) {
            enabled = true;
            lastDeadline = Math.max(lastDeadline, probe.probed_at + PROBE_RESULT_TTL_MS);
        }
        if (group.mode !== 'failover') continue;
        enabled = true;
        lastDeadline = Math.max(lastDeadline, group.runtime.affinity_until);
        for (const cooldownUntil of Object.values(group.runtime.cooldowns)) {
            lastDeadline = Math.max(lastDeadline, cooldownUntil);
        }
    }

    useEffect(() => {
        if (!enabled) return;

        let timer = 0;
        const tick = () => {
            const next = Date.now();
            setNow(next);
            if (next >= lastDeadline) window.clearInterval(timer);
        };
        // 依赖变化后先异步校正一次，避免上一轮倒计时结束后残留的旧时间参与判断。
        const immediate = window.setTimeout(tick, 0);
        if (Date.now() < lastDeadline) timer = window.setInterval(tick, 1000);

        return () => {
            window.clearTimeout(immediate);
            window.clearInterval(timer);
        };
    }, [enabled, lastDeadline]);

    return now;
}

// freshProbe 取该成员仍在有效期内的体检结论：过期的结论一律当没有，要看就重新测活。
// 后端读出来时已经滤过一遍（刷新、换设备都一致），这里再滤一次是为了页面开着不动时到点自己消失。
export function freshProbe(group: Group, itemId: number | undefined, now: number): GroupProbeResult | undefined {
    if (itemId === undefined) return undefined;
    const probe = group.runtime.probes?.[itemId];
    if (!probe) return undefined;
    return now < probe.probed_at + PROBE_RESULT_TTL_MS ? probe : undefined;
}

// MemberStatus 展示成员的强制标记、体检结论、健康分偏移、冷却、亲和倒计时或当前使用圆点。
export function MemberStatus({ group, itemId, now, active = false, activeClassName }: MemberStatusProps) {
    const t = useTranslations('group.card');
    const isPinned = group.mode === 'failover' && itemId !== undefined && group.pinned_item_id === itemId;
    // 健康分只在故障转移模式累积; 手动模式没有进程内路由, 后端恒回空表。
    const baseScore = group.mode === 'failover' && itemId !== undefined ? (group.runtime.scores?.[itemId] ?? 0) : 0;
    // 体检结论两种模式都有：手动模式不参与选路，但"这条此刻通不通"仍是用户要看的结论。
    const probe = freshProbe(group, itemId, now);
    // 展示用的健康分 = 基础分 + 有效期内的测活加权（与后端选路同一条规则）。
    // 结论过期后 probe 变成 undefined，这一档自动消失，不会出现"徽标没了但 +3 还在"。
    const score = baseScore + (probe ? probeScoreVote(probe) : 0);

    if (group.mode === 'failover' && itemId !== undefined) {
        const cooldownUntil = group.runtime.cooldowns[itemId] ?? 0;
        const affinityUntil = group.runtime.current_item_id === itemId
            ? group.runtime.affinity_until
            : 0;
        if (now < cooldownUntil || now < affinityUntil) {
            const cooling = now < cooldownUntil;
            const deadline = cooling ? cooldownUntil : affinityUntil;
            const label = t(cooling ? 'cooling' : 'affinity', { seconds: Math.ceil((deadline - now) / 1000) });

            return (
                <span className="flex shrink-0 items-center gap-1">
                    {isPinned && <PinnedMark label={t('pinned')} />}
                    {probe && <ProbeMark probe={probe} />}
                    <RankMark score={score} />
                    <Badge
                        variant="outline"
                        className={cn(
                            'shrink-0 px-1.5 py-0 text-[10px] font-medium',
                            cooling
                                ? 'border-orange-500/30 bg-orange-500/10 text-orange-600 dark:text-orange-400'
                                : 'border-cyan-500/30 bg-cyan-500/10 text-cyan-600 dark:text-cyan-400'
                        )}
                    >
                        {label}
                    </Badge>
                </span>
            );
        }
    }

    if (!isPinned && !active && score === 0 && !probe) return null;

    return (
        <span className="flex shrink-0 items-center gap-1">
            {isPinned && <PinnedMark label={t('pinned')} />}
            {probe && <ProbeMark probe={probe} />}
            <RankMark score={score} />
            {active && (
                <span aria-hidden="true" className={cn('inline-flex shrink-0 text-primary', activeClassName)}>
                    <CircleCheck className="size-4" />
                </span>
            )}
        </span>
    );
}

// ProbeMark 标出该成员最近一次人工测活的结论: 通过显示耗时, 失败显示错误摘要。
// 与 RankMark 并列而非互斥: 测活通过会在结论有效期内把健康分抬到满档, 两者一起看才明白这次排名上升是体检带来的。
// 提示里带上有效期: 徽标到点会自己消失, 先说清"能看多久"才不至于让人以为它是丢了。
function ProbeMark({ probe }: { probe: GroupProbeResult }) {
    const t = useTranslations('group.card');
    const conclusion = probe.ok
        ? t('probeOk', { ms: probe.latency_ms })
        : t('probeFailed', { message: probe.message || t('probeUnknownError') });
    const title = `${conclusion} · ${t('probeValidFor', { minutes: PROBE_RESULT_TTL_MS / 60_000 })}`;

    return (
        <Badge
            variant="outline"
            className={cn(
                'shrink-0 gap-0.5 px-1 py-0 text-[10px] font-medium tabular-nums',
                probe.ok
                    ? 'border-emerald-500/30 bg-emerald-500/10 text-emerald-600 dark:text-emerald-400'
                    : 'border-rose-500/30 bg-rose-500/10 text-rose-600 dark:text-rose-400'
            )}
            title={title}
        >
            {probe.ok ? <HeartPulse className="size-2.5" /> : <HeartCrack className="size-2.5" />}
            {probe.ok ? `${probe.latency_ms}ms` : '✕'}
        </Badge>
    );
}

// RankMark 标出成员相对配置优先级的排名偏移: 正分因连续成功上浮, 负分因进入冷却下沉。
// 与冷却标记并列而非互斥: 下沉本就由冷却引起, 冷却倒计时与偏移一因一果, 同时可见才看得懂这次让位。
function RankMark({ score }: { score: number }) {
    const t = useTranslations('group.card');
    if (score === 0) return null;

    const up = score > 0;
    return (
        <Badge
            variant="outline"
            className={cn(
                'shrink-0 gap-0.5 px-1 py-0 text-[10px] font-medium tabular-nums',
                up
                    ? 'border-emerald-500/30 bg-emerald-500/10 text-emerald-600 dark:text-emerald-400'
                    : 'border-rose-500/30 bg-rose-500/10 text-rose-600 dark:text-rose-400'
            )}
            title={t(up ? 'rankUp' : 'rankDown', { steps: Math.abs(score) })}
        >
            {up ? <ArrowUp className="size-2.5" /> : <ArrowDown className="size-2.5" />}
            {Math.abs(score)}
        </Badge>
    );
}

// PinnedMark 标出被强制优先使用的成员。
// 与当前使用圆点并列而非互斥: 强制成员在冷却期间会让位给别的成员, 此时它仍带强制标记但不是当前成员。
function PinnedMark({ label }: { label: string }) {
    return (
        <Badge
            variant="outline"
            className="shrink-0 gap-0.5 px-1 py-0 text-[10px] font-medium border-primary/30 bg-primary/10 text-primary"
            title={label}
        >
            <Pin className="size-2.5" />
        </Badge>
    );
}
