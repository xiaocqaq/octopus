import { useMemo, useState } from 'react';
import { CalendarClock, Trash2 } from 'lucide-react';
import { useTranslations } from 'use-intl';
import { toast } from 'sonner';
import { useDeleteScheduledProbe, useUpdateScheduledProbe, type ScheduledProbe, type ScheduledProbeInput } from '@/api/scheduled-probe';
import { Switch } from '@/components/ui/switch';
import { IconButton } from '@/components/common/IconButton';
import { Tooltip, TooltipContent, TooltipTrigger } from '@/components/ui/tooltip';
import { describeWindow } from './format';

// toInput 把一条任务还原成提交体：更新是整体替换，未列出的字段会被当作清空，故必须带全。
function toInput(probe: ScheduledProbe, enabled: boolean): ScheduledProbeInput {
    return {
        channel_id: probe.channel_id,
        model_name: probe.model_name,
        interval_minutes: probe.interval_minutes,
        enabled,
        weekdays: probe.weekdays,
        start_hour: probe.start_hour,
        end_hour: probe.end_hour,
    };
}

// Item 展示一条定时测活任务：测的是哪个渠道的哪个模型、多久测一次、什么时段测，以及启停与删除。
export function Item({ probe }: { probe: ScheduledProbe }) {
    const t = useTranslations('scheduledProbe');
    const updateProbe = useUpdateScheduledProbe();
    const deleteProbe = useDeleteScheduledProbe();
    const [confirmDelete, setConfirmDelete] = useState(false);

    // 授权数为 0 时任务会空转：渠道被停用、凭据全被停用或模型已被移除都会落到这里。
    // 这不是错误，只是当下无可测对象，界面上要说清楚，否则用户会以为调度器坏了。
    const window = useMemo(() => describeWindow(probe, t), [probe, t]);

    const handleEnableChange = (checked: boolean) => {
        updateProbe.mutate({ ...toInput(probe, checked), id: probe.id }, {
            onSuccess: () => toast.success(checked ? t('toast.enabled') : t('toast.disabled')),
            onError: (error) => toast.error(error.message),
        });
    };

    const handleConfirmDelete = () => {
        deleteProbe.mutate(probe.id, {
            onSuccess: () => {
                setConfirmDelete(false);
                toast.success(t('toast.deleted'));
            },
            onError: (error) => {
                setConfirmDelete(false);
                toast.error(error.message);
            },
        });
    };

    return (
        <article className="flex items-center gap-3 rounded-3xl border border-border bg-card p-4 text-card-foreground">
            <CalendarClock aria-hidden="true" className="size-9 shrink-0 text-primary" />

            <div className="flex min-w-0 flex-1 flex-col gap-1">
                <div className="flex min-w-0 items-center gap-2">
                    <Tooltip>
                        <TooltipTrigger asChild>
                            <span className="truncate text-base font-semibold leading-tight">{probe.channel_name}</span>
                        </TooltipTrigger>
                        <TooltipContent side="top" sideOffset={10} align="center">{probe.channel_name}</TooltipContent>
                    </Tooltip>
                    <span className="shrink-0 text-muted-foreground">/</span>
                    <Tooltip>
                        <TooltipTrigger asChild>
                            <span className="truncate text-sm text-muted-foreground">{probe.model_name}</span>
                        </TooltipTrigger>
                        <TooltipContent side="top" sideOffset={10} align="center">{probe.model_name}</TooltipContent>
                    </Tooltip>
                </div>

                <p className="flex flex-wrap items-center gap-x-2 gap-y-1 text-xs text-muted-foreground">
                    <span>{t('intervalValue', { minutes: probe.interval_minutes })}</span>
                    <span className="text-muted-foreground/60">|</span>
                    <span>{window}</span>
                    <span className="text-muted-foreground/60">|</span>
                    <span className={probe.grant_count === 0 ? 'text-destructive' : undefined}>
                        {t('grantCount', { count: probe.grant_count })}
                    </span>
                </p>
            </div>

            <div className="flex shrink-0 items-center gap-2">
                <Switch
                    checked={probe.enabled}
                    onCheckedChange={handleEnableChange}
                    disabled={updateProbe.isPending || deleteProbe.isPending}
                    aria-label={t('enable')}
                />

                {confirmDelete ? (
                    <div className="flex items-center gap-1">
                        <button
                            type="button"
                            onClick={() => setConfirmDelete(false)}
                            className="h-8 rounded-lg border border-border px-2 text-xs font-medium hover:bg-muted/30"
                        >
                            {t('cancel')}
                        </button>
                        <button
                            type="button"
                            onClick={handleConfirmDelete}
                            disabled={deleteProbe.isPending}
                            className="h-8 rounded-lg bg-destructive px-2 text-xs font-medium text-destructive-foreground disabled:opacity-50"
                        >
                            {t('confirmDelete')}
                        </button>
                    </div>
                ) : (
                    <IconButton
                        onClick={() => setConfirmDelete(true)}
                        disabled={deleteProbe.isPending}
                        tip={t('delete')}
                        className="size-9 hover:text-destructive"
                    >
                        <Trash2 className="size-4" />
                    </IconButton>
                )}
            </div>
        </article>
    );
}
