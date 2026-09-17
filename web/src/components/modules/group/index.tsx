import { useMemo } from 'react';
import { ArrowUpAZ } from 'lucide-react';
import { useTranslations } from 'use-intl';
import { GroupCard } from './Card';
import { CreateDialogContent } from './Create';
import { useRuntimeClock } from './MemberStatus';
import { useGroupList, PROBE_RESULT_TTL_MS, scoreDeadline } from '@/api/group';
import { PageActions, usePageActionsStore } from '@/components/common/PageActions';
import { VirtualizedGrid } from '@/components/common/VirtualizedGrid';

// GroupActions 向稳定顶栏提供分组页面的搜索、视图选项和创建入口。
export function GroupActions() {
    const t = useTranslations('toolbar');
    const searchTerm = usePageActionsStore((state) => state.searchTerms.group || '');
    const sortOrder = usePageActionsStore((state) => state.sortOrders.group === 'desc' ? 'desc' : 'asc');
    const filter = usePageActionsStore((state) => state.groupFilter);
    const setSearchTerm = usePageActionsStore((state) => state.setSearchTerm);
    const setSort = usePageActionsStore((state) => state.setSort);
    const setFilter = usePageActionsStore((state) => state.setGroupFilter);

    return (
        <PageActions
            searchTerm={searchTerm}
            onSearchTermChange={(value) => setSearchTerm('group', value)}
            sortOptions={[
                { value: 'asc', label: t('popover.nameAsc'), icon: ArrowUpAZ },
                { value: 'desc', label: t('popover.nameDesc'), icon: ArrowUpAZ },
            ]}
            sortValue={sortOrder}
            onSortChange={(value) => {
                if (value === 'asc' || value === 'desc') setSort('group', value);
            }}
            filterOptions={[
                { value: 'all', label: t('popover.filter.group.all') },
                { value: 'with-members', label: t('popover.filter.group.withMembers') },
                { value: 'empty', label: t('popover.filter.group.empty') },
            ]}
            filterValue={filter}
            onFilterChange={(value) => {
                if (value === 'all' || value === 'with-members' || value === 'empty') setFilter(value);
            }}
        >
            <CreateDialogContent />
        </PageActions>
    );
}

// Group 渲染分组列表正文。
export function Group() {
    const { data: groups } = useGroupList(true, true);
    const runtimeNow = useRuntimeClock(groups);
    const searchTerm = usePageActionsStore((state) => state.searchTerms.group || '');
    const sortOrder = usePageActionsStore((state) => state.sortOrders.group === 'desc' ? 'desc' : 'asc');
    const filter = usePageActionsStore((state) => state.groupFilter);

    const sortedGroups = useMemo(() => {
        if (!groups) return [];
        return [...groups].sort((a, b) =>
            sortOrder === 'asc' ? a.name.localeCompare(b.name) : b.name.localeCompare(a.name)
        );
    }, [groups, sortOrder]);

    const visibleGroups = useMemo(() => {
        const term = searchTerm.toLowerCase().trim();
        const byName = !term ? sortedGroups : sortedGroups.filter((g) => g.name.toLowerCase().includes(term));

        if (filter === 'with-members') return byName.filter((g) => (g.items?.length || 0) > 0);
        if (filter === 'empty') return byName.filter((g) => (g.items?.length || 0) === 0);

        return byName;
    }, [sortedGroups, searchTerm, filter]);

    return (
        <VirtualizedGrid
            items={visibleGroups}
            columns={{ default: 1, md: 2, lg: 3 }}
            // 成员区默认收起, 卡片只有标题行的高度; 展开态挂在浮层上不参与布局, 故估值按收起态给。
            // 实际高度由 VirtualizedGrid 自行测量, 估值只影响首帧滚动条长度。
            estimateItemHeight={88}
            getItemKey={(group) => group.id}
            renderItem={(group) => {
                // now 是共享时钟的真实时间; 下面的截止时间只在时钟停摆时兜底:
                // 时钟按 1 秒步进, 停下来时可能停在截止时间之前一点点, 冻在截止值上能让倒计时标记干净消失。
                // 体检结论的有效期也要算进来 —— 分组里只有体检结论而没有冷却时, 若 deadline 仍是 0,
                // 传 0 会让"结论是否已过期"的比较恒为真, 徽标就永远不消失(实测踩过这个坑)。
                let deadline = 0;
                for (const probe of Object.values(group.runtime.probes ?? {})) {
                    deadline = Math.max(deadline, probe.probed_at + PROBE_RESULT_TTL_MS);
                }
                deadline = Math.max(deadline, group.runtime.affinity_until);
                for (const cooldownUntil of Object.values(group.runtime.cooldowns)) {
                    deadline = Math.max(deadline, cooldownUntil);
                }
                // 健康分也要算进来：它自己会按 score_at 往 0 走，走到 0 可能比亲和/冷却晚得多
                // （最重 -3 要 15 分钟）。漏掉这一项，时钟会停在亲和到期那一刻，
                // 页面上的排名标记就不再往下降档 —— 实测就是这个症状。
                for (const [itemID, score] of Object.entries(group.runtime.scores ?? {})) {
                    deadline = Math.max(deadline, scoreDeadline(score, group.runtime.score_at?.[Number(itemID)] ?? 0));
                }
                return <GroupCard group={group} now={deadline === 0 ? runtimeNow : Math.min(runtimeNow, deadline)} />;
            }}
        />
    );
}
