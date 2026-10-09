import { memo, useEffect, useMemo, useState, type CSSProperties } from 'react';
import { AlertCircle, ArrowDownToLine, ArrowRight, ArrowUpFromLine, Brain, Clock, Cpu, Database, DollarSign, Gauge, KeyRound, Loader2, Square, Zap, Percent } from 'lucide-react';
import { useTranslations } from 'use-intl';
import JsonView from '@uiw/react-json-view';
import { githubDarkTheme } from '@uiw/react-json-view/githubDark';
import { githubLightTheme } from '@uiw/react-json-view/githubLight';
import { useTheme } from '@/provider/theme';
import { type RelayLogOverview, getRetryErrors, useLogRequestBody, useLogResponseBody, useStopRequest } from '@/api/log';
import { useGroup, useUpdateGroup } from '@/api/group';
import { Protocol } from '@/api/channel';
import { getModelIcon } from '@/lib/model-icons';
import { Badge } from '@/components/ui/badge';
import { Tabs, TabsList, TabsTrigger } from '@/components/ui/tabs';
import { cn } from '@/lib/utils';
import { toast } from 'sonner';
import { MemberStatus } from '@/components/modules/group/MemberStatus';
import { RetryHistory, RetryHistoryPending } from '@/components/modules/log/RetryHistory';
import {
    MorphingDialog,
    MorphingDialogTrigger,
    MorphingDialogContainer,
    MorphingDialogContent,
    MorphingDialogClose,
    MorphingDialogTitle,
    MorphingDialogDescription,
    useMorphingDialog,
} from '@/components/ui/morphing-dialog';

// formatTime 将后端 RFC3339 时间转换为本地时分秒。
function formatTime(value: string) {
    const date = new Date(value);
    if (Number.isNaN(date.getTime()) || date.getUTCFullYear() === 1) return '--';
    return date.toLocaleTimeString(undefined, {
        hour: '2-digit',
        minute: '2-digit',
        second: '2-digit',
        hour12: false,
    });
}

// formatMilliseconds 将毫秒转换为以秒为单位的耗时文本。
function formatMilliseconds(value: number) {
    return `${(Math.max(0, value) / 1000).toFixed(2)}s`;
}

// formatRoundStartedAt 将服务端轮次开始时间格式化为本地时分秒.毫秒, 各部分固定补零。
function formatRoundStartedAt(value: string) {
    const date = new Date(value);
    if (Number.isNaN(date.getTime()) || date.getUTCFullYear() === 1) return '--:--:--.---';
    return `${date.toLocaleTimeString(undefined, {
        hour: '2-digit',
        minute: '2-digit',
        second: '2-digit',
        hour12: false,
    })}.${String(date.getMilliseconds()).padStart(3, '0')}`;
}

// PROTOCOL_LABELS 是协议位值对应的界面标识, 与渠道页和分组页的授权标签同一套词。
// 键是单个协议位而非掩码组合: 日志记录的是本次请求与本轮上游各自实际使用的那一个协议。
const PROTOCOL_LABELS: Record<number, string> = {
    [Protocol.OpenAIChatCompletion]: 'Chat',
    [Protocol.OpenAIResponse]: 'Response',
    [Protocol.AnthropicMessage]: 'Message',
};

// LogMetrics 渲染时间、API Key、耗时、费用和 Token 指标; card 变体用于卡片栅格, footer 变体用于弹窗底部。
function LogMetrics({ log, now, brandColor, variant }: { log: RelayLogOverview; now: number; brandColor: string; variant: 'card' | 'footer' }) {
    const cachedTokens = log.usage.prompt_tokens_details?.cached_tokens ?? 0;
    // 缓存率取输入缓存占全部输入 Token 的比例, 无输入时为零。
    const cacheRate = log.usage.prompt_tokens > 0 ? Math.round((cachedTokens / log.usage.prompt_tokens) * 100) : 0;
    // 总耗时保持端到端口径: 请求到达至响应结束, 进行中按当前时刻实时推算。
    const requestActive = log.status === 'running' || log.status === 'committed';
    const elapsedMs = requestActive
        ? now - new Date(log.started_at).getTime()
        : log.duration / 1_000_000;
    const duration = formatMilliseconds(elapsedMs);
    // 响应阶段耗时(首字节至结束)只用于折算速度, 不再充当「总耗时」。
    const responseMs = (log.stream_duration || log.response_duration) / 1_000_000;
    // 首字耗时从请求到达算起(含选路与重试), 非流式同样记录; 未提交前为零, 显示占位而不是形似真实的 0ms。
    const firstToken = log.first_token_duration > 0 ? formatMilliseconds(log.first_token_duration / 1_000_000) : '-';
    // 请求进行中使用同一份服务端快照中的字符数和流式传输时长, 结束后改用最终 Token 数。
    const outputCount = requestActive ? log.output_chars : log.usage.completion_tokens;
    const outputSpeed = responseMs > 0 ? outputCount / (responseMs / 1000) : 0;
    const outputSpeedUnit = requestActive ? 'c/s' : 't/s';
    const metrics = [
        { key: 'time', Icon: Clock, iconClassName: 'size-3.5 shrink-0', iconStyle: { color: brandColor } as CSSProperties, value: formatTime(log.started_at), cellClassName: 'whitespace-nowrap col-span-5 md:col-span-1' },
        { key: 'apiKey', Icon: KeyRound, iconClassName: 'size-3.5 shrink-0 text-orange-500', value: log.api_key_name || '-', cellClassName: 'whitespace-nowrap col-span-5 md:col-span-1' },
        { key: 'firstToken', Icon: Zap, iconClassName: 'size-3.5 shrink-0 text-amber-500', value: firstToken, cellClassName: 'whitespace-nowrap col-span-5 md:col-span-1' },
        { key: 'duration', Icon: Cpu, iconClassName: 'size-3.5 shrink-0 text-blue-500', value: duration, cellClassName: 'whitespace-nowrap col-span-5 md:col-span-1' },
        { key: 'prompt', Icon: ArrowDownToLine, iconClassName: 'size-3.5 shrink-0 text-green-500', value: (log.usage.prompt_tokens - cachedTokens).toLocaleString(), cellClassName: 'whitespace-nowrap col-span-4 md:col-span-1' },
        { key: 'cached', Icon: Database, iconClassName: 'size-3.5 shrink-0 text-cyan-500', value: `${cachedTokens.toLocaleString()}`, cellClassName: 'whitespace-nowrap col-span-4 md:col-span-1' },
        { key: 'cacheRate', Icon: Percent, iconClassName: 'size-3.5 shrink-0 text-teal-500', value: `${cacheRate}%`, valueClassName: 'tabular-nums', cellClassName: 'col-span-4 md:col-span-1' },
        { key: 'completion', Icon: ArrowUpFromLine, iconClassName: 'size-3.5 shrink-0 text-purple-500', value: (requestActive ? log.output_chars.toLocaleString() : log.usage.completion_tokens.toLocaleString()), cellClassName: 'col-span-4 md:col-span-1' },
        { key: 'cacheWrite', Icon: Database, iconClassName: 'size-3.5 shrink-0 text-orange-500', value: (log.usage.prompt_tokens_details?.write_cached_tokens ?? 0).toLocaleString(), cellClassName: 'whitespace-nowrap col-span-4 md:col-span-1' },
        { key: 'speed', Icon: Gauge, iconClassName: 'size-3.5 shrink-0 text-sky-500', value: outputSpeed > 0 ? `${outputSpeed.toFixed(0)}${outputSpeedUnit}` : '-', cellClassName: 'whitespace-nowrap col-span-4 md:col-span-1' },
        { key: 'cost', Icon: DollarSign, iconClassName: 'size-3.5 shrink-0 text-emerald-500', value: log.cost.toFixed(6), cellClassName: 'whitespace-nowrap col-span-4 md:col-span-1' },
    ];

    return metrics.map((metric) => (
        <div
            key={metric.key}
            title={metric.key === 'apiKey' ? log.api_key_name : undefined}
            className={cn('flex min-w-0 items-center gap-1.5', variant === 'card' && metric.cellClassName)}
        >
            <metric.Icon className={metric.iconClassName} style={metric.iconStyle} />
            <span>{metric.value}</span>
        </div>
    ));
}

// JsonContent 渲染请求或响应正文, 能解析为 JSON 时使用折叠视图, 否则按纯文本展示。
function JsonContent({ content, fallbackText }: { content: string | object | undefined; fallbackText: string }) {
    const { resolvedTheme } = useTheme();

    const parsed = useMemo(() => {
        if (content === undefined || content === '') return null;
        if (typeof content !== 'string') return { isJson: true, data: content };
        try {
            return { isJson: true, data: JSON.parse(content) as object };
        } catch {
            return { isJson: false, data: content };
        }
    }, [content]);

    if (!parsed) {
        return (
            <pre className="p-4 text-xs text-muted-foreground whitespace-pre-wrap wrap-break-word leading-relaxed">
                {fallbackText}
            </pre>
        );
    }

    if (!parsed.isJson) {
        return (
            <pre className="p-4 text-xs text-muted-foreground whitespace-pre-wrap wrap-break-word font-mono leading-relaxed animate-in fade-in duration-200">
                {parsed.data as string}
            </pre>
        );
    }

    return (
        <div className="p-4 animate-in fade-in duration-200">
            <JsonView
                value={parsed.data as object}
                style={{
                    ...(resolvedTheme === 'dark' ? githubDarkTheme : githubLightTheme),
                    fontSize: '12px',
                    fontFamily: 'ui-monospace, SFMono-Regular, "SF Mono", Menlo, Consolas, monospace',
                    backgroundColor: 'transparent',
                }}
                displayDataTypes={false}
                displayObjectSize={false}
                collapsed={false}
            />
        </div>
    );
}

// LogDetail 渲染日志详情弹窗内容, 仅在弹窗打开期间挂载, 由此避免列表中的卡片持有详情查询和状态。
function LogDetail({ log, now }: { log: RelayLogOverview; now: number }) {
    const t = useTranslations('log.card');
    const statusT = useTranslations('log.status');
    const [leftTab, setLeftTab] = useState<'request' | 'group'>('group');
    const [detailReady, setDetailReady] = useState(false); // 展开动画结束后才允许加载详情数据。
    const [switchingItemId, setSwitchingItemId] = useState<number | null>(null);
    const requestBody = useLogRequestBody(log.id, log.started_at, detailReady && leftTab === 'request');
    const responseBody = useLogResponseBody(log.id, log.started_at, detailReady && log.status === 'success');
    const { data: activeGroup } = useGroup(log.group_id, detailReady, detailReady);
    const updateActiveItem = useUpdateGroup();
    const stopRequest = useStopRequest();
    const actualModel = log.target_model || log.model;
    const { Icon, className: iconClassName, color: brandColor } = getModelIcon(actualModel);
    const errorText = log.error ?? '';
    const requestFailed = log.status === 'failed' || log.status === 'canceled';
    const responseCommitted = log.status === 'committed';
    const requestActive = log.status === 'running' || responseCommitted;
    // retryErrors 是本次请求全部失败轮次的服务端历史, 重开详情或进入下一轮都不会丢。
    const retryErrors = getRetryErrors(log);
    // showRounds 只在请求进行中为真: 那时面板主体展示正在等待上游的当前轮次,
    // 已结束的失败轮次统一交给面板底部的失败历史, 避免同一批轮次显示两遍。
    const showRounds = log.status === 'running';
    const isWaitingForSelection = log.status === 'running' && !log.sending && activeGroup?.mode === 'manual' && activeGroup.runtime.current_item_id === 0; // isWaitingForSelection 表示手动模式请求正等待选择渠道。

    // 让弹窗先完成展开动画, 避免详情请求及其状态更新占用动画起步帧。
    useEffect(() => {
        const timer = window.setTimeout(() => setDetailReady(true), 600);
        return () => window.clearTimeout(timer);
    }, []);

    return (
        <MorphingDialogContent className="relative w-[calc(100vw-2rem)] md:w-[80vw] bg-card text-card-foreground px-6 py-4 rounded-3xl h-[calc(100vh-2rem)] flex flex-col overflow-hidden">
            <MorphingDialogClose className="top-4 right-5 text-muted-foreground hover:text-foreground transition-colors" />
            <MorphingDialogTitle className="flex flex-wrap items-center gap-2 mb-3 text-sm">
                <span className="flex min-w-0 items-center gap-2 w-full md:w-auto">
                    <Icon aria-hidden="true" className={cn('hidden shrink-0 md:block', iconClassName)} width={28} height={28} />
                    <span className="shrink-0 text-xs text-muted-foreground/70"><span className="md:hidden">{PROTOCOL_LABELS[log.protocol]?.charAt(0) ?? '-'}</span><span className="hidden md:inline">{PROTOCOL_LABELS[log.protocol] ?? '-'}</span></span>
                    <span className="min-w-0 truncate font-semibold text-card-foreground">{log.model || t('unknownModel')}</span>
                    {log.reasoning_effort && (
                        <Badge variant="outline" className="max-w-32 bg-violet-500/10 px-1.5 py-0 text-xs text-violet-700 dark:text-violet-300">
                            <Brain aria-hidden="true" />
                            <span className="truncate">{log.reasoning_effort}</span>
                        </Badge>
                    )}
                    {log.status === 'running' || responseCommitted
                        ? <Loader2 className={cn('size-3.5 animate-spin', log.status === 'committed' ? 'text-green-500' : log.round > 1 ? 'text-red-500' : 'text-muted-foreground/50')} />
                        : <ArrowRight className="size-3.5 text-muted-foreground/50" />}
                </span>
                <span className="flex items-center gap-2 w-full md:w-auto">
                    <span className="text-xs text-muted-foreground/70"><span className="md:hidden">{PROTOCOL_LABELS[log.target_protocol]?.charAt(0) ?? '-'}</span><span className="hidden md:inline">{PROTOCOL_LABELS[log.target_protocol] ?? '-'}</span></span>
                    <Badge
                        variant="secondary"
                        className="text-xs px-1.5 py-0"
                        style={{ backgroundColor: `${brandColor}15`, color: brandColor }}
                    >
                        {log.target_channel_key || '-'}
                    </Badge>
                    <span className="text-muted-foreground">{actualModel}</span>
                </span>
            </MorphingDialogTitle>

            <MorphingDialogDescription className="flex-1 min-h-0">
                <div className="grid grid-cols-1 md:grid-cols-2 gap-4 h-full min-h-0">
                    <div className="flex flex-col rounded-2xl border border-border bg-muted/30 overflow-hidden min-h-0">
                        <div className="flex h-10 shrink-0 items-center gap-2 border-b border-border bg-muted/50 pl-1 pr-3 md:pr-4">
                            <Tabs value={leftTab} onValueChange={(value) => setLeftTab(value as 'request' | 'group')}>
                                <TabsList variant="text" className="p-0">
                                    <TabsTrigger value="group" className="pr-0">
                                        {t('group')}
                                    </TabsTrigger>
                                    <span aria-hidden="true" className="mx-1 inline-flex h-full -translate-y-px items-center text-sm font-medium leading-none text-muted-foreground/50">/</span>
                                    <TabsTrigger value="request" className="pl-0">
                                        {t('requestContent')}
                                    </TabsTrigger>
                                </TabsList>
                            </Tabs>
                            {leftTab === 'request' && (
                                <Badge variant="secondary" className="ml-auto text-xs">
                                    {(log.usage.prompt_tokens - (log.usage.prompt_tokens_details?.cached_tokens ?? 0)).toLocaleString()} {t('tokens')}
                                </Badge>
                            )}
                        </div>
                        <div className="flex-1 overflow-auto min-h-0">
                            {!detailReady ? (
                                <div className="flex h-full items-center justify-center">
                                    <Loader2 className="size-5 animate-spin text-muted-foreground" />
                                </div>
                            ) : leftTab === 'request' ? (
                                requestBody.isLoading ? (
                                    <div className="flex h-full items-center justify-center">
                                        <Loader2 className="size-5 animate-spin text-muted-foreground" />
                                    </div>
                                ) : requestBody.error ? (
                                    <div className="flex h-full flex-col items-center justify-center gap-2 px-4 text-xs text-destructive">
                                        <AlertCircle className="size-5" />
                                        <span>{t('detailUnavailable')}</span>
                                    </div>
                                ) : (
                                    <JsonContent content={requestBody.data} fallbackText={t('noRequestContent')} />
                                )
                            ) : !activeGroup ? (
                                <div className="flex h-full items-center justify-center px-4 text-xs text-muted-foreground">
                                    {t('groupUnavailable')}
                                </div>
                            ) : !activeGroup.items.length ? (
                                <div className="flex h-full items-center justify-center px-4 text-xs text-muted-foreground">
                                    {t('noGroupItems')}
                                </div>
                            ) : (
                                <div className="divide-y divide-border">
                                    {activeGroup.items.map((item) => {
                                        const { Icon: ItemIcon, className: itemIconClassName } = getModelIcon(item.model_name);
                                        const itemCurrent = switchingItemId !== null
                                            ? item.id === switchingItemId
                                            : activeGroup.runtime.current_item_id === item.id;
                                        return (
                                            <button
                                                key={item.id}
                                                type="button"
                                                aria-pressed={itemCurrent}
                                                disabled={activeGroup.mode === 'failover' || switchingItemId !== null || stopRequest.isPending}
                                                onClick={async () => {
                                                    if (activeGroup.mode === 'failover') return;
                                                    setSwitchingItemId(item.id);
                                                    const isCurrent = activeGroup.runtime.current_item_id === item.id;
                                                    try {
                                                        await updateActiveItem.mutateAsync({ id: activeGroup.id, active_item_id: isCurrent ? 0 : item.id });
                                                        if (log.sending) {
                                                            await stopRequest.mutateAsync({ requestId: log.id, round: log.round });
                                                        }
                                                        toast.success(isCurrent ? t('channelCleared') : t('channelChanged'));
                                                    } catch (cause) {
                                                        toast.error(t('channelChangeFailed'), { description: cause instanceof Error ? cause.message : undefined });
                                                    } finally {
                                                        setSwitchingItemId(null);
                                                    }
                                                }}
                                                className="flex w-full items-center gap-2.5 rounded-lg px-3 py-2.5 text-left text-xs transition-colors hover:bg-muted/50 disabled:cursor-default disabled:hover:bg-transparent"
                                            >
                                                <ItemIcon aria-hidden="true" className={itemIconClassName} width={20} height={20} />
                                                <span className="min-w-0 flex-1">
                                                    <span className="block truncate font-semibold text-foreground">
                                                        {item.model_name}
                                                    </span>
                                                    <span className="block truncate text-[11px] text-muted-foreground">
                                                        {item.key_name ? `${item.channel_name} · ${item.key_name}` : item.channel_name}
                                                    </span>
                                                </span>
                                                <MemberStatus group={activeGroup} itemId={item.id} now={now} active={itemCurrent} />
                                            </button>
                                        );
                                    })}
                                </div>
                            )}
                        </div>
                    </div>

                    <div className="flex flex-col rounded-2xl border border-border bg-muted/30 overflow-hidden min-h-0">
                        <div className="flex h-10 shrink-0 items-center gap-2 border-b border-border bg-muted/50 px-3 md:px-4">
                            <span className="text-sm font-medium text-card-foreground">
                                {isWaitingForSelection ? t('waitingChannelSelection') : showRounds ? t('retryDetails') : requestFailed ? t('errorInfo') : t('responseContent')}
                            </span>
                            <div className="ml-auto flex items-center gap-2">
                                {log.status === 'running' && log.sending && activeGroup?.mode === 'manual' && (
                                    <button
                                        type="button"
                                        disabled={stopRequest.isPending}
                                        onClick={async () => {
                                            try {
                                                await stopRequest.mutateAsync({ requestId: log.id, round: log.round });
                                            } catch (cause) {
                                                toast.error(t('stopFailed'), { description: cause instanceof Error ? cause.message : undefined });
                                            }
                                        }}
                                        className="flex items-center gap-1.5 rounded-md px-2 py-1 text-xs text-destructive transition-colors hover:bg-destructive/10 disabled:opacity-50"
                                    >
                                        {stopRequest.isPending ? <Loader2 className="size-3.5 animate-spin" /> : <Square className="size-3.5" />}
                                        {t('stopRound')}
                                    </button>
                                )}
                                {requestActive && (
                                    <button
                                        type="button"
                                        disabled={stopRequest.isPending}
                                        onClick={async () => {
                                            try {
                                                await stopRequest.mutateAsync({ requestId: log.id });
                                            } catch (cause) {
                                                toast.error(t('cancelFailed'), { description: cause instanceof Error ? cause.message : undefined });
                                            }
                                        }}
                                        className="flex items-center gap-1.5 rounded-md px-2 py-1 text-xs text-destructive transition-colors hover:bg-destructive/10 disabled:opacity-50"
                                    >
                                        {stopRequest.isPending ? <Loader2 className="size-3.5 animate-spin" /> : <Square className="size-3.5" />}
                                        {t('cancelRequest')}
                                    </button>
                                )}
                                {!requestFailed && !(log.status === 'running' && log.sending && activeGroup?.mode === 'manual') && (
                                    <Badge variant="secondary" className="text-xs">
                                        {responseCommitted
                                            ? statusT('committed')
                                            : `${log.usage.completion_tokens.toLocaleString()} ${t('tokens')}`}
                                    </Badge>
                                )}
                            </div>
                        </div>
                        <div className="min-h-0 flex-1 overflow-auto">
                            {!detailReady ? (
                                <div className="flex h-full items-center justify-center">
                                    <Loader2 className="size-5 animate-spin text-muted-foreground" />
                                </div>
                            ) : isWaitingForSelection ? (
                                <div className="flex h-full items-center justify-center gap-2 text-xs text-muted-foreground">
                                    <Loader2 className="size-4 animate-spin" />
                                    {t('waitingChannelSelection')}
                                </div>
                            ) : showRounds ? (
                                <div className="divide-y divide-border">
                                    <RetryHistoryPending
                                        round={log.round}
                                        channelKey={log.target_channel_key}
                                        startedAt={formatRoundStartedAt(log.round_started_at)}
                                        sending={log.sending}
                                        error={errorText}
                                    />
                                </div>
                            ) : responseCommitted ? (
                                <div className="flex h-full items-center justify-center gap-2 text-xs text-muted-foreground">
                                    <Loader2 className="size-4 animate-spin" />
                                    {t('responseStreaming')}
                                </div>
                            ) : requestFailed ? (
                                <JsonContent content={errorText} fallbackText={t('noResponseContent')} />
                            ) : responseBody.isLoading ? (
                                <div className="flex h-full items-center justify-center">
                                    <Loader2 className="size-5 animate-spin text-muted-foreground" />
                                </div>
                            ) : responseBody.error ? (
                                <div className="flex h-full flex-col items-center justify-center gap-2 px-4 text-xs text-destructive">
                                    <AlertCircle className="size-5" />
                                    <span>{t('detailUnavailable')}</span>
                                </div>
                            ) : (
                                <JsonContent content={responseBody.data} fallbackText={t('noResponseContent')} />
                            )}
                        </div>
                        <div className="max-h-40 shrink-0 overflow-auto">
                            <RetryHistory errors={retryErrors} active={log.status === 'running'} />
                        </div>
                    </div>
                </div>
            </MorphingDialogDescription>

            <div className="flex w-full shrink-0 flex-wrap items-center gap-3 pt-4 mt-auto text-xs text-muted-foreground md:gap-4">
                <LogMetrics log={log} now={now} brandColor={brandColor} variant="footer" />
            </div>
        </MorphingDialogContent>
    );
}

// LogCardBody 渲染日志概览卡片, 并在弹窗打开时挂载详情面板。
function LogCardBody({ log }: { log: RelayLogOverview }) {
    const t = useTranslations('log.card');
    const { isOpen } = useMorphingDialog();
    const [now, setNow] = useState(() => Date.now());
    const [displayError, setDisplayError] = useState(log.error ?? ''); // 保留重试期间最近一次错误, 直到响应真正开始。
    const actualModel = log.target_model || log.model;
    const { Icon, className: iconClassName, color: brandColor } = getModelIcon(actualModel);
    const requestRunning = log.status === 'running' || log.status === 'committed';
    const errorText = log.error ?? '';
    const visibleError = log.status === 'committed' || log.status === 'success' ? '' : displayError;
    // retryErrors 与详情面板取自同一份服务端历史, 所以卡片上的入口数与详情内容永远一致。
    const retryErrors = getRetryErrors(log);

    // 仅在请求进行中或弹窗打开时按 500ms 刷新, 避免已完成日志持续触发重渲染。
    useEffect(() => {
        if (!requestRunning && !isOpen) return;
        const timer = window.setInterval(() => setNow(Date.now()), 500);
        return () => window.clearInterval(timer);
    }, [isOpen, requestRunning]);

    // 新错误覆盖旧错误; 响应已开始或请求成功后清除错误提示。
    useEffect(() => {
        if (log.status === 'committed' || log.status === 'success') {
            setDisplayError('');
        } else if (errorText) {
            setDisplayError(errorText);
        }
    }, [errorText, log.status]);

    return (
        <>
            <MorphingDialogTrigger
                className="rounded-3xl border border-border bg-card w-full text-left"
            >
                <div className="p-4 grid grid-cols-[auto_1fr] gap-4 items-start">
                    <Icon aria-hidden="true" className={cn('hidden md:block', iconClassName)} width={40} height={40} />
                    <div className="min-w-0 flex flex-col gap-3 col-span-2 md:col-span-1">
                        <div className="flex flex-wrap items-center gap-x-2 gap-y-1 min-w-0 text-sm">
                            <span className="flex w-full min-w-0 items-center gap-2 md:w-auto">
                                <span className="shrink-0 text-xs text-muted-foreground/70"><span className="md:hidden">{PROTOCOL_LABELS[log.protocol]?.charAt(0) ?? '-'}</span><span className="hidden md:inline">{PROTOCOL_LABELS[log.protocol] ?? '-'}</span></span>
                                <span className="font-semibold text-card-foreground truncate">
                                    {log.model || t('unknownModel')}
                                </span>
                                {log.reasoning_effort && (
                                    <Badge variant="secondary" className="max-w-32 bg-violet-500/10 px-1.5 py-0 text-xs text-violet-700 dark:text-violet-300">
                                        <Brain aria-hidden="true" />
                                        <span className="truncate">{log.reasoning_effort}</span>
                                    </Badge>
                                )}
                                {requestRunning
                                    ? <Loader2 className={cn('size-3.5 shrink-0 animate-spin', log.status === 'committed' ? 'text-green-500' : log.round > 1 ? 'text-red-500' : 'text-muted-foreground/50')} />
                                    : <ArrowRight className="size-3.5 shrink-0 text-muted-foreground/50" />}
                            </span>
                            <span className="flex w-full min-w-0 items-center gap-2 md:w-auto">
                                <span className="shrink-0 text-xs text-muted-foreground/70"><span className="md:hidden">{PROTOCOL_LABELS[log.target_protocol]?.charAt(0) ?? '-'}</span><span className="hidden md:inline">{PROTOCOL_LABELS[log.target_protocol] ?? '-'}</span></span>
                                <Badge
                                    variant="secondary"
                                    className="shrink-0 text-xs px-1.5 py-0"
                                    style={{ backgroundColor: `${brandColor}15`, color: brandColor }}
                                >
                                    {log.target_channel_key || '-'}
                                </Badge>
                                <span className="text-muted-foreground truncate">
                                    {actualModel}
                                </span>
                            </span>
                        </div>
                        <div className="grid grid-cols-20 gap-x-4 gap-y-2 text-xs tabular-nums text-muted-foreground md:grid-cols-11">
                            <LogMetrics log={log} now={now} brandColor={brandColor} variant="card" />
                        </div>
                        {visibleError && (
                            <div className="p-2.5 rounded-xl bg-destructive/10 border border-destructive/20 overflow-hidden">
                                <p className="text-xs text-destructive line-clamp-1 whitespace-pre-line">{log.status === 'running' ? `${t('retryIndex', { index: log.round })}: ` : ''}{visibleError}</p>
                            </div>
                        )}
                        {log.status !== 'running' && retryErrors.length > 0 && (
                            <p className="text-xs text-muted-foreground">{t('retryHistory', { count: retryErrors.length })}</p>
                        )}
                    </div>
                </div>
            </MorphingDialogTrigger>

            <MorphingDialogContainer>
                <LogDetail log={log} now={now} />
            </MorphingDialogContainer>
        </>
    );
}

// LogCard 展示一条日志概览, 并在弹窗打开时加载详情。
export const LogCard = memo(function LogCard({ log }: { log: RelayLogOverview }) {
    return (
        <MorphingDialog>
            <LogCardBody log={log} />
        </MorphingDialog>
    );
});
