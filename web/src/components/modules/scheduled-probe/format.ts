import {
    WeekdayAll,
    WeekdayMonday,
    WeekdayTuesday,
    WeekdayWednesday,
    WeekdayThursday,
    WeekdayFriday,
    WeekdaySaturday,
    WeekdaySunday,
    type ScheduledProbe,
} from '@/api/scheduled-probe';

// WEEKDAY_BITS 按界面顺序（周一在前）列出星期掩码位，与后端 model.Weekday* 的排位一致。
export const WEEKDAY_BITS = [
    { bit: WeekdayMonday, key: 'mon' },
    { bit: WeekdayTuesday, key: 'tue' },
    { bit: WeekdayWednesday, key: 'wed' },
    { bit: WeekdayThursday, key: 'thu' },
    { bit: WeekdayFriday, key: 'fri' },
    { bit: WeekdaySaturday, key: 'sat' },
    { bit: WeekdaySunday, key: 'sun' },
] as const;

// weekdayKeys 取掩码里选中的星期，供界面按顺序显示。
export function weekdayKeys(weekdays: number) {
    return WEEKDAY_BITS.filter(({ bit }) => (weekdays & bit) !== 0).map(({ key }) => key);
}

// toggleWeekday 翻转掩码中的某一位，返回新的掩码。
export function toggleWeekday(weekdays: number, bit: number) {
    return weekdays ^ bit;
}

// formatHour 把整点补成两位，让 09:00 与 18:00 在界面上对齐。
export function formatHour(hour: number) {
    return `${String(hour).padStart(2, '0')}:00`;
}

// describeWindow 给出时间窗的一句话描述，掩码为 0 时明确说明不限时段。
// 跨午夜的窗口按 22:00–次日 05:00 表达：直接写 22:00–05:00 会被读成"倒着的区间"。
export function describeWindow(probe: Pick<ScheduledProbe, 'weekdays' | 'start_hour' | 'end_hour'>, t: (key: string) => string) {
    const days = weekdayKeys(probe.weekdays);
    if (days.length === 0) return t('window.anytime');

    const dayText = probe.weekdays === WeekdayAll
        ? t('window.everyday')
        : days.map((key) => t(`weekday.${key}`)).join(' ');

    if (probe.start_hour === probe.end_hour) return `${dayText} · ${t('window.wholeDay')}`;
    if (probe.start_hour > probe.end_hour) {
        return `${dayText} ${formatHour(probe.start_hour)}–${t('window.nextDay')} ${formatHour(probe.end_hour)}`;
    }
    return `${dayText} ${formatHour(probe.start_hour)}–${formatHour(probe.end_hour)}`;
}

// describeProbedAt 把结论产生时间说成"多久之前"。
// 说相对时间而不是绝对时刻：卡片要回答的是"这条结论还新不新"，结论本身不设有效期、
// 只会被下一次测活覆盖，一个绝对时刻（16:42:07）还得让人自己跟现在做减法，相对时间才是这个场景真正要读的信息。
//
// 单位词（分钟/小时）取自语言包而不是拼在这里：这个函数三个语言共用一份实现，
// 把中文单位写死会让英文界面显示成 "5 分钟 ago"。
//
// now 由调用方传入而不是在这里读 Date.now(): 这是本函数唯一的非纯输入, 藏在函数体里 React 就看不见它。
// React Compiler 会把这次调用的结果按入参(probed_at 与 t)缓存下来, 而"现在几点"不在其中,
// 于是只要结论本身没变, 这段文字就永远停在第一次算出的"刚刚"——实测现象正是页面开着不动一直显示
// "刚刚", 手动刷新才变成"6 分钟前"。把时刻提到入参里, 它才进得了缓存的比较条件, 时间推进才会走到界面上。
export function describeProbedAt(probedAt: number, now: number, t: (key: string, values?: Record<string, string | number>) => string) {
    const seconds = Math.max(0, Math.floor((now - probedAt) / 1000));
    if (seconds < 60) return t('justNow');
    if (seconds < 3600) return t('probedAgo', { time: t('minutesShort', { count: Math.floor(seconds / 60) }) });
    return t('probedAgo', { time: t('hoursShort', { count: Math.floor(seconds / 3600) }) });
}
