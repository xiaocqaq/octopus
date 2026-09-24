import {
    MorphingDialog,
    MorphingDialogTrigger,
    MorphingDialogContainer,
    MorphingDialogContent,
    MorphingDialogDescription,
} from '@/components/ui/morphing-dialog';
import { AnimatePresence, motion } from 'motion/react';
import { CheckCircle2, DollarSign, Layers, MessageSquare, XCircle } from 'lucide-react';
import { type ChannelStatsFormatted, useEnableChannel } from '@/api/channel';
import { usePageActionsStore } from '@/components/common/PageActions';
import { ChannelStats } from './Stats';
import { ChannelForm } from './Form';
import { useTranslations } from 'use-intl';
import { Tooltip, TooltipTrigger, TooltipContent } from '@/components/ui/tooltip';
import { Switch } from '@/components/ui/switch';
import { toast } from 'sonner';
import { useState } from 'react';

// Card 展示单个渠道的概览; 名称, 启停与统计都在同一条统计查询里, 整份配置只在点开编辑时另取。
export function Card({ channel }: { channel: ChannelStatsFormatted }) {
    const t = useTranslations('channel.card');
    // 布局是渠道页共享的视图选项, 直接取用而不由列表层层传入。
    const layout = usePageActionsStore((state) => state.layouts.channel || 'grid');
    const enableChannel = useEnableChannel();
    // 弹窗默认展示统计, 编辑入口在统计视图的头部; 关闭后下次打开仍回到统计, 故点卡片时复位。
    const [openInEditing, setOpenInEditing] = useState(false);
    const metrics = channel.formatted;

    // 列表布局的统计项, 缺少 unit 时只显示数值
    const listMetrics: { icon: React.ReactNode; label: string; value: string | number; unit?: string }[] = [
        { icon: <MessageSquare className="size-4 shrink-0 text-primary" />, label: t('requestCount'), value: metrics.request_count.formatted.value, unit: metrics.request_count.formatted.unit },
        { icon: <Layers className="size-4 shrink-0 text-primary" />, label: t('model'), value: channel.models.length },
        { icon: <CheckCircle2 className="size-4 shrink-0 text-emerald-500" />, label: t('successRequests'), value: metrics.request_success.formatted.value },
        { icon: <XCircle className="size-4 shrink-0 text-destructive" />, label: t('failedRequests'), value: metrics.request_failed.formatted.value },
        { icon: <DollarSign className="size-4 shrink-0 text-primary" />, label: t('totalCost'), value: metrics.total_cost.formatted.value, unit: metrics.total_cost.formatted.unit },
    ];

    // 网格布局的统计项
    const gridMetrics = [
        { icon: <MessageSquare className="size-4 shrink-0 text-primary" />, label: t('requestCount'), metric: metrics.request_count },
        { icon: <DollarSign className="size-4 shrink-0 text-primary" />, label: t('totalCost'), metric: metrics.total_cost },
    ];

    const handleEnableChange = (checked: boolean) => {
        enableChannel.mutate(
            { id: channel.channel_id, enabled: checked },
            {
                onSuccess: () => {
                    toast.success(checked ? t('toast.enabled') : t('toast.disabled'));
                },
                onError: (error) => {
                    toast.error(error.message);
                },
            }
        );
    };

    return (
        <MorphingDialog>
            <MorphingDialogTrigger className="w-full">
                <article
                    onClickCapture={() => setOpenInEditing(false)}
                    className="flex flex-col gap-4 rounded-3xl border border-border bg-card text-card-foreground p-4"
                >
                    <header className="flex items-center justify-between gap-2">
                        <Tooltip>
                            <TooltipTrigger asChild>
                                <h3 className="text-lg font-bold truncate min-w-0">{channel.channel_name}</h3>
                            </TooltipTrigger>
                            <TooltipContent key={channel.channel_name} side="top" sideOffset={10} align="center">
                                {channel.channel_name}
                            </TooltipContent>
                        </Tooltip>
                        <div className="flex shrink-0 items-center gap-1">
                            <Switch
                                checked={channel.enabled}
                                onCheckedChange={handleEnableChange}
                                disabled={enableChannel.isPending}
                                onClick={(e) => e.stopPropagation()}
                            />
                        </div>
                    </header>

                    {layout === 'list' ? (
                        <dl className="grid grid-cols-2 gap-2 lg:grid-cols-5">
                            {listMetrics.map(({ icon, label, value, unit }) => (
                                <div key={label} className="rounded-2xl border border-border/70 bg-background/80 p-2">
                                    <dt className="mb-1 flex items-center gap-1.5 text-xs text-muted-foreground">
                                        {icon}
                                        <span className="truncate">{label}</span>
                                    </dt>
                                    <dd className="text-right text-xl font-bold">
                                        {value}
                                        {unit && <span className="ml-1 text-xs font-normal text-muted-foreground">{unit}</span>}
                                    </dd>
                                </div>
                            ))}
                        </dl>
                    ) : (
                        <dl className="grid grid-cols-1 gap-3">
                            {gridMetrics.map(({ icon, label, metric }) => (
                                <div key={label} className="rounded-2xl border border-border/70 bg-background/80 p-2">
                                    <dt className="flex items-center gap-1.5 text-xs text-muted-foreground">
                                        {icon}
                                        <span className="truncate">{label}</span>
                                    </dt>
                                    <dd className="mt-1 text-right text-xl font-bold">
                                        {metric.formatted.value}
                                        <span className="ml-1 text-xs font-normal text-muted-foreground">{metric.formatted.unit}</span>
                                    </dd>
                                </div>
                            ))}
                        </dl>
                    )}

                </article>
            </MorphingDialogTrigger>

            <MorphingDialogContainer>
                <MorphingDialogContent
                    dismissOnClickOutside={!openInEditing}
                    className="relative w-full max-w-[calc(100vw-1rem)] md:max-w-3xl max-h-[calc(100dvh-1rem)] bg-card text-card-foreground p-3 md:p-4 rounded-2xl md:rounded-3xl overflow-hidden"
                >
                    {/* 高度固定在外层, 与表单自带的高度取同一值: 切换时两者同高, 弹窗才不会随内容缩放。
                        两个视图绝对定位重叠, 退场与入场同时进行: 串行会在两段动画之间留出谁都不在的空档。
                        重叠期间旧视图 pointer-events-none, 否则正在淡出的那份还能挡住点击。
                        位移方向表达前进与后退: 进编辑时表单自右侧推入, 返回统计时反向。 */}
                    <MorphingDialogDescription className="relative h-[min(29rem,calc(100vh-10rem))]">
                        <AnimatePresence initial={false}>
                            {openInEditing ? (
                                <motion.div
                                    key="form"
                                    initial={{ opacity: 0, x: 20 }}
                                    animate={{ opacity: 1, x: 0 }}
                                    exit={{ opacity: 0, x: 20, pointerEvents: 'none' }}
                                    transition={{ type: 'spring', stiffness: 500, damping: 40 }}
                                    className="absolute inset-0"
                                >
                                    <ChannelForm
                                        channelId={channel.channel_id}
                                        onBack={() => setOpenInEditing(false)}
                                    />
                                </motion.div>
                            ) : (
                                <motion.div
                                    key="stats"
                                    initial={{ opacity: 0, x: -20 }}
                                    animate={{ opacity: 1, x: 0 }}
                                    exit={{ opacity: 0, x: -20, pointerEvents: 'none' }}
                                    transition={{ type: 'spring', stiffness: 500, damping: 40 }}
                                    className="absolute inset-0 overflow-y-auto overscroll-contain"
                                >
                                    <ChannelStats
                                        channel={channel}
                                        onEdit={() => setOpenInEditing(true)}
                                    />
                                </motion.div>
                            )}
                        </AnimatePresence>
                    </MorphingDialogDescription>
                </MorphingDialogContent>
            </MorphingDialogContainer>
        </MorphingDialog>
    );
}
