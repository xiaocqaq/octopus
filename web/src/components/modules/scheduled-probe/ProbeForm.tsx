import { useMemo, useState } from 'react';
import { useTranslations } from 'use-intl';
import { Plus, X } from 'lucide-react';
import { SCHEDULED_PROBE_DEFAULT_INTERVAL, SCHEDULED_PROBE_HOURS, type ScheduledProbeTarget } from '@/api/scheduled-probe';
import { useChannelDetail, useChannelStats } from '@/api/channel';
import { Button } from '@/components/ui/button';
import { Input } from '@/components/ui/input';
import { Field, FieldDescription, FieldGroup, FieldLabel } from '@/components/ui/field';
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from '@/components/ui/select';
import { formatHour } from './format';
import { WeekdayPicker } from './WeekdayPicker';
import { SearchMultiSelect, type SearchOption } from './SearchMultiSelect';

// ProbeFormValues 是这张表单的产出，与后端的提交体同形状（主键除外）。
export type ProbeFormValues = {
    name: string;
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

const DEFAULT_TARGET: ScheduledProbeTarget = { channel_id: 0, model_name: '' };

// ProbeForm 是创建与编辑共用的表单：一个自定义名字 + 一组被监控目标 + 间隔 + 时段。
//
// 创建与编辑共用一份：两者字段完全相同，分开写就要把"名字、目标增删、间隔校验、时段"这套逻辑维护两遍，
// 而它们必须保持一致 —— 编辑里少一个字段就会把用户刚配好的值清掉（更新是整体替换）。
export function ProbeForm({ initial, submitText, submittingText, isSubmitting, onCancel, onSubmit }: ProbeFormProps) {
    const t = useTranslations('scheduledProbe');
    const [name, setName] = useState(initial?.name ?? '');
    const [targets, setTargets] = useState<ScheduledProbeTarget[]>(initial?.targets ?? [DEFAULT_TARGET]);
    const [interval, setIntervalValue] = useState(String(initial?.interval_minutes ?? SCHEDULED_PROBE_DEFAULT_INTERVAL));
    const [weekdays, setWeekdays] = useState(initial?.weekdays ?? 0);
    const [startHour, setStartHour] = useState(initial?.start_hour ?? 9);
    const [endHour, setEndHour] = useState(initial?.end_hour ?? 18);

    const intervalMinutes = Number(interval);
    const intervalValid = Number.isInteger(intervalMinutes) && intervalMinutes >= 1 && intervalMinutes <= 1440;
    const filledTargets = targets.filter((target) => target.channel_id > 0 && target.model_name !== '');
    const canSubmit = name.trim() !== '' && filledTargets.length > 0 && intervalValid;

    const handleSubmit = (event: React.FormEvent<HTMLFormElement>) => {
        event.preventDefault();
        if (!canSubmit) return;
        onSubmit({
            name: name.trim(),
            targets: filledTargets,
            interval_minutes: intervalMinutes,
            // 编辑时保留原有的启停状态：开关在卡片上，表单不该顺手把它打开。
            enabled: initial?.enabled ?? true,
            weekdays,
            start_hour: startHour,
            end_hour: endHour,
        });
    };

    const updateTarget = (index: number, patch: Partial<ScheduledProbeTarget>) => {
        setTargets((current) => current.map((item, i) => (i === index ? { ...item, ...patch } : item)));
    };

    return (
        <form onSubmit={handleSubmit} className="flex min-h-0 flex-1 flex-col">
            {/* 表单可能很长（目标多时），故让字段区自己滚动、按钮固定在底部：
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

                    <Field>
                        <FieldLabel>{t('form.targets')}</FieldLabel>
                        <div className="flex flex-col gap-2">
                            {targets.map((target, index) => (
                                <TargetPicker
                                    key={index}
                                    target={target}
                                    // 只剩一个目标时不给删：一个目标都没有的任务测不出任何东西，
                                    // 与其让用户删空后再看到提交被拒，不如让最后一个留在那里。
                                    canRemove={targets.length > 1}
                                    onChange={(patch) => updateTarget(index, patch)}
                                    onRemove={() => setTargets((current) => current.filter((_, i) => i !== index))}
                                />
                            ))}
                        </div>
                        <Button
                            type="button"
                            variant="secondary"
                            onClick={() => setTargets((current) => [...current, DEFAULT_TARGET])}
                            className="h-9 w-full rounded-xl"
                        >
                            <Plus className="size-4" />
                            {t('form.addTarget')}
                        </Button>
                        <FieldDescription>{t('form.targetsHint')}</FieldDescription>
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

// TargetPicker 是一行目标选择：渠道下拉 + 该渠道的模型下拉。
// 模型下拉只列所选渠道已有的模型，与后端提交时的校验同一口径：后端拒绝"模型不属于该渠道"，
// 前端就别让这种组合有机会被选中。
function TargetPicker({
    target,
    canRemove,
    onChange,
    onRemove,
}: {
    target: ScheduledProbeTarget;
    canRemove: boolean;
    onChange: (patch: Partial<ScheduledProbeTarget>) => void;
    onRemove: () => void;
}) {
    const t = useTranslations('scheduledProbe');
    const { data: channels } = useChannelStats();
    const channelId = target.channel_id > 0 ? target.channel_id : undefined;
    const { data: detail } = useChannelDetail(channelId);

    const channelOptions = useMemo<SearchOption[]>(
        () => (channels ?? []).map((channel) => ({
            value: String(channel.channel_id),
            label: channel.channel_name,
            hint: t('form.modelCount', { count: channel.models.length }),
            keywords: channel.enabled ? '' : t('form.channelDisabled'),
        })),
        [channels, t]
    );

    const modelOptions = useMemo<SearchOption[]>(
        () => (detail?.models ?? []).map((modelName) => ({ value: modelName, label: modelName })),
        [detail]
    );

    return (
        <div className="flex items-center gap-2">
            <div className="min-w-0 flex-1">
                <SearchMultiSelect
                    options={channelOptions}
                    selected={target.channel_id > 0 ? [String(target.channel_id)] : []}
                    onChange={(values) => onChange({ channel_id: values.length > 0 ? Number(values[values.length - 1]) : 0, model_name: '' })}
                    placeholder={t('form.channelPlaceholder')}
                    searchPlaceholder={t('form.searchChannel')}
                    emptyText={t('form.channelEmpty')}
                    single
                />
            </div>
            <div className="min-w-0 flex-1">
                <SearchMultiSelect
                    options={modelOptions}
                    selected={target.model_name ? [target.model_name] : []}
                    onChange={(values) => onChange({ model_name: values.length > 0 ? values[values.length - 1]! : '' })}
                    placeholder={t('form.modelPlaceholder')}
                    searchPlaceholder={t('form.searchModel')}
                    emptyText={channelId === undefined ? t('form.modelPickChannelFirst') : t('form.modelEmpty')}
                    disabled={channelId === undefined || modelOptions.length === 0}
                    single
                />
            </div>
            <button
                type="button"
                onClick={onRemove}
                disabled={!canRemove}
                className="size-8 shrink-0 rounded-lg border border-border text-muted-foreground transition-colors hover:text-destructive disabled:opacity-40"
                aria-label={t('form.removeTarget')}
                title={t('form.removeTarget')}
            >
                <X className="mx-auto size-3.5" />
            </button>
        </div>
    );
}
