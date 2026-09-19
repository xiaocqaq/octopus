import { ArrowDown, ArrowUp, TrendingUp } from 'lucide-react';
import { useTranslations } from 'use-intl';
import { useChannelStatsByPeriod } from '@/api/channel';
import { PERIOD_DAYS, useHomeViewStore, type MetricKey, type RankSort } from './store';
import { cacheRate, useGroupStatsByPeriod, type StatsMetricsFormatted } from '@/api/stats';

// 榜单只展示这几项, 渠道可直接复用 api/channel 已算好的 formatted。
// 输入与命中词元只取其 raw: 缓存率要按累计 Tokens 重算, 直接平均各条目的百分比会算错。
type RankMetrics = Pick<
    StatsMetricsFormatted,
    'total_cost' | 'total_token' | 'request_count' | 'request_success' | 'request_failed' | 'input_token' | 'cached_token'
>;

// 榜单中的一个条目, 渠道和模型共用。
// 主标题是否被渠道名模糊开关糊掉由 blurName 决定: 渠道榜的标题本身就是渠道名, 模型榜的模型名不是。
interface RankItem {
    id: string;
    name: string; // 渠道榜为渠道名, 模型榜为模型名。
    blurName?: boolean; // 为真时模糊主标题, 仅渠道榜需要。
    formatted: RankMetrics;
}

// RANK_COLUMNS 是榜单三列的展示与排序顺序: 次数, 词元, 金额。
// 它同时是表头的排序入口 —— 表头顺序与数据列的渲染顺序必须一致。
const RANK_COLUMNS = ['count', 'tokens', 'cost'] as const;

// RankCard 渲染单个排行榜。三个指标(次数 / 词元 / 金额)一行全展示, 点表头按该列排序, 再点同一列翻转方向。
function RankCard({
    title,
    hint,
    items,
    sort,
    onSortChange,
    hideChannelName,
}: {
    title: string;
    hint?: string;
    items: RankItem[];
    sort: RankSort;
    onSortChange: (value: RankSort) => void;
    hideChannelName?: boolean;
}) {
    const t = useTranslations('home.rank');
    const tMetric = useTranslations('home.metric');
    const sortField = sort.key === 'cost' ? 'total_cost' : sort.key === 'count' ? 'request_count' : 'total_token';
    const ranked = [...items].sort((a, b) => {
        const diff = b.formatted[sortField].raw - a.formatted[sortField].raw;
        return sort.desc ? diff : -diff;
    });

    // 点同一列翻转方向, 点别的列换成那一列并回到由大到小。
    const toggleSort = (key: MetricKey) => {
        onSortChange(sort.key === key ? { key, desc: !sort.desc } : { key, desc: true });
    };

    // 三列指标与表头共用同一套栅格, 数据行的列才能与标签对齐。
    const grid = 'grid grid-cols-[auto_minmax(0,1fr)_auto_auto_auto] items-center gap-3';

    return (
        <div className="rounded-3xl bg-card text-card-foreground border-border border pt-2 px-4">
            <h3 className="font-semibold text-base">{title}</h3>
            {hint ? <p className="text-xs text-muted-foreground mt-1">{hint}</p> : null}

            {ranked.length === 0 ? (
                <div className="flex flex-col items-center justify-center py-8 text-muted-foreground">
                    <TrendingUp className="w-12 h-12 mb-3 opacity-30" />
                    <p className="text-sm">{t('noData')}</p>
                </div>
            ) : (
                <>
                    <div className={`${grid} pt-4 pb-1 text-sm text-muted-foreground`}>
                        <div />
                        <div />
                        {RANK_COLUMNS.map((key) => (
                            <button
                                key={key}
                                type="button"
                                onClick={() => toggleSort(key)}
                                className={`flex items-center justify-end gap-0.5 tabular-nums transition-colors hover:text-foreground ${sort.key === key ? 'font-medium text-foreground' : ''}`}
                            >
                                {tMetric(key)}
                                {sort.key === key && (sort.desc ? <ArrowDown className="size-3" /> : <ArrowUp className="size-3" />)}
                            </button>
                        ))}
                    </div>
                    <div className="space-y-3 max-h-[300px] overflow-y-auto">
                        {ranked.map((item, index) => {
                            // 模型榜把同一模型的多个渠道合并成一条, 分母与分子都要用合并后的累计词元, 不能平均各渠道的百分比。
                            const hitRate = cacheRate(item.formatted.input_token.raw, item.formatted.cached_token.raw);

                            return (
                                <div key={item.id} className={`${grid} py-3`}>
                                    <div className="flex items-center justify-center font-bold text-lg">{index + 1}</div>

                                    <div className="min-w-0">
                                        <p className={`font-medium text-sm truncate ${hideChannelName && item.blurName ? 'select-none blur-[3px]' : ''}`}>
                                            {item.name}
                                        </p>
                                        {/* 缓存率与排序维度无关, 始终展示; 失败数已在次数列并排展示, 不再重复给成功率。 */}
                                        <div className="flex flex-wrap items-center gap-x-2 text-xs text-muted-foreground mt-1">
                                            <span>
                                                {t('cacheRate')}: <span className="tabular-nums">{hitRate.toFixed(1)}%</span>
                                            </span>
                                        </div>
                                    </div>

                                    {/* 次数列: 成功/失败并排, 字号与词元, 金额两列一致, 三列视觉重量才对等。 */}
                                    <div className="flex items-center justify-end gap-1 text-base font-semibold tabular-nums">
                                        <span className="text-accent">
                                            {item.formatted.request_success.formatted.value}
                                            <span className="text-xs text-muted-foreground">
                                                {item.formatted.request_success.formatted.unit}
                                            </span>
                                        </span>
                                        <span className="text-muted-foreground/40 font-light">/</span>
                                        <span className="text-destructive">
                                            {item.formatted.request_failed.formatted.value}
                                            <span className="text-xs text-muted-foreground">
                                                {item.formatted.request_failed.formatted.unit}
                                            </span>
                                        </span>
                                    </div>

                                    {/* 词元列。 */}
                                    <div className="text-right font-semibold text-base tabular-nums">
                                        {item.formatted.total_token.formatted.value}
                                        <span className="text-xs text-muted-foreground">
                                            {item.formatted.total_token.formatted.unit}
                                        </span>
                                    </div>

                                    {/* 金额列, 排在最后: 次数 / 词元是请求侧的主指标, 金额只在需要对比成本时看。 */}
                                    <div className="text-right font-semibold text-base tabular-nums">
                                        {item.formatted.total_cost.formatted.value}
                                        <span className="text-xs text-muted-foreground">
                                            {item.formatted.total_cost.formatted.unit}
                                        </span>
                                    </div>
                                </div>
                            );
                        })}
                    </div>
                </>
            )}
        </div>
    );
}

// Rank 并列渠道榜和分组榜, 两榜各自独立排序, 统计范围跟随首页共用的时间周期。
export function Rank() {
    const period = useHomeViewStore((state) => state.chartPeriod);
    const { data: channelStats } = useChannelStatsByPeriod(PERIOD_DAYS[period]);
    const { data: groupStats } = useGroupStatsByPeriod(PERIOD_DAYS[period]);
    const t = useTranslations('home.rank');
    const isChannelNameHidden = useHomeViewStore((state) => state.isChannelNameHidden);
    const channelRankSort = useHomeViewStore((state) => state.channelRankSort);
    const setChannelRankSort = useHomeViewStore((state) => state.setChannelRankSort);
    const groupRankSort = useHomeViewStore((state) => state.groupRankSort);
    const setGroupRankSort = useHomeViewStore((state) => state.setGroupRankSort);

    const channelItems: RankItem[] = (channelStats ?? []).map((channel) => ({
        id: `channel-${channel.channel_id}`,
        name: channel.channel_name,
        blurName: true,
        formatted: channel.formatted,
    }));

    // 分组榜直接读按请求归属的分组统计: 同一渠道同一模型多把 Key 只记实际打到该组的请求,
    // 同一上游模型被多个分组引用时各记各的, 不再按成员复制渠道模型总量。
    const groupItems: RankItem[] = (groupStats ?? []).map((group) => ({
        id: `group-${group.group_id}`,
        name: group.group_name,
        formatted: group.formatted,
    }));

    return (
        <div className="grid grid-cols-1 @3xl/home:grid-cols-2 gap-4">
            <RankCard
                title={t('channel')}
                items={channelItems}
                sort={channelRankSort}
                onSortChange={setChannelRankSort}
                hideChannelName={isChannelNameHidden}
            />
            <RankCard
                title={t('group')}
                hint={t('sinceEnabled')}
                items={groupItems}
                sort={groupRankSort}
                onSortChange={setGroupRankSort}
            />
        </div>
    );
}
