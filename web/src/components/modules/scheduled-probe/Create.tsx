import { useEffect, useMemo, useState } from 'react';
import { useTranslations } from 'use-intl';
import { toast } from 'sonner';
import { useCreateScheduledProbe, SCHEDULED_PROBE_DEFAULT_INTERVAL, SCHEDULED_PROBE_HOURS } from '@/api/scheduled-probe';
import { useChannelDetail, useChannelStats } from '@/api/channel';
import { Button } from '@/components/ui/button';
import { Input } from '@/components/ui/input';
import { Field, FieldDescription, FieldGroup, FieldLabel } from '@/components/ui/field';
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from '@/components/ui/select';
import { MorphingDialogDescription, useMorphingDialog } from '@/components/ui/morphing-dialog';
import { formatHour } from './format';
import { WeekdayPicker } from './WeekdayPicker';

// CreateDialogContent 渲染新建定时测活任务的表单。
// 渠道与模型两级联动: 模型下拉只列所选渠道已有的模型, 与后端提交时的校验同一口径 ——
// 后端拒绝"模型不属于该渠道", 前端就别让这种组合有机会被选中。
export function CreateDialogContent() {
    const { setIsOpen } = useMorphingDialog();
    const t = useTranslations('scheduledProbe');
    const createProbe = useCreateScheduledProbe();
    const { data: channels } = useChannelStats();
    const [channelId, setChannelId] = useState<number | undefined>(undefined);
    const [modelName, setModelName] = useState('');
    const [interval, setIntervalValue] = useState(String(SCHEDULED_PROBE_DEFAULT_INTERVAL));
    const [weekdays, setWeekdays] = useState(0);
    const [startHour, setStartHour] = useState(9);
    const [endHour, setEndHour] = useState(18);

    // 只在选好渠道后才拉该渠道的模型：渠道列表自带模型集合，但那份来自列表接口，可能落后于刚编辑过的配置。
    const { data: detail } = useChannelDetail(channelId);

    const models = useMemo(() => detail?.models ?? [], [detail]);

    // 换渠道要清掉已选模型：上一个渠道的模型名在新渠道里多半不存在，留着会提交出必被拒绝的组合。
    useEffect(() => {
        setModelName('');
    }, [channelId]);

    const intervalMinutes = Number(interval);
    const intervalValid = Number.isInteger(intervalMinutes) && intervalMinutes >= 1 && intervalMinutes <= 1440;
    const canSubmit = channelId !== undefined && modelName !== '' && intervalValid;

    const handleSubmit = (event: React.FormEvent<HTMLFormElement>) => {
        event.preventDefault();
        if (!canSubmit) return;

        createProbe.mutate(
            {
                channel_id: channelId,
                model_name: modelName,
                interval_minutes: intervalMinutes,
                enabled: true,
                weekdays,
                start_hour: startHour,
                end_hour: endHour,
            },
            {
                onSuccess: () => setIsOpen(false),
                onError: (error) => toast.error(t('toast.createFailed'), { description: error.message }),
            }
        );
    };

    return (
        <div className="w-screen max-w-full md:max-w-xl">
            <MorphingDialogDescription>
                <form onSubmit={handleSubmit}>
                    <FieldGroup className="gap-4">
                        <Field>
                            <FieldLabel htmlFor="scheduled-probe-channel">{t('form.channel')}</FieldLabel>
                            <Select
                                value={channelId === undefined ? undefined : String(channelId)}
                                onValueChange={(value) => setChannelId(Number(value))}
                            >
                                <SelectTrigger id="scheduled-probe-channel" className="w-full rounded-xl">
                                    <SelectValue placeholder={t('form.channelPlaceholder')} />
                                </SelectTrigger>
                                <SelectContent>
                                    {(channels ?? []).map((channel) => (
                                        <SelectItem key={channel.channel_id} value={String(channel.channel_id)}>
                                            {channel.channel_name}
                                        </SelectItem>
                                    ))}
                                </SelectContent>
                            </Select>
                        </Field>

                        <Field>
                            <FieldLabel htmlFor="scheduled-probe-model">{t('form.model')}</FieldLabel>
                            <Select
                                value={modelName === '' ? undefined : modelName}
                                onValueChange={setModelName}
                                disabled={channelId === undefined || models.length === 0}
                            >
                                <SelectTrigger id="scheduled-probe-model" className="w-full rounded-xl">
                                    <SelectValue placeholder={t('form.modelPlaceholder')} />
                                </SelectTrigger>
                                <SelectContent>
                                    {models.map((model) => (
                                        <SelectItem key={model} value={model}>{model}</SelectItem>
                                    ))}
                                </SelectContent>
                            </Select>
                            {channelId !== undefined && models.length === 0 && (
                                <FieldDescription>{t('form.modelEmpty')}</FieldDescription>
                            )}
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

                        <div className="flex gap-2">
                            <Button
                                type="button"
                                variant="secondary"
                                onClick={() => setIsOpen(false)}
                                className="h-11 flex-1 rounded-xl"
                            >
                                {t('cancel')}
                            </Button>
                            <Button
                                type="submit"
                                disabled={createProbe.isPending || !canSubmit}
                                className="h-11 flex-1 rounded-xl"
                            >
                                {createProbe.isPending ? t('submitting') : t('submit')}
                            </Button>
                        </div>
                    </FieldGroup>
                </form>
            </MorphingDialogDescription>
        </div>
    );
}
