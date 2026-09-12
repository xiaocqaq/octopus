import { create } from 'zustand';
import { createJSONStorage, persist } from 'zustand/middleware';

// 首页各处共用的统计维度: 金额, 次数, 词元。
export type MetricKey = 'cost' | 'count' | 'tokens';

// 首页统计可选的时间周期: 数字为天数, all 为全时段。
// 该周期同时作用于趋势图, 顶部汇总卡片与下方的渠道, 模型榜单, 三处读同一个值。
export type ChartPeriod = '1' | '7' | '30' | 'all';

// PERIOD_DAYS 把周期换算为请求统计接口时的天数窗口, all 取 0 表示不限窗口。
export const PERIOD_DAYS: Record<ChartPeriod, number> = { '1': 1, '7': 7, '30': 30, all: 0 };

// PERIOD_SEQUENCE 是点击切换时的循环顺序。
export const PERIOD_SEQUENCE: ChartPeriod[] = ['1', '7', '30', 'all'];

// periodSinceDate 返回该周期的起始日期, 格式 20060102; all 返回空串表示不设下界。
// 按日期过滤而不是取最后 N 行: 每日统计只为有请求的那天留行, 零流量的日期没有行,
// 取行数会让 7 天跨到更早的日历日, 与榜单按日期范围聚合的后端口径不一致。
export function periodSinceDate(period: ChartPeriod): string {
    if (period === 'all') return '';
    const since = new Date();
    since.setDate(since.getDate() - (Number(period) - 1));
    const month = String(since.getMonth() + 1).padStart(2, '0');
    const day = String(since.getDate()).padStart(2, '0');
    return `${since.getFullYear()}${month}${day}`;
}

// 首页各区块的视图选项, 除渠道名模糊开关外均持久化到 localStorage。
interface HomeViewState {
    channelRankSortMode: MetricKey; // 渠道排行榜的排序维度。
    modelRankSortMode: MetricKey; // 模型排行榜的排序维度。
    chartMetricType: MetricKey; // 趋势图展示的指标。
    chartPeriod: ChartPeriod; // 趋势图的时间周期。
    isChannelNameHidden: boolean; // 是否模糊渠道名称, 分享图跟随此状态, 不持久化。
    setChannelRankSortMode: (value: MetricKey) => void;
    setModelRankSortMode: (value: MetricKey) => void;
    setChartMetricType: (value: MetricKey) => void;
    setChartPeriod: (value: ChartPeriod) => void;
    setChannelNameHidden: (value: boolean) => void;
}

export const useHomeViewStore = create<HomeViewState>()(
    persist(
        (set) => ({
            channelRankSortMode: 'cost',
            modelRankSortMode: 'cost',
            chartMetricType: 'cost',
            chartPeriod: '1',
            isChannelNameHidden: false,
            setChannelRankSortMode: (value) => set({ channelRankSortMode: value }),
            setModelRankSortMode: (value) => set({ modelRankSortMode: value }),
            setChartMetricType: (value) => set({ chartMetricType: value }),
            setChartPeriod: (value) => set({ chartPeriod: value }),
            setChannelNameHidden: (value) => set({ isChannelNameHidden: value }),
        }),
        {
            name: 'home-view-options-storage',
            storage: createJSONStorage(() => localStorage),
            partialize: (state) => ({
                channelRankSortMode: state.channelRankSortMode,
                modelRankSortMode: state.modelRankSortMode,
                chartMetricType: state.chartMetricType,
                chartPeriod: state.chartPeriod,
            }),
        }
    )
);
