import { useMemo } from 'react';
import { TrendingUp } from 'lucide-react';
import { useTranslations } from 'use-intl';
import { useChannelStatsByPeriod } from '@/api/channel';
import { formatCount, formatMoney } from '@/lib/utils';
import { PERIOD_DAYS, useHomeViewStore, type MetricKey } from './store';
import { MetricTabs } from './metric-tabs';
import type { StatsMetricsFormatted } from '@/api/stats';

// 榜单只展示这几项, 渠道可直接复用 api/channel 已算好的 formatted。
type RankMetrics = Pick<
    StatsMetricsFormatted,
    'total_cost' | 'total_token' | 'request_count' | 'request_success' | 'request_failed'
>;

// sumRankMetrics 把两份榜单统计按 raw 相加并重新格式化。
// 不能直接相加 formatted: 那是带单位的展示串, 5K + 5K 得重新进位成 10K 才对。
function sumRankMetrics(a: RankMetrics, b: RankMetrics): RankMetrics {
    return {
        total_cost: formatMoney(a.total_cost.raw + b.total_cost.raw),
        total_token: formatCount(a.total_token.raw + b.total_token.raw),
        request_count: formatCount(a.request_count.raw + b.request_count.raw),
        request_success: formatCount(a.request_success.raw + b.request_success.raw),
        request_failed: formatCount(a.request_failed.raw + b.request_failed.raw),
    };
}

// 榜单中的一个条目, 渠道和模型共用。
// 主标题是否被渠道名模糊开关糊掉由 blurName 决定: 渠道榜的标题本身就是渠道名, 模型榜的模型名不是。
interface RankItem {
    id: string;
    name: string; // 渠道榜为渠道名, 模型榜为模型名。
    blurName?: boolean; // 为真时模糊主标题, 仅渠道榜需要。
    formatted: RankMetrics;
}

// RankCard 渲染单个排行榜: 标题, 维度切换和榜单列表。
function RankCard({
    title,
    items,
    sortMode,
    onSortModeChange,
    hideChannelName,
}: {
    title: string;
    items: RankItem[];
    sortMode: MetricKey;
    onSortModeChange: (value: MetricKey) => void;
    hideChannelName?: boolean;
}) {
    const t = useTranslations('home.rank');
    const sortField = sortMode === 'cost' ? 'total_cost' : sortMode === 'count' ? 'request_count' : 'total_token';
    const ranked = [...items].sort((a, b) => b.formatted[sortField].raw - a.formatted[sortField].raw);

    return (
        <div className="rounded-3xl bg-card text-card-foreground border-border border pt-2 px-4">
            <div className="flex items-center justify-between">
                <h3 className="font-semibold text-base">{title}</h3>
                <MetricTabs value={sortMode} onChange={onSortModeChange} />
            </div>

            {ranked.length === 0 ? (
                <div className="flex flex-col items-center justify-center py-8 text-muted-foreground">
                    <TrendingUp className="w-12 h-12 mb-3 opacity-30" />
                    <p className="text-sm">{t('noData')}</p>
                </div>
            ) : (
                <div className="space-y-3 max-h-[300px] overflow-y-auto">
                    {ranked.map((item, index) => {
                        const successCount = item.formatted.request_success.raw;
                        const totalCount = successCount + item.formatted.request_failed.raw;

                        return (
                            <div key={item.id} className="grid grid-cols-[auto_minmax(0,1fr)_auto] items-center gap-3 py-3">
                                <div className="flex items-center justify-center font-bold text-lg">{index + 1}</div>

                                <div className="min-w-0">
                                    <p className={`font-medium text-sm truncate ${hideChannelName && item.blurName ? 'select-none blur-[3px]' : ''}`}>
                                        {item.name}
                                    </p>
                                    {sortMode === 'count' && (
                                        <div className="flex items-center gap-1 text-xs text-muted-foreground mt-1">
                                            <span>{t('successRate')}:</span>
                                            <span>{(totalCount > 0 ? (successCount / totalCount) * 100 : 0).toFixed(1)}%</span>
                                        </div>
                                    )}
                                </div>

                                <div className="flex items-center gap-1 text-right">
                                    {sortMode === 'count' ? (
                                        <div className="flex items-center gap-1 text-sm font-medium tabular-nums">
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
                                    ) : (
                                        <span className="font-semibold text-base">
                                            {item.formatted[sortField].formatted.value}
                                            <span className="text-xs text-muted-foreground">
                                                {item.formatted[sortField].formatted.unit}
                                            </span>
                                        </span>
                                    )}
                                </div>
                            </div>
                        );
                    })}
                </div>
            )}
        </div>
    );
}

// Rank 并列渠道榜和模型榜, 两榜各自独立排序, 统计范围跟随首页共用的时间周期。
export function Rank() {
    const period = useHomeViewStore((state) => state.chartPeriod);
    const { data: channelStats } = useChannelStatsByPeriod(PERIOD_DAYS[period]);
    const t = useTranslations('home.rank');
    const channelSortMode = useHomeViewStore((state) => state.channelRankSortMode);
    const setChannelSortMode = useHomeViewStore((state) => state.setChannelRankSortMode);
    const modelSortMode = useHomeViewStore((state) => state.modelRankSortMode);
    const setModelSortMode = useHomeViewStore((state) => state.setModelRankSortMode);
    const isChannelNameHidden = useHomeViewStore((state) => state.isChannelNameHidden);

    const channelItems: RankItem[] = (channelStats ?? []).map((channel) => ({
        id: `channel-${channel.channel_id}`,
        name: channel.channel_name,
        blurName: true,
        formatted: channel.formatted,
    }));

    // 模型榜按模型名合并, 不区分供应商: 同一模型在多个渠道上的调用算作一条。
    // 名称大小写不敏感, 展示用首次出现的原样。
    const modelItems: RankItem[] = useMemo(() => {
        const merged = new Map<string, { name: string; metrics: RankMetrics }>();
        for (const channel of channelStats ?? []) {
            for (const channelModel of channel.models) {
                const key = channelModel.model_name.toLowerCase();
                const existing = merged.get(key);
                if (!existing) {
                    merged.set(key, { name: channelModel.model_name, metrics: { ...channelModel.formatted } });
                    continue;
                }
                existing.metrics = sumRankMetrics(existing.metrics, channelModel.formatted);
            }
        }
        return [...merged.entries()].map(([key, item]) => ({
            id: `model-${key}`,
            name: item.name,
            formatted: item.metrics,
        }));
    }, [channelStats]);

    return (
        <div className="grid grid-cols-1 @3xl/home:grid-cols-2 gap-4">
            <RankCard
                title={t('channel')}
                items={channelItems}
                sortMode={channelSortMode}
                onSortModeChange={setChannelSortMode}
                hideChannelName={isChannelNameHidden}
            />
            <RankCard
                title={t('model')}
                items={modelItems}
                sortMode={modelSortMode}
                onSortModeChange={setModelSortMode}
            />
        </div>
    );
}
