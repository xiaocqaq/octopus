import {
    Activity,
    MessageSquare,
    Clock,
    ArrowDownToLine,
    ChartColumnBig,
    Bot,
    ArrowUpFromLine,
    Rewind,
    DollarSign,
    FastForward
} from 'lucide-react';
import { useMemo } from 'react';
import { useTranslations } from 'use-intl';
import { formatStatsMetrics, useStatsDaily, useStatsHourly, useStatsTotal, type StatsMetrics } from '@/api/stats';
import { AnimatedNumber } from '@/components/common/AnimatedNumber';
import { periodSinceDate, useHomeViewStore } from './store';

// EMPTY_METRICS 用于周期内暂无数据时的求和初值。
const EMPTY_METRICS: StatsMetrics = {
    input_token: 0,
    output_token: 0,
    input_cost: 0,
    output_cost: 0,
    wait_time: 0,
    request_success: 0,
    request_failed: 0,
};

// Total 展示选定周期内的请求, 总量, 输入和输出四组指标卡片。
// 全部周期直接读总计接口, 其余周期由每日或每小时统计求和: 两者的字段与总计同名, 求和后按同一口径格式化。
export function Total() {
    const { data: totalStats } = useStatsTotal();
    const { data: statsDaily } = useStatsDaily();
    const { data: statsHourly } = useStatsHourly();
    const t = useTranslations('home.total');
    const period = useHomeViewStore((state) => state.chartPeriod);

    const stats = useMemo(() => {
        if (period === 'all') return totalStats;
        // 今天取小时粒度与趋势图一致; 其余取最近 N 天。
        const since = periodSinceDate(period);
        const source = period === '1'
            ? (statsHourly ?? [])
            : (statsDaily ?? []).filter((stat) => stat.date >= since);
        // 两个 Hook 给出的是已格式化的取值, 原始数字在 raw 上; 求和后再统一格式化一次。
        const summed = source.reduce<StatsMetrics>((acc, item) => ({
            input_token: acc.input_token + item.input_token.raw,
            output_token: acc.output_token + item.output_token.raw,
            input_cost: acc.input_cost + item.input_cost.raw,
            output_cost: acc.output_cost + item.output_cost.raw,
            wait_time: acc.wait_time + item.wait_time.raw,
            request_success: acc.request_success + item.request_success.raw,
            request_failed: acc.request_failed + item.request_failed.raw,
        }), EMPTY_METRICS);
        return formatStatsMetrics(summed);
    }, [period, totalStats, statsDaily, statsHourly]);

    const cards = [
        {
            title: t('requestStats'),
            headerIcon: Activity,
            items: [
                { label: t('requestCount'), metric: stats?.request_count, icon: MessageSquare, bgColor: 'bg-primary/10' },
                { label: t('timeConsumed'), metric: stats?.wait_time, icon: Clock, bgColor: 'bg-accent/10' },
            ],
        },
        {
            title: t('totalStats'),
            headerIcon: ChartColumnBig,
            items: [
                { label: t('totalToken'), metric: stats?.total_token, icon: Bot, bgColor: 'bg-chart-1/10' },
                { label: t('totalCost'), metric: stats?.total_cost, icon: DollarSign, bgColor: 'bg-chart-2/10' },
            ],
        },
        {
            title: t('inputStats'),
            headerIcon: ArrowDownToLine,
            items: [
                { label: t('inputTokens'), metric: stats?.input_token, icon: Rewind, bgColor: 'bg-chart-3/10' },
                { label: t('inputCost'), metric: stats?.input_cost, icon: DollarSign, bgColor: 'bg-chart-3/10' },
            ],
        },
        {
            title: t('outputStats'),
            headerIcon: ArrowUpFromLine,
            items: [
                { label: t('outputTokens'), metric: stats?.output_token, icon: FastForward, bgColor: 'bg-chart-4/10' },
                { label: t('outputCost'), metric: stats?.output_cost, icon: DollarSign, bgColor: 'bg-chart-4/10' },
            ],
        },
    ];

    return (
        <div className="grid grid-cols-1 @xl/home:grid-cols-2 @3xl/home:grid-cols-4 gap-4">
            {cards.map((card) => (
                <section
                    key={card.title}
                    className="rounded-3xl bg-card border-border border p-5 text-card-foreground flex flex-row items-center gap-4"
                >
                    <div className="flex flex-col items-center justify-center gap-3 border-r border-border/50 pr-4 py-1 self-stretch">
                        <card.headerIcon className="w-4 h-4" />
                        <h3 className="font-medium text-sm [writing-mode:vertical-lr]">{card.title}</h3>
                    </div>

                    <div className="flex flex-col gap-4 flex-1 min-w-0">
                        {card.items.map((item) => (
                            <div key={item.label} className="flex items-center gap-3">
                                <div className={`w-10 h-10 rounded-xl flex items-center justify-center shrink-0 text-primary ${item.bgColor}`}>
                                    <item.icon className="w-5 h-5" />
                                </div>
                                <div className="flex flex-col min-w-0">
                                    <span className="text-xs text-muted-foreground">{item.label}</span>
                                    <div className="flex items-baseline gap-1">
                                        <span className="text-xl">
                                            <AnimatedNumber value={item.metric?.formatted.value} />
                                        </span>
                                        {/* 计数在千位以下 unit 为空串, 空 span 仍是 flex 项, 会多出 gap-1 的间距。 */}
                                        {item.metric?.formatted.unit && (
                                            <span className="text-sm text-muted-foreground">{item.metric.formatted.unit}</span>
                                        )}
                                    </div>
                                </div>
                            </div>
                        ))}
                    </div>
                </section>
            ))}
        </div>
    );
}
