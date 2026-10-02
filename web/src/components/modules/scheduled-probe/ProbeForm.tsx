import { useMemo, useState } from 'react';
import { useTranslations } from 'use-intl';
import { useChannelGrantList } from '@/api/channel';
import {
    SCHEDULED_PROBE_DEFAULT_END_HOUR,
    SCHEDULED_PROBE_DEFAULT_INTERVAL,
    SCHEDULED_PROBE_DEFAULT_START_HOUR,
    SCHEDULED_PROBE_DEFAULT_WEEKDAYS,
    SCHEDULED_PROBE_HOURS,
    type ScheduledProbeTarget,
} from '@/api/scheduled-probe';
import { Button } from '@/components/ui/button';
import { Input } from '@/components/ui/input';
import { Field, FieldDescription, FieldGroup, FieldLabel } from '@/components/ui/field';
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from '@/components/ui/select';
import { formatHour } from './format';
import { WeekdayPicker } from './WeekdayPicker';
import { SearchMultiSelect, type SearchOption } from './SearchMultiSelect';
import { buildTargetOptions, targetValueForKey, targetsOf } from './targets';

// ProbeFormValues 是这张表单的产出，与后端的提交体同形状（主键除外）。
export type ProbeFormValues = {
    name: string;
    // 后端的粒度只到 (渠道, 模型)；界面上的凭据那一段在提交前进了 excluded_keys。
    targets: ScheduledProbeTarget[];
    interval_minutes: number;
    enabled: boolean;
    weekdays: number;
    start_hour: number;
    end_hour: number;
};

type ProbeFormProps = {
    initial?: ProbeFormValues;
    submitText: string;
    submittingText: string;
    isSubmitting: boolean;
    onCancel: () => void;
    onSubmit: (values: ProbeFormValues) => void;
};

// ProbeForm 是创建与编辑共用的表单：一个自定义名字 + 渠道/凭据/模型合并多选 + 间隔 + 时段。
//
// 合并为一份多选下拉，而不是分成渠道、凭据、模型三个下拉：一个模型可能被多个渠道提供，
// 拆成三份的话"渠道 × 凭据 × 模型"是个笛卡尔积，会产出用户没看见的组合；
// 合成一份「渠道名 凭据名 模型名」的选项，勾的是确确实实会监控的那一条。
//
// 选项来源是授权列表(channelGrantListQueryOptions)而不是渠道统计：统计只给到"某渠道有哪些模型"，
// 拿不到凭据名，而这正是这个下拉要展示的第三段。
export function ProbeForm({ initial, submitText, submittingText, isSubmitting, onCancel, onSubmit }: ProbeFormProps) {
    const t = useTranslations('scheduledProbe');
    const { data: candidates } = useChannelGrantList();

    // 选项列表：候选授权摊成 (渠道, 凭据, 模型) 三段，按标签排序。
    const options = useMemo(() => buildTargetOptions(candidates ?? []), [candidates]);
    const targetOptions: SearchOption[] = useMemo(
        () => options.map((option) => ({ value: option.value, label: option.label })),
        [options],
    );

    // 初始回填（编辑）：目标上只记了 (渠道, 模型)，凭据那段要靠候选去补一条真实存在的 ——
    // 否则编辑框里会出现一个空凭据名，用户以为监控丢了凭据，一保存就把范围改小。
    //
    // 候选是异步到的：首次渲染时可能还是空的，只回填出"空凭据"的选项。
    // 故这里跟着候选一起算，并在候选到达后把空凭据的选中项替换成真实的凭据名。
    const initialValues = useMemo(() => {
        const values: string[] = [];
        for (const target of initial?.targets ?? []) {
            const value = targetValueForKey(target, candidates ?? []);
            if (value && !values.includes(value)) values.push(value);
        }
        return values;
    }, [initial, candidates]);

    const [selected, setSelected] = useState<string[]>(initialValues);
    const [name, setName] = useState(initial?.name ?? '');
    const [interval, setIntervalValue] = useState(String(initial?.interval_minutes ?? SCHEDULED_PROBE_DEFAULT_INTERVAL));
    const [weekdays, setWeekdays] = useState(initial?.weekdays ?? SCHEDULED_PROBE_DEFAULT_WEEKDAYS);
    const [startHour, setStartHour] = useState(initial?.start_hour ?? SCHEDULED_PROBE_DEFAULT_START_HOUR);
    const [endHour, setEndHour] = useState(initial?.end_hour ?? SCHEDULED_PROBE_DEFAULT_END_HOUR);

    const targets = targetsOf(selected, candidates ?? []);

    const intervalMinutes = Number(interval);
    const intervalValid = Number.isInteger(intervalMinutes) && intervalMinutes >= 1 && intervalMinutes <= 1440;
    const canSubmit = name.trim() !== '' && targets.length > 0 && intervalValid;

    const handleSubmit = (event: React.FormEvent<HTMLFormElement>) => {
        event.preventDefault();
        if (!canSubmit) return;
        onSubmit({
            name: name.trim(),
            targets,
            interval_minutes: intervalMinutes,
            // 编辑时保留原有的启停状态：开关在卡片上，表单不该顺手把它打开。
            enabled: initial?.enabled ?? true,
            weekdays,
            start_hour: startHour,
            end_hour: endHour,
        });
    };

    return (
        <form onSubmit={handleSubmit} className="flex min-h-0 flex-1 flex-col">
            {/* 表单可能很长，故让字段区自己滚、按钮固定在底部：
                否则目标一多，提交按钮就被挤出可视区，用户得先滚到底才能点。 */}
            <div className="min-h-0 flex-1 overflow-y-auto overscroll-contain pr-1">
                <FieldGroup className="gap-4">
                    <Field>
                        <FieldLabel htmlFor="scheduled-probe-name">{t('form.name')}</FieldLabel>
                        <Input
                            id="scheduled-probe-name"
                            value={name}
                            onChange={(event) => setName(event.target.value)}
                            placeholder={t('form.namePlaceholder')}
                            className="rounded-xl"
                        />
                        <FieldDescription>{t('form.nameHint')}</FieldDescription>
                    </Field>

                    {/* 合成一个多选：渠道、凭据与模型不再是三个独立下拉, 而是「渠道名 凭据名 模型名」的组合选项。
                        选它就是在说"这条通道的这个模型, 用这把钥匙测"。 */}
                    <Field>
                        <FieldLabel htmlFor="scheduled-probe-targets">{t('form.targets')}</FieldLabel>
                        <SearchMultiSelect
                            id="scheduled-probe-targets"
                            options={targetOptions}
                            selected={selected}
                            onChange={setSelected}
                            placeholder={t('form.targetPlaceholder')}
                            searchPlaceholder={t('form.searchTarget')}
                            emptyText={t('form.targetEmpty')}
                            selectAllLabel={t('form.selectAll')}
                            clearLabel={t('form.clear')}
                            confirmLabel={t('form.confirm')}
                        />
                        <FieldDescription>{t('form.targetHint')}</FieldDescription>
                    </Field>

                    <Field>
                        <FieldLabel htmlFor="scheduled-probe-interval">{t('form.interval')}</FieldLabel>
                        <Input
                            id="scheduled-probe-interval"
                            type="number"
                            min={1}
                            max={1440}
                            value={interval}
                            onChange={(event) => setIntervalValue(event.target.value)}
                            className="rounded-xl"
                        />
                        <FieldDescription>{t('form.intervalHint')}</FieldDescription>
                    </Field>

                    <Field>
                        <FieldLabel>{t('form.window')}</FieldLabel>
                        <WeekdayPicker weekdays={weekdays} onChange={setWeekdays} />
                        <FieldDescription>{t('form.windowHint')}</FieldDescription>
                    </Field>

                    {weekdays !== 0 && (
                        <div className="grid grid-cols-2 gap-4">
                            <Field>
                                <FieldLabel htmlFor="scheduled-probe-start">{t('form.startHour')}</FieldLabel>
                                <Select value={String(startHour)} onValueChange={(value) => setStartHour(Number(value))}>
                                    <SelectTrigger id="scheduled-probe-start" className="w-full rounded-xl">
                                        <SelectValue />
                                    </SelectTrigger>
                                    <SelectContent>
                                        {SCHEDULED_PROBE_HOURS.map((hour) => (
                                            <SelectItem key={hour} value={String(hour)}>{formatHour(hour)}</SelectItem>
                                        ))}
                                    </SelectContent>
                                </Select>
                            </Field>
                            <Field>
                                <FieldLabel htmlFor="scheduled-probe-end">{t('form.endHour')}</FieldLabel>
                                <Select value={String(endHour)} onValueChange={(value) => setEndHour(Number(value))}>
                                    <SelectTrigger id="scheduled-probe-end" className="w-full rounded-xl">
                                        <SelectValue />
                                    </SelectTrigger>
                                    <SelectContent>
                                        {SCHEDULED_PROBE_HOURS.map((hour) => (
                                            <SelectItem key={hour} value={String(hour)}>{formatHour(hour)}</SelectItem>
                                        ))}
                                    </SelectContent>
                                </Select>
                            </Field>
                            <p className="col-span-2 text-xs text-muted-foreground">
                                {startHour > endHour ? t('form.windowCrossMidnight') : t('form.windowSameDay')}
                            </p>
                        </div>
                    )}
                </FieldGroup>
            </div>

            <div className="mt-4 flex shrink-0 gap-2">
                <Button type="button" variant="secondary" onClick={onCancel} className="h-11 flex-1 rounded-xl">
                    {t('cancel')}
                </Button>
                <Button type="submit" disabled={isSubmitting || !canSubmit} className="h-11 flex-1 rounded-xl">
                    {isSubmitting ? submittingText : submitText}
                </Button>
            </div>
        </form>
    );
}
