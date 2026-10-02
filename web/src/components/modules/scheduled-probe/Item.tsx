import { useState } from 'react';
import { Candy, Check, Pencil, Trash2, X, Zap } from 'lucide-react';
import { useTranslations } from 'use-intl';
import { toast } from 'sonner';
import {
    useDeleteScheduledProbe,
    useProbeScheduledIQNow,
    useProbeScheduledNow,
    useUpdateScheduledProbe,
    type ScheduledProbe,
    type ScheduledProbeInput,
} from '@/api/scheduled-probe';
import { Switch } from '@/components/ui/switch';
import { IconButton } from '@/components/common/IconButton';
import { Tooltip, TooltipContent, TooltipTrigger } from '@/components/ui/tooltip';
import { getModelIcon } from '@/lib/model-icons';
import { cn } from '@/lib/utils';
import { describeWindow } from './format';
import { RowList } from './RowList';
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
export function Item({ probe, now }: { probe: ScheduledProbe; now: number }) {
    const t = useTranslations('scheduledProbe');
    const updateProbe = useUpdateScheduledProbe();
    const deleteProbe = useDeleteScheduledProbe();
    const probeNow = useProbeScheduledNow();
    const probeIQ = useProbeScheduledIQNow();
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

    // handleProbeIQ 跑一次糖果测试：把这条任务的每条凭据都问一遍智商题。
    //
    // 汇报口径是"正常 / 降智"两档, 而不是把答案数字抛出来: 结论是要一眼扫过去的,
    // 具体答了几留在行里的悬停提示, 谁想知道再去看。
    // 三类要分开数: 请求失败的既不是正常也不是降智(它根本没答上话), 混进任何一档都是假结论。
    // 全绿时用 success、出现降智时用 warning 而不是 error: 降智是"模型变笨了"这个观察结果,
    // 不是一次失败的操作 —— 用报错色会让人以为测试本身没跑成。
    const handleProbeIQ = () => {
        probeIQ.mutate(probe.id, {
            onSuccess: (rows) => {
                // 按 iq_asked 而不是 ok 分档: 后端目前"成功即问过", 但这是两处实现之间的耦合,
                // 界面读自己拿到的那三个字段就够了 —— 判据越少, 以后改后端时越不容易在这里悄悄错档。
                const normal = rows.filter((row) => row.iq_asked && row.iq_correct).length;
                const dumb = rows.filter((row) => row.iq_asked && !row.iq_correct).length;
                const failed = rows.length - normal - dumb;
                const summary = t('toast.iqDone', { normal, dumb });
                const description = failed > 0 ? t('toast.iqPartialFailed', { count: failed }) : undefined;
                if (dumb === 0 && failed === 0) {
                    toast.success(summary);
                    return;
                }
                if (dumb > 0) {
                    toast.warning(summary, { description });
                    return;
                }
                toast.error(summary, { description });
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
                        aria-label={t('probeNow')}
                        className="size-7"
                    >
                        <Zap className={cn('size-3.5', probeNow.isPending && 'animate-pulse')} />
                    </IconButton>

                    {/* 糖果按钮紧挨着闪电：两个都是"手动打一发"，区别只在问什么。
                        放一起才看得出这是一对, 否则一颗糖孤零零挂在一排工具图标里, 看不出它和测活有关。
                        用糖果而不是大脑图标: 大脑已经用在行里的结论上了, 同一个图标既表示"结论"又表示"去测"会让人读错。 */}
                    <IconButton
                        onClick={handleProbeIQ}
                        disabled={probeIQ.isPending || probe.rows.length === 0}
                        tip={t('iqNow')}
                        aria-label={t('iqNow')}
                        className="size-7"
                    >
                        <Candy className={cn('size-3.5', probeIQ.isPending && 'animate-pulse')} />
                    </IconButton>

                    {/* 编辑弹窗自带 provider，trigger 由 IconButton asChild 承载 motion.div 形变。 */}
                    <MorphingDialog>
                        <IconButton asChild tip={t('edit')} className="size-7">
                            <MorphingDialogTrigger aria-label={t('edit')}>
                                <Pencil className="size-3.5" />
                            </MorphingDialogTrigger>
                        </IconButton>
                        <MorphingDialogContainer>
                            {/* 与新建弹窗同一套宽度写法: 不显式给宽度, 弹窗会缩到内容宽度而塌成窄缝。
                                标题加 shrink-0: 它和下面可滚动的表单同处一列, 不加就会被表单压扁。 */}
                            <MorphingDialogContent
                                dismissOnClickOutside={false}
                                className="relative flex h-[calc(100dvh-2rem)] w-screen max-w-full flex-col overflow-hidden rounded-3xl bg-card px-4 py-3 text-card-foreground md:h-auto md:max-h-[calc(100vh-2rem)] md:max-w-2xl md:px-6 md:py-4"
                            >
                                <MorphingDialogTitle className="shrink-0">{t('editTitle', { name: probe.name })}</MorphingDialogTitle>
                                <EditDialogContent initial={probe} />
                            </MorphingDialogContent>
                        </MorphingDialogContainer>
                    </MorphingDialog>

                    {confirmDelete ? (
                        <>
                            {/* 与渠道卡片同一套确认方式: ✕ 取消、✓ 确认删除。
                                文字按钮在这么窄的一行里要占掉两个按钮的宽度, 标题会被挤没;
                                图标只占一个按钮位, 和它替掉的垃圾桶一样大, 换进来不会让整行跳动。
                                ✓ 用危险色而不是绿色: 这一下点下去是删除, 颜色该提示后果而不是"成功"。 */}
                            <IconButton
                                onClick={() => setConfirmDelete(false)}
                                tip={t('cancel')}
                                aria-label={t('cancel')}
                                className="size-7"
                            >
                                <X className="size-3.5" />
                            </IconButton>
                            <IconButton
                                onClick={handleConfirmDelete}
                                disabled={deleteProbe.isPending}
                                tip={t('confirmDelete')}
                                aria-label={t('confirmDelete')}
                                className="size-7 text-destructive hover:text-destructive/70"
                            >
                                <Check className="size-3.5" />
                            </IconButton>
                        </>
                    ) : (
                        <IconButton
                            onClick={() => setConfirmDelete(true)}
                            disabled={deleteProbe.isPending}
                            tip={t('delete')}
                            aria-label={t('delete')}
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

            <RowList probeId={probe.id} rows={probe.rows} now={now} />
        </article>
    );
}
