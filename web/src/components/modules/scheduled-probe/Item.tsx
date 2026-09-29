import { useState } from 'react';
import { HeartCrack, HeartPulse, Pencil, Trash2, Zap } from 'lucide-react';
import { useTranslations } from 'use-intl';
import { toast } from 'sonner';
import {
    useDeleteScheduledProbe,
    useProbeGrantNow,
    useProbeScheduledNow,
    useUpdateScheduledProbe,
    type ScheduledProbe,
    type ScheduledProbeInput,
    type ScheduledProbeRow,
} from '@/api/scheduled-probe';
import { Switch } from '@/components/ui/switch';
import { IconButton } from '@/components/common/IconButton';
import { Tooltip, TooltipContent, TooltipTrigger } from '@/components/ui/tooltip';
import { getModelIcon } from '@/lib/model-icons';
import { cn } from '@/lib/utils';
import { describeProbedAt, describeWindow } from './format';
import {
    MorphingDialog,
    MorphingDialogContainer,
    MorphingDialogContent,
    MorphingDialogTitle,
    MorphingDialogTrigger,
} from '@/components/ui/morphing-dialog';
import { EditDialogContent } from './Edit';

// toInput 把任务还原为编辑时提交的体：更新是整体替换，未列出的字段会被清空。
// enabled 交给调用方传入：开关在卡片上，表单本身不参与启停，否则勾一下框又被表单悄悄关掉。
function toInput(probe: ScheduledProbe, enabled: boolean): ScheduledProbeInput {
    return {
        name: probe.name,
        targets: probe.targets,
        interval_minutes: probe.interval_minutes,
        enabled,
        weekdays: probe.weekdays,
        start_hour: probe.start_hour,
        end_hour: probe.end_hour,
    };
}

// Item 展示一条模型监控任务：一个自定义名字下挂多行凭据，每行各可独立测一次。
//
// 三个控制按钮(图标的测一次 / 编辑 / 删除)与启用开关排在同一行：
// 用户截图里它们就靠在一起，把"立刻测一次"改成图标后，一行放下全部控件不会堆累赘。
// 编辑弹窗由卡片自身管理，就像分组页那样：trigger 必须在 provider 內才能取 context。
//
// 卡片头不摆"每 10 分钟 · 不限时段"那枚徽标: 它恒定占着宽度, 把一行能放下的卡片数从四个压到两个,
// 而这两项配置是设一次就很少再看的东西 —— 挪到名字的悬停提示里, 想核对时同样一眼可见, 平时不占版面。
export function Item({ probe }: { probe: ScheduledProbe }) {
    const t = useTranslations('scheduledProbe');
    const updateProbe = useUpdateScheduledProbe();
    const deleteProbe = useDeleteScheduledProbe();
    const probeNow = useProbeScheduledNow();
    const [confirmDelete, setConfirmDelete] = useState(false);

    const window = describeWindow(probe, t);
    // 图标与名字取自任务名；名字是用户自定的（通常就是模型名），沒匹配到兜個通用图标。
    const { Icon, className: iconClassName, color: brandColor } = getModelIcon(probe.name);
    // 名字的悬停提示顺带给出间隔与时段: 徽标撤掉之后, 这两项配置总得有个地方能看到。
    const titleHint = `${probe.name} · ${t('intervalValue', { minutes: probe.interval_minutes })} · ${window}`;

    const handleEnableChange = (checked: boolean) => {
        updateProbe.mutate({ ...toInput(probe, checked), id: probe.id }, {
            onSuccess: () => toast.success(checked ? t('enabled') : t('disabled')),
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

    const handleProbeAll = () => {
        probeNow.mutate(probe.id, {
            onSuccess: (rows) => {
                // 逐条汇报而不是只说"完成"：多凭据时用户要看的就是哪条通、哪条不通。
                const failed = rows.filter((row) => !row.ok);
                if (failed.length === 0) {
                    toast.success(t('toast.probeDone'));
                    return;
                }
                toast.error(t('toast.probeFailed', { message: failed[0]!.message }), {
                    description: failed.length > 1 ? t('toast.probePartialFailed', { count: failed.length }) : undefined,
                });
            },
            onError: (error) => toast.error(error.message),
        });
    };

    return (
        <article className="flex h-full flex-col gap-1.5 rounded-2xl border border-border bg-card p-2.5 text-card-foreground">
            <div className="flex items-center gap-1.5">
                {/* 图标和名字设置比渠道名/key 小一点：名字只是标题，渠道名/key 是这行真正的主键。
                    把重点放在行里的渠道名/key 上, 让人扫一眼就知道"这是哪条凭据"。 */}
                <Icon aria-hidden="true" className={cn('size-5 shrink-0', iconClassName)} style={{ color: brandColor }} />
                <Tooltip>
                    <TooltipTrigger asChild>
                        <span className="min-w-0 flex-1 truncate text-sm font-semibold leading-tight">{probe.name}</span>
                    </TooltipTrigger>
                    <TooltipContent side="top" sideOffset={10} align="start">{titleHint}</TooltipContent>
                </Tooltip>

                <div className="flex shrink-0 items-center gap-0.5">
                    {/* 图标按钮替代文字："立刻测一次"这个称呼在同一行也放不下，图标正好又显得轻量。
                        放在删除与启用开关之前: 用户多半先点测一次看看效果，再决定要不要删或关。 */}
                    <IconButton
                        onClick={handleProbeAll}
                        disabled={probeNow.isPending || probe.rows.length === 0}
                        tip={t('probeNow')}
                        className="size-7"
                    >
                        <Zap className={cn('size-3.5', probeNow.isPending && 'animate-pulse')} />
                    </IconButton>

                    {/* 编辑弹窗自带 provider，trigger 由 IconButton asChild 承载 motion.div 形变。 */}
                    <MorphingDialog>
                        <IconButton asChild tip={t('edit')} className="size-7">
                            <MorphingDialogTrigger>
                                <Pencil className="size-3.5" />
                            </MorphingDialogTrigger>
                        </IconButton>
                        <MorphingDialogContainer>
                            <MorphingDialogContent
                                dismissOnClickOutside={false}
                                className="flex max-h-[calc(100vh-2rem)] w-fit max-w-full flex-col overflow-hidden rounded-3xl bg-card px-6 py-4 text-card-foreground"
                            >
                                <MorphingDialogTitle>{t('editTitle', { name: probe.name })}</MorphingDialogTitle>
                                <EditDialogContent initial={probe} />
                            </MorphingDialogContent>
                        </MorphingDialogContainer>
                    </MorphingDialog>

                    {confirmDelete ? (
                        <div className="flex items-center gap-0.5">
                            {/* 确认删除就地换成两个小按钮而不弹窗: 卡片已经很窄, 再开一层弹窗反而更重;
                                按钮文字收到 11px 才不至于把标题挤没, 而"取消/确认删除"四个字本身已足够清楚。 */}
                            <button
                                type="button"
                                onClick={() => setConfirmDelete(false)}
                                className="h-6 rounded-md border border-border px-1.5 text-[11px] font-medium hover:bg-muted/30"
                            >
                                {t('cancel')}
                            </button>
                            <button
                                type="button"
                                onClick={handleConfirmDelete}
                                disabled={deleteProbe.isPending}
                                className="h-6 rounded-md bg-destructive px-1.5 text-[11px] font-medium text-destructive-foreground disabled:opacity-50"
                            >
                                {t('confirmDelete')}
                            </button>
                        </div>
                    ) : (
                        <IconButton
                            onClick={() => setConfirmDelete(true)}
                            disabled={deleteProbe.isPending}
                            tip={t('delete')}
                            className="size-7 hover:text-destructive"
                        >
                            <Trash2 className="size-3.5" />
                        </IconButton>
                    )}

                    <Switch
                        checked={probe.enabled}
                        onCheckedChange={handleEnableChange}
                        disabled={updateProbe.isPending || deleteProbe.isPending}
                        aria-label={t('enable')}
                    />
                </div>
            </div>

            <RowList rows={probe.rows} />
        </article>
    );
}

// RowList 逐行列出各条凭据。一行 = 一条凭据，行尾的闪电只测这一条。
function RowList({ rows }: { rows: ScheduledProbeRow[] }) {
    const t = useTranslations('scheduledProbe');
    if (rows.length === 0) {
        return <p className="px-1 py-2 text-xs text-muted-foreground/70">{t('noTarget')}</p>;
    }

    return (
        <ul className="flex flex-col gap-1">
            {rows.map((row, index) => (
                <Row key={row.grant_id > 0 ? row.grant_id : `empty-${index}`} row={row} />
            ))}
        </ul>
    );
}

// Row 渲染一条凭据：左侧是「渠道名/凭据名」(大字)、中间是结论、右侧是只测这一条的闪电。
function Row({ row }: { row: ScheduledProbeRow }) {
    const t = useTranslations('scheduledProbe');
    const probeGrant = useProbeGrantNow();
    const hasGrant = row.grant_id > 0;
    const label = row.key_name || `#${row.grant_id}`;

    const handleProbe = () => {
        probeGrant.mutate(row.grant_id, {
            onSuccess: () => toast.success(t('toast.probeDone')),
            onError: (error) => toast.error(error.message),
        });
    };

    return (
        <li className="flex items-center gap-1.5 rounded-lg border border-border/60 px-1.5 py-1">
            {/* 渠道名與凭据名字体更大: 它们回答"这条通道、这把钥匙"，是扫列表时第一眼要看的。
                模型名只在卡片标题出现, 行里不重复；同个任务挂多模型时才补在 tooltip，别把主视觉打散。 */}
            <Tooltip>
                <TooltipTrigger asChild>
                    <span className="min-w-0 flex-1 truncate text-sm">
                        <span className="text-foreground">{row.channel_name || `#${row.channel_id}`}</span>
                        <span className="text-muted-foreground/50">/</span>
                        <span className="text-foreground">{label}</span>
                    </span>
                </TooltipTrigger>
                <TooltipContent side="top" sideOffset={10} align="start">
                    {`${row.channel_name || `#${row.channel_id}`} / ${label}`}
                    {row.model_name ? ` · ${row.model_name}` : ''}
                </TooltipContent>
            </Tooltip>

            {row.probed && (
                <span className="flex shrink-0 items-center gap-1 text-[11px] text-muted-foreground">
                    {row.ok ? (
                        <HeartPulse className="size-3.5 text-emerald-600 dark:text-emerald-400" />
                    ) : (
                        <HeartCrack className="size-3.5 text-rose-600 dark:text-rose-400" />
                    )}
                    <span className="tabular-nums">{row.latency_ms}ms</span>
                    <span className="text-muted-foreground/60">{describeProbedAt(row.probed_at, t)}</span>
                </span>
            )}

            {/* 行尾的闪电只测这一条凭据: 点击它的意图是"我想看清这条钥匙通不通"，
                而不是把整个任务都拧起来一起测。 */}
            <IconButton
                onClick={handleProbe}
                disabled={!hasGrant || probeGrant.isPending}
                tip={hasGrant ? t('probeThis') : t('noCredential')}
                className="size-6"
            >
                <Zap className={cn('size-3', probeGrant.isPending && 'animate-pulse')} />
            </IconButton>
        </li>
    );
}