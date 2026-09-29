import { useState } from 'react';
import { useQueries } from '@tanstack/react-query';
import { useTranslations } from 'use-intl';
import { channelDetailQueryOptions, useChannelStats } from '@/api/channel';
import { SCHEDULED_PROBE_DEFAULT_INTERVAL, SCHEDULED_PROBE_HOURS, type ScheduledProbeTarget } from '@/api/scheduled-probe';
import { Badge } from '@/components/ui/badge';
import { Button } from '@/components/ui/button';
import { Input } from '@/components/ui/input';
import { Field, FieldDescription, FieldGroup, FieldLabel } from '@/components/ui/field';
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from '@/components/ui/select';
import { formatHour } from './format';
import { WeekdayPicker } from './WeekdayPicker';
import { SearchMultiSelect, type SearchOption } from './SearchMultiSelect';
import { distinct, expandTargets } from './targets';

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

// ProbeForm 是创建与编辑共用的表单：一个自定义名字 + 渠道与模型（各可多选）+ 间隔 + 时段。
//
// 创建与编辑共用一份：两者字段完全相同，分开写就要把"名字、目标展开、间隔校验、时段"这套逻辑维护两遍，
// 而它们必须保持一致 —— 编辑里少一个字段就会把用户刚配好的值清掉（更新是整体替换）。
export function ProbeForm({ initial, submitText, submittingText, isSubmitting, onCancel, onSubmit }: ProbeFormProps) {
    const t = useTranslations('scheduledProbe');
    const { data: channels } = useChannelStats();
    const [name, setName] = useState(initial?.name ?? '');
    // 渠道与模型各存一份选择，目标由两者展开而来：用户勾的是渠道和模型，不必一行行手工拼组合。
    const [channelIds, setChannelIds] = useState<number[]>(() => distinct((initial?.targets ?? []).map((target) => target.channel_id)));
    const [modelNames, setModelNames] = useState<string[]>(() => distinct((initial?.targets ?? []).map((target) => target.model_name)));
    const [interval, setIntervalValue] = useState(String(initial?.interval_minutes ?? SCHEDULED_PROBE_DEFAULT_INTERVAL));
    const [weekdays, setWeekdays] = useState(initial?.weekdays ?? 0);
    const [startHour, setStartHour] = useState(initial?.start_hour ?? 9);
    const [endHour, setEndHour] = useState(initial?.end_hour ?? 18);

    // 模型下拉的备选来自**已选渠道的全部模型**：一个模型可能被多个渠道提供，故顺手记下"哪些渠道提供它"，
    // 展开目标时逐个配对，界面上也把来源标出来。
    // 逐渠道取详情而不是读渠道列表里的模型：列表那份可能落后于刚编辑过的配置，模型列表必须与提交时的校验同源。
    const detailQueries = useQueries({ queries: channelIds.map((id) => channelDetailQueryOptions(id)) });
    const modelsOf = new Map<number, string[]>();
    const providers = new Map<string, number[]>();
    channelIds.forEach((channelId, index) => {
        const models = detailQueries[index]?.data?.models;
        if (!models) return;
        modelsOf.set(channelId, models);
        for (const modelName of models) {
            providers.set(modelName, [...(providers.get(modelName) ?? []), channelId]);
        }
    });

    const channelNameOf = (channelId: number) =>
        channels?.find((channel) => channel.channel_id === channelId)?.channel_name ?? `#${channelId}`;

    // 只保留仍有渠道提供的模型：取消勾选渠道后，原先选的模型可能已经无处可挂，
    // 继续以"已选"的样子留在下拉里，用户会以为它还在被监控。
    const selectedModels = modelNames.filter((modelName) => providers.has(modelName));
    const targets = expandTargets(channelIds, selectedModels, modelsOf);

    const intervalMinutes = Number(interval);
    const intervalValid = Number.isInteger(intervalMinutes) && intervalMinutes >= 1 && intervalMinutes <= 1440;
    const canSubmit = name.trim() !== '' && targets.length > 0 && intervalValid;

    const channelOptions: SearchOption[] = (channels ?? []).map((channel) => ({
        value: String(channel.channel_id),
        label: channel.channel_name,
        hint: t('form.modelCount', { count: channel.models.length }),
        keywords: channel.enabled ? '' : t('form.channelDisabled'),
    }));

    const modelOptions: SearchOption[] = [...providers.entries()]
        .map(([modelName, owners]) => ({
            value: modelName,
            label: modelName,
            // 只被一个渠道提供就写渠道名，多个则写个数：下拉一行放不下几个渠道名，写全反而把模型名挤没了。
            hint: owners.length > 1 ? t('form.channelCount', { count: owners.length }) : channelNameOf(owners[0] ?? 0),
        }))
        .sort((left, right) => left.value.localeCompare(right.value));

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
            {/* 表单可能很长，故让字段区自己滚动、按钮固定在底部：
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
                        <FieldLabel htmlFor="scheduled-probe-channels">{t('form.channel')}</FieldLabel>
                        <SearchMultiSelect
                            id="scheduled-probe-channels"
                            options={channelOptions}
                            selected={channelIds.map(String)}
                            onChange={(values) => setChannelIds(values.map(Number))}
                            placeholder={t('form.channelPlaceholder')}
                            searchPlaceholder={t('form.searchChannel')}
                            emptyText={t('form.channelEmpty')}
                            selectAllLabel={t('form.selectAll')}
                            clearLabel={t('form.clear')}
                        />
                        <FieldDescription>{t('form.channelHint')}</FieldDescription>
                    </Field>

                    <Field>
                        <FieldLabel htmlFor="scheduled-probe-models">{t('form.model')}</FieldLabel>
                        <SearchMultiSelect
                            id="scheduled-probe-models"
                            options={modelOptions}
                            selected={selectedModels}
                            onChange={setModelNames}
                            placeholder={t('form.modelPlaceholder')}
                            searchPlaceholder={t('form.searchModel')}
                            emptyText={channelIds.length === 0 ? t('form.modelPickChannelFirst') : t('form.modelEmpty')}
                            // 只在没选渠道时禁用：选了渠道但一个模型都没有时要让用户打得开面板，
                            // 否则"该渠道没有模型，先去渠道页添加"这句话永远显示不出来。
                            disabled={channelIds.length === 0}
                            selectAllLabel={t('form.selectAll')}
                            clearLabel={t('form.clear')}
                        />
                        <FieldDescription>{t('form.modelHint')}</FieldDescription>
                    </Field>

                    <Field>
                        <FieldLabel>{t('form.targets')}</FieldLabel>
                        <TargetPreview targets={targets} nameOf={channelNameOf} />
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

// TargetPreview 列出这次提交真正会产生的监控目标。
// 渠道与模型都是多选，最终目标是"渠道 × 模型"再按各渠道实际提供的模型过滤，
// 光看两个下拉数不出最后有几条；把结果铺开，用户按下保存之前就能确认自己没多勾。
function TargetPreview({ targets, nameOf }: { targets: ScheduledProbeTarget[]; nameOf: (channelId: number) => string }) {
    const t = useTranslations('scheduledProbe');

    if (targets.length === 0) {
        return <p className="text-xs text-muted-foreground">{t('form.targetsEmpty')}</p>;
    }

    return (
        <>
            <p className="text-xs text-muted-foreground">{t('form.targetCount', { count: targets.length })}</p>
            <div className="flex max-h-28 flex-wrap gap-1 overflow-y-auto rounded-xl border border-border/60 p-2">
                {targets.map((target) => (
                    <Badge
                        key={`${target.channel_id}:${target.model_name}`}
                        variant="secondary"
                        className="max-w-full truncate px-1.5 py-0 text-xs font-normal"
                    >
                        {nameOf(target.channel_id)} / {target.model_name}
                    </Badge>
                ))}
            </div>
        </>
    );
}
