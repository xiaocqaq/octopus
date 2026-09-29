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
