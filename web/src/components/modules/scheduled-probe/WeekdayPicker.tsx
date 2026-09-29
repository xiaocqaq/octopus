import { useTranslations } from 'use-intl';
import { cn } from '@/lib/utils';
import { WEEKDAY_BITS, toggleWeekday } from './format';

// chipClass 是星期按钮的共用样式：选中态用主色，未选中用弱化底。
function chipClass(active: boolean) {
    return cn(
        'inline-flex h-8 items-center justify-center rounded-lg border px-2 text-xs font-medium transition-colors',
        active
            ? 'border-primary/30 bg-primary text-primary-foreground'
            : 'border-border bg-muted/20 text-foreground hover:bg-muted/30'
    );
}

// WeekdayPicker 是时间窗的星期选择：掩码为 0（一个都没选）就表示不限时段，故"不限时段"本身也是一个选项。
// 做成单选组而不是开关 + 复选：少一个状态就少一种"开关开着但一个都没选"的中间态。
export function WeekdayPicker({ weekdays, onChange }: {
    weekdays: number;
    onChange: (next: number) => void;
}) {
    const t = useTranslations('scheduledProbe');

    return (
        <div className="flex flex-wrap gap-1">
            {WEEKDAY_BITS.map(({ bit, key }) => (
                <button
                    key={key}
                    type="button"
                    onClick={() => onChange(toggleWeekday(weekdays, bit))}
                    aria-pressed={(weekdays & bit) !== 0}
                    className={cn(chipClass((weekdays & bit) !== 0), 'min-w-11')}
                >
                    {t(`weekday.${key}`)}
                </button>
            ))}
            <button
                type="button"
                onClick={() => onChange(0)}
                aria-pressed={weekdays === 0}
                className={chipClass(weekdays === 0)}
            >
                {t('form.windowAnytime')}
            </button>
        </div>
    );
}
