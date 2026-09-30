import { useMemo, useState } from 'react';
import { useTranslations } from 'use-intl';
import { useChannelStats } from '@/api/channel';
import {
    SCHEDULED_PROBE_DEFAULT_INTERVAL,
    SCHEDULED_PROBE_HOURS,
    type ScheduledProbeTarget,
} from '@/api/scheduled-probe';
import { Badge } from '@/components/ui/badge';
import { Button } from '@/components/ui/button';
import { Input } from '@/components/ui/input';
import { Field, FieldDescription, FieldGroup, FieldLabel } from '@/components/ui/field';
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from '@/components/ui/select';
import { formatHour } from './format';
import { WeekdayPicker } from './WeekdayPicker';
import { SearchMultiSelect, type SearchOption } from './SearchMultiSelect';
import { targetFromKey, targetKey } from './targets';

// ProbeFormValues 是这张表单的产出，与后端的提交体同形状（主键除外）。
export type ProbeFormValues = {
    name: string;
    // 合并为一条「渠道/模型」选项列表，每条就是一个 (channel_id, model_name)。
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

// ProbeForm 是创建与编辑共用的表单：一个自定义名字 + 渠道/模型合并多选 + 间隔 + 时段。
//
// 合并为一份多选下拉，而非分开的渠道+模型：一个模型可能被多个渠道提供，
// 拆开的话"渠道 × 模型"是个笛卡尔积，会产出用户没看见的组合；
// 合成一份「渠道名/模型名」的选项, 勾的是确确实实会监控的那一条。
export function ProbeForm({ initial, submitText, submittingText, isSubmitting, onCancel, onSubmit }: ProbeFormProps) {
    const t = useTranslations('scheduledProbe');
    const { data: channels } = useChannelStats();

    // 选项列表来源：用 useChannelStats 的 models 来合成, 不用单独查渠道详情 ——
    // 列表里的 models 本身就是当前生效的模型集合。
    const targetOptions: SearchOption[] = useMemo(() => {
        const opts: SearchOption[] = [];
        channels?.forEach((channel) => {
            channel.models.forEach((channelModel) => {
                const modelName = channelModel.model_name;
                opts.push({
                    value: `${channel.channel_id}\u0000${modelName}`,
                    label: `${channel.channel_name}/${modelName}`,
                    hint: channel.enabled ? undefined : t('form.channelDisabled'),
                });
            });
        });
        return opts.sort((a, b) => a.label.localeCompare(b.label));
    }, [channels, t]);

    // 初始回填：把已有目标还原为选项 value 列表。
    const initialValues = useMemo(
        () => (initial?.targets ?? []).map((target) => `${target.channel_id}\u0000${target.model_name}`),
        [initial],
    );

    const [selected, setSelected] = useState<string[]>(initialValues);
    const [name, setName] = useState(initial?.name ?? '');
    const [interval, setIntervalValue] = useState(String(initial?.interval_minutes ?? SCHEDULED_PROBE_DEFAULT_INTERVAL));
    const [weekdays, setWeekdays] = useState(initial?.weekdays ?? 0);
    const [startHour, setStartHour] = useState(initial?.start_hour ?? 9);
    const [endHour, setEndHour] = useState(initial?.end_hour ?? 18);

    const targets = selected.flatMap((value) => {
        const target = targetFromKey(value);
        return target ? [target] : [];
    });

    const intervalMinutes = Number(interval);
    const intervalValid = Number.isInteger(intervalMinutes) && intervalMinutes >= 1 && intervalMinutes <= 1440;
    const canSubmit = name.trim() !== '' && selected.length > 0 && intervalValid;

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

                    {/* 合成一个多选：渠道与模型各自不再是独立下拉, 而是「渠道名/模型名」的组合选项。 */}
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

                    {/* 已选目标预览：每条就是一条真实会被监控的凭据, 一览无遗。 */}
                    {selected.length > 0 && <TargetPreview targets={targets} channels={channels ?? []} />}

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

// TargetPreview 列出已选的目标 —— 每一条都是一条真实会被监控的 (渠道, 模型)。
function TargetPreview({ targets, channels }: { targets: ScheduledProbeTarget[]; channels: { channel_id: number; channel_name: string }[] }) {
    const t = useTranslations('scheduledProbe');

    if (targets.length === 0) {
        return null;
    }

    const channelNameOf = (channelId: number) =>
        channels.find((channel) => channel.channel_id === channelId)?.channel_name ?? `#${channelId}`;

    return (
        <>
            <p className="text-xs text-muted-foreground">{t('form.targetCount', { count: targets.length })}</p>
            <div className="flex max-h-28 flex-wrap gap-1 overflow-y-auto rounded-xl border border-border/60 p-2">
                {targets.map((target) => (
                    <Badge
                        key={targetKey(target)}
                        variant="secondary"
                        className="max-w-full truncate px-1.5 py-0 text-xs font-normal"
                    >
                        {channelNameOf(target.channel_id)} / {target.model_name}
                    </Badge>
                ))}
            </div>
        </>
    );
}
