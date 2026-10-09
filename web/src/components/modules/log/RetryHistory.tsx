import { Loader2 } from 'lucide-react';
import { useTranslations } from 'use-intl';
import { type RelayRetryError } from '@/api/log';
import { CopyIconButton } from '@/components/common/CopyButton';

// RetryHistory 展示本次请求的全部失败轮次, 数据来自服务端持久化的历史,
// 因此重开详情、进入下一轮或新一轮清空 error 都不会丢失。
// 传入的 errors 已按轮次倒序, 最新的失败排在最前。
export function RetryHistory({ errors, active }: { errors: RelayRetryError[]; active: boolean }) {
    const t = useTranslations('log.card');
    if (!errors.length) return null;

    return (
        <details open={active} className="border-t border-border text-xs">
            <summary className="cursor-pointer px-4 py-3 font-medium text-muted-foreground">
                {t('retryHistory', { count: errors.length })}
            </summary>
            <div className="divide-y divide-border">
                {errors.map((entry) => (
                    <div key={entry.round} className="flex flex-col gap-1.5 px-4 py-2.5">
                        <div className="flex flex-wrap items-center gap-2 text-muted-foreground">
                            <span className="shrink-0 tabular-nums">{t('retryIndex', { index: entry.round })}</span>
                            <span className="font-semibold text-foreground">{entry.target_channel_key || '-'}</span>
                            <span className="break-all">{entry.target_model}</span>
                            <CopyIconButton
                                text={entry.error}
                                className="ml-auto shrink-0 p-1 rounded-md hover:bg-muted"
                                copyIconClassName="size-3.5"
                                checkIconClassName="size-3.5"
                            />
                        </div>
                        <div className="leading-relaxed text-muted-foreground whitespace-pre-wrap wrap-break-word">
                            {entry.error}
                        </div>
                    </div>
                ))}
            </div>
        </details>
    );
}

// RetryHistoryPending 渲染正在进行中的轮次, 用于让详情面板在等待上游时仍然有内容可看。
export function RetryHistoryPending({ round, channelKey, startedAt, sending, error }: { round: number; channelKey: string; startedAt: string; sending: boolean; error: string }) {
    const t = useTranslations('log.card');
    return (
        <div className="flex flex-col gap-1.5 px-3 py-2.5 text-xs">
            <div className="flex items-center gap-2">
                <span className="shrink-0 tabular-nums text-muted-foreground">{startedAt}</span>
                <span className="shrink-0 text-muted-foreground">{t('retryIndex', { index: round })}</span>
                <span className="shrink-0 font-semibold text-foreground">{channelKey || '-'}</span>
                <Loader2 className="ml-auto size-3.5 animate-spin text-muted-foreground" />
            </div>
            {error ? (
                <div className="text-[11px] leading-relaxed text-destructive/90 whitespace-pre-wrap wrap-break-word">{error}</div>
            ) : (
                <div className="flex items-center gap-2 text-[11px] text-muted-foreground">
                    <Loader2 className="size-3.5 animate-spin" />
                    {sending ? t('waitingResponse') : t('waitingChannelSelection')}
                </div>
            )}
        </div>
    );
}
