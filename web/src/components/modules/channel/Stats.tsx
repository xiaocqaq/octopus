import { Fragment, useEffect, useMemo, useRef, useState } from 'react';
import {
    Activity,
    Check,
    CheckCircle2,
    Clock,
    Copy,
    DollarSign,
    Download,
    Eye,
    EyeOff,
    FileText,
    MessageSquare,
    Pencil,
    Share2,
    Trash2,
    X,
} from 'lucide-react';
import { snapdom } from '@zumer/snapdom';
import { toast } from 'sonner';
import { useTranslations } from 'use-intl';
import { type ChannelStatsFormatted, useDeleteChannel } from '@/api/channel';
import { type StatsMetricsFormatted } from '@/api/stats';
import { useMorphingDialog } from '@/components/ui/morphing-dialog';

type FormattedMetric = StatsMetricsFormatted['request_count'];

// 模型统计的排序维度
type ModelSortKey = 'cost' | 'count' | 'tokens';

// 成功率百分比, 无请求时按 0 处理
const successRate = (success: number, failed: number) => {
    const total = success + failed;
    return total > 0 ? (success / total) * 100 : 0;
};

// MetricValue 统一渲染数值与单位
function MetricValue({ metric }: { metric: FormattedMetric }) {
    return (
        <span>
            {metric.formatted.value}
            <span className="ml-0.5 text-xs font-normal text-muted-foreground">{metric.formatted.unit}</span>
        </span>
    );
}

// ChannelStats 展示单个渠道的汇总指标与模型排行, 布局随容器宽度自适应, 并可导出为分享图
// 统计自带渠道名称与模型清单, 由 /channel/stats 一并给出
// 渠道的编辑与删除入口也在此: 卡片只负责打开弹窗, 需要看清指标后才动手的操作都收在头部
export function ChannelStats({ channel, onEdit }: {
    channel: ChannelStatsFormatted;
    onEdit: () => void; // 切换同一弹窗到编辑表单。
}) {
    const t = useTranslations('channel.stats');
    const tCommon = useTranslations('common');
    const { setIsOpen } = useMorphingDialog();
    const deleteChannel = useDeleteChannel();
    const [modelSort, setModelSort] = useState<ModelSortKey>('cost');
    // 隐藏渠道名称, 分享图也跟随此状态
    const [isNameHidden, setIsNameHidden] = useState(false);
    // 删除需二次确认, 确认态下换成取消与确认两个键
    const [isConfirmingDelete, setIsConfirmingDelete] = useState(false);
    const [isSharing, setIsSharing] = useState(false);
    // 分享图预览, url 用于展示, blob 用于复制和下载
    const [preview, setPreview] = useState<{ url: string; blob: Blob } | null>(null);
    // 分享截图的源节点
    const contentRef = useRef<HTMLDivElement>(null);
    const metrics = channel.formatted;

    // 预览关闭或组件卸载时释放临时对象 URL
    useEffect(() => () => {
        if (preview) URL.revokeObjectURL(preview.url);
    }, [preview]);

    // 渠道汇总指标, 次要行承载成功率与输入/输出明细
    const summary: { icon: React.ReactNode; label: string; value: React.ReactNode; sub?: React.ReactNode }[] = [
        {
            icon: <Activity className="size-3.5 text-chart-1" />,
            label: t('totalRequests'),
            value: <MetricValue metric={metrics.request_count} />,
            sub: (
                <>
                    <span className="text-accent">{metrics.request_success.formatted.value}</span>
                    <span className="text-muted-foreground/40">/</span>
                    <span className="text-destructive">{metrics.request_failed.formatted.value}</span>
                    <span className="text-muted-foreground/40">·</span>
                    <span>{successRate(metrics.request_success.raw, metrics.request_failed.raw).toFixed(1)}%</span>
                </>
            ),
        },
        {
            icon: <FileText className="size-3.5 text-chart-3" />,
            label: t('totalToken'),
            value: <MetricValue metric={metrics.total_token} />,
            sub: (
                <>
                    <span>↓ {metrics.input_token.formatted.value}{metrics.input_token.formatted.unit}</span>
                    <span>↑ {metrics.output_token.formatted.value}{metrics.output_token.formatted.unit}</span>
                </>
            ),
        },
        {
            icon: <DollarSign className="size-3.5 text-chart-5" />,
            label: t('totalCost'),
            value: <MetricValue metric={metrics.total_cost} />,
            sub: (
                <>
                    <span>↓ {metrics.input_cost.formatted.value}{metrics.input_cost.formatted.unit}</span>
                    <span>↑ {metrics.output_cost.formatted.value}{metrics.output_cost.formatted.unit}</span>
                </>
            ),
        },
        {
            icon: <Clock className="size-3.5 text-primary" />,
            label: t('avgWaitTime'),
            value: <MetricValue metric={metrics.wait_time} />,
        },
    ];

    // 模型级统计: 按当前维度排序并计算占比, 各项格式化已由查询层完成
    const modelStats = useMemo(() => {
        const items = channel.models.map((channelModel) => {
            const { formatted } = channelModel;
            return {
                id: channelModel.model_id,
                name: channelModel.model_name,
                weight: modelSort === 'cost'
                    ? formatted.total_cost.raw
                    : modelSort === 'tokens' ? formatted.total_token.raw : formatted.request_count.raw,
                rate: successRate(formatted.request_success.raw, formatted.request_failed.raw),
                count: formatted.request_count,
                tokens: formatted.total_token,
                cost: formatted.total_cost,
            };
        });

        const total = items.reduce((sum, item) => sum + item.weight, 0);
        return items
            .sort((a, b) => b.weight - a.weight || a.name.localeCompare(b.name))
            .map((item) => ({ ...item, share: total > 0 ? (item.weight / total) * 100 : 0 }));
    }, [channel.models, modelSort]);

    const sortOptions: { key: ModelSortKey; label: string }[] = [
        { key: 'cost', label: t('sortByCost') },
        { key: 'count', label: t('sortByCount') },
        { key: 'tokens', label: t('sortByTokens') },
    ];

    // 把统计区克隆进屏外的固定宽度卡片再截图, 容器查询使分享图不受当前屏幕宽度影响
    const handleShare = async () => {
        if (!contentRef.current) return;

        const stage = document.createElement('div');
        stage.className = 'rounded-3xl bg-card px-4 py-2 text-card-foreground';
        // 宽度与桌面端弹窗卡片一致, 定位到屏外避免闪动
        Object.assign(stage.style, { position: 'fixed', top: '0', left: '-10000px', width: '768px' });
        stage.appendChild(contentRef.current.cloneNode(true));
        document.body.appendChild(stage);

        setIsSharing(true);
        try {
            const blob = await snapdom.toBlob(stage, {
                type: 'png',
                scale: 2,
                embedFonts: true,
                exclude: ['[data-share-exclude]'],
                excludeMode: 'remove',
            });
            setPreview({ url: URL.createObjectURL(blob), blob });
        } catch (error) {
            toast.error(error instanceof Error ? error.message : String(error));
        } finally {
            stage.remove();
            setIsSharing(false);
        }
    };

    const handleCopyImage = async () => {
        if (!preview) return;
        try {
            await navigator.clipboard.write([new ClipboardItem({ 'image/png': preview.blob })]);
            toast.success(tCommon('copy.success'));
        } catch {
            toast.error(tCommon('copy.failed'));
        }
    };

    const handleDownloadImage = () => {
        if (!preview) return;
        const link = document.createElement('a');
        link.href = preview.url;
        link.download = `channel-${channel.channel_id}.png`;
        link.click();
    };

    return (
        // 撑满弹窗给定的高度, 两列各自拉伸填满; 内容更高时(窄屏堆成一列)继续增长, 由外层滚动。
        // 高度不能写成 @2xl/stats:h-full: 容器查询只能由祖先容器回答, 这里自己声明了 @container/stats, 查不到自己。
        // min-h-full 在分享用的屏外容器里取不到确定高度, 退化为 0, 不影响分享图。
        <div ref={contentRef} className="@container/stats cursor-default flex min-h-full flex-col">
            <div className="grid flex-1 gap-4 @2xl/stats:grid-cols-[minmax(0,260px)_minmax(0,1fr)]">
                {/* 左列: 渠道汇总 */}
                <section className="flex min-h-0 flex-col gap-2">
                    {/* min-h-6 由最高的子元素(size-6 图标按钮)决定, 不锁死上限;
                        与右列表头取同一下限, 两列的卡片顶边才对齐。 */}
                    <div className="flex min-h-6 items-center gap-1">
                        <h3 className={`truncate text-xs font-semibold tracking-wider text-muted-foreground ${isNameHidden ? 'select-none blur-[3px]' : ''}`}>
                            {channel.channel_name}
                        </h3>
                        <div data-share-exclude className="flex shrink-0 items-center">
                            <button
                                type="button"
                                onClick={() => setIsNameHidden(!isNameHidden)}
                                aria-pressed={isNameHidden}
                                className="flex size-6 items-center justify-center rounded-md text-muted-foreground/60 transition-colors hover:text-foreground disabled:opacity-50"
                            >
                                {isNameHidden ? <EyeOff className="size-3.5" /> : <Eye className="size-3.5" />}
                            </button>
                            <button type="button" onClick={handleShare} disabled={isSharing} className="flex size-6 items-center justify-center rounded-md text-muted-foreground/60 transition-colors hover:text-foreground disabled:opacity-50">
                                <Share2 className="size-3.5" />
                            </button>
                            {isConfirmingDelete ? (
                                <>
                                    <button
                                        type="button"
                                        onClick={() => setIsConfirmingDelete(false)}
                                        aria-label={t('cancel')}
                                        className="flex size-6 items-center justify-center rounded-md text-muted-foreground/60 transition-colors hover:text-foreground"
                                    >
                                        <X className="size-3.5" />
                                    </button>
                                    {/* 删除成功后该渠道已不存在, 连带关掉弹窗 */}
                                    <button
                                        type="button"
                                        onClick={() => deleteChannel.mutate(channel.channel_id, {
                                            onSuccess: () => setIsOpen(false),
                                        })}
                                        disabled={deleteChannel.isPending}
                                        aria-label={t('confirmDelete')}
                                        className="flex size-6 items-center justify-center rounded-md text-destructive transition-colors hover:text-destructive/70 disabled:opacity-50"
                                    >
                                        <Check className="size-3.5" />
                                    </button>
                                </>
                            ) : (
                                <>
                                    <button
                                        type="button"
                                        onClick={onEdit}
                                        aria-label={t('edit')}
                                        className="flex size-6 items-center justify-center rounded-md text-muted-foreground/60 transition-colors hover:text-foreground"
                                    >
                                        <Pencil className="size-3.5" />
                                    </button>
                                    <button
                                        type="button"
                                        onClick={() => setIsConfirmingDelete(true)}
                                        aria-label={t('delete')}
                                        className="flex size-6 items-center justify-center rounded-md text-muted-foreground/60 transition-colors hover:text-destructive"
                                    >
                                        <Trash2 className="size-3.5" />
                                    </button>
                                </>
                            )}
                        </div>
                    </div>
                    {/* 宽屏下四项各占四分之一列高: 行数与 summary 的项数一致, 等分由 grid-rows-4 表达。 */}
                    <dl className="grid grid-cols-2 gap-2 @md/stats:grid-cols-4 @2xl/stats:flex-1 @2xl/stats:grid-cols-1 @2xl/stats:grid-rows-4">
                        {summary.map(({ icon, label, value, sub }) => (
                            <div key={label} className="flex flex-col rounded-2xl border bg-card p-3">
                                <dt className="flex items-center gap-1.5 text-xs text-muted-foreground">
                                    {icon}
                                    <span className="truncate">{label}</span>
                                </dt>
                                <dd className="mt-auto pt-2 text-right">
                                    <span className="block text-lg font-bold tabular-nums text-card-foreground">{value}</span>
                                    {sub && (
                                        <span className="mt-0.5 flex flex-wrap items-center justify-end gap-x-2 text-[11px] tabular-nums text-muted-foreground">
                                            {sub}
                                        </span>
                                    )}
                                </dd>
                            </div>
                        ))}
                    </dl>
                </section>

                {/* 右列: 模型统计, 仅列出当前维度下的前五名 */}
                <section className="flex min-h-0 flex-col gap-2">
                    {/* 与左列表头取同一下限, 两列的卡片顶边才对齐。 */}
                    <div className="flex min-h-6 items-center justify-between gap-2">
                        <h4 className="flex items-center gap-2 text-xs font-semibold uppercase tracking-wider text-muted-foreground">
                            {t('models')}
                            <span className="tabular-nums">({channel.models.length})</span>
                        </h4>
                        <div className="flex shrink-0 items-center text-xs">
                            {sortOptions.map(({ key, label }, index) => (
                                <Fragment key={key}>
                                    {index > 0 && <span aria-hidden="true" className="mx-1 text-muted-foreground/40">/</span>}
                                    <button
                                        type="button"
                                        onClick={() => setModelSort(key)}
                                        aria-pressed={modelSort === key}
                                        className={`transition-colors ${modelSort === key
                                            ? 'font-medium text-foreground'
                                            : 'text-muted-foreground/50 hover:text-muted-foreground'}`}
                                    >
                                        {label}
                                    </button>
                                </Fragment>
                            ))}
                        </div>
                    </div>

                    {/* 宽屏下五项等分右列剩余高度, 与左列同为 grid 等分; 窄屏按内容取高。
                        行数固定为 5 而不是随模型数变化: 不足 5 个时行高仍按五等分, 两列的卡片高度才一致;
                        超出的模型不显示, 这里是排行摘要而非完整列表。 */}
                    {modelStats.length === 0 ? (
                        <div className="flex items-center justify-center rounded-2xl border bg-card p-6 text-center text-xs text-muted-foreground @2xl/stats:flex-1">
                            {t('noModels')}
                        </div>
                    ) : (
                        <ul className="grid gap-2 @2xl/stats:flex-1 @2xl/stats:grid-rows-5">
                            {modelStats.slice(0, 5).map((model) => (
                                <li
                                    key={model.id}
                                    className="grid gap-2 rounded-2xl border bg-card p-3 @md/stats:grid-cols-[minmax(0,1fr)_auto] @md/stats:items-center @md/stats:gap-4"
                                >
                                    <div className="min-w-0 space-y-1.5">
                                        <span className="block truncate text-sm font-medium text-card-foreground">{model.name}</span>
                                        <div className="flex items-center gap-2">
                                            <div className="h-1.5 flex-1 overflow-hidden rounded-full bg-muted">
                                                <div className="h-full rounded-full bg-primary" style={{ width: `${model.share}%` }} />
                                            </div>
                                            <span className="w-8 shrink-0 text-right text-[10px] tabular-nums text-muted-foreground">
                                                {model.share.toFixed(0)}%
                                            </span>
                                        </div>
                                    </div>

                                    <div className="grid grid-cols-2 gap-x-3 gap-y-1 text-xs tabular-nums @md/stats:w-64 @md/stats:grid-cols-4 @md/stats:gap-x-2">
                                        <div className="flex items-center gap-1">
                                            <MessageSquare className="size-3.5 shrink-0 text-chart-1" />
                                            <MetricValue metric={model.count} />
                                        </div>
                                        <div className="flex items-center gap-1">
                                            <CheckCircle2 className="size-3.5 shrink-0 text-accent" />
                                            <span>{model.rate.toFixed(0)}%</span>
                                        </div>
                                        <div className="flex items-center gap-1">
                                            <FileText className="size-3.5 shrink-0 text-chart-3" />
                                            <MetricValue metric={model.tokens} />
                                        </div>
                                        <div className="flex items-center gap-1">
                                            <DollarSign className="size-3.5 shrink-0 text-chart-5" />
                                            <MetricValue metric={model.cost} />
                                        </div>
                                    </div>
                                </li>
                            ))}
                        </ul>
                    )}
                </section>
            </div>

            {/* 分享图预览: 覆盖整张弹窗卡片 */}
            {preview && (
                <div className="absolute inset-0 z-30 flex flex-col items-center justify-center gap-3 rounded-3xl bg-card/95 p-4 backdrop-blur-sm">
                    <img
                        src={preview.url}
                        alt=""
                        className="max-h-[70vh] min-h-0 w-auto max-w-full rounded-2xl border border-border object-contain"
                    />
                    <div className="flex shrink-0 items-center gap-2">
                        <button type="button" onClick={handleCopyImage} className="flex size-10 items-center justify-center rounded-xl border border-border bg-card text-muted-foreground transition-colors hover:text-foreground active:scale-95">
                            <Copy className="size-4" />
                        </button>
                        <button type="button" onClick={handleDownloadImage} className="flex size-10 items-center justify-center rounded-xl border border-border bg-card text-muted-foreground transition-colors hover:text-foreground active:scale-95">
                            <Download className="size-4" />
                        </button>
                        <button type="button" onClick={() => setPreview(null)} className="flex size-10 items-center justify-center rounded-xl border border-border bg-card text-muted-foreground transition-colors hover:text-foreground active:scale-95">
                            <X className="size-4" />
                        </button>
                    </div>
                </div>
            )}
        </div>
    );
}
