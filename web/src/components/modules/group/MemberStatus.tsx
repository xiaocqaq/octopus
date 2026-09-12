import { useEffect, useState } from 'react';
import { CircleCheck, Pin } from 'lucide-react';
import { useTranslations } from 'use-intl';
import { Badge } from '@/components/ui/badge';
import { cn } from '@/lib/utils';
import type { Group } from '@/api/group';

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

// MemberStatus 展示成员的强制标记、冷却、亲和倒计时或当前使用圆点。
export function MemberStatus({ group, itemId, now, active = false, activeClassName }: MemberStatusProps) {
    const t = useTranslations('group.card');
    const isPinned = group.mode === 'failover' && itemId !== undefined && group.pinned_item_id === itemId;

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

    if (!isPinned && !active) return null;

    return (
        <span className="flex shrink-0 items-center gap-1">
            {isPinned && <PinnedMark label={t('pinned')} />}
            {active && (
                <span aria-hidden="true" className={cn('inline-flex shrink-0 text-primary', activeClassName)}>
                    <CircleCheck className="size-4" />
                </span>
            )}
        </span>
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
