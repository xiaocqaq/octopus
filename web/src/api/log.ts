import { useMutation, useQuery } from '@tanstack/react-query';
import { useEffect, useState } from 'react';
import { apiRequest } from './client';

// RequestState 表示 Relay 请求的实时状态。
export type RequestState = 'running' | 'committed' | 'success' | 'failed' | 'canceled';

// RelayUsage 保存请求结束后确认的统一 Token 用量。
export interface RelayUsage {
    prompt_tokens: number;
    completion_tokens: number;
    total_tokens: number;
    prompt_tokens_details: {
        cached_tokens: number;
        write_cached_tokens?: number;
    } | null;
}

// RelayRetryError 保留失败轮次, 不受下一轮清空 error 或详情弹窗生命周期影响。
export interface RelayRetryError {
    round: number;
    target_channel: string;
    target_model: string;
    error: string;
}

// RelayLogOverview 是请求状态流发送的完整进程内请求状态。
export interface RelayLogOverview {
    id: number;
    status: RequestState;
    started_at: string;
    duration: number;
    // first_token_duration 是首字耗时（纳秒）：从请求到达到第一个字节写出客户端，含此前的选路与重试。
    // 未提交前为零；非流式请求与 duration 相同。
    first_token_duration: number;
    stream_duration: number;
    response_duration: number;
    model: string;
    protocol: number;
    group_id: number;
    api_key_name: string;
    usage: RelayUsage;
    cost: number;
    output_chars: number;
    round: number;
    round_started_at: string;
    target_channel: string;
    target_model: string;
    target_protocol: number;
    sending: boolean;
    error?: string;
    retry_errors?: RelayRetryError[];
    // reasoning 是客户端声明的思维强度, 后端已把三种协议各自的字段归一为一段短文本; 未声明时缺省。
    reasoning?: string;
}

// getRetryErrors 以服务端历史为准, 兼容正在失败的当前轮快照; 最终错误始终单独展示。
export function getRetryErrors(log: RelayLogOverview): RelayRetryError[] {
    const rounds = new Map((log.retry_errors ?? []).map((entry) => [entry.round, entry]));
    if (log.status === 'running' && log.round > 0 && log.error && !rounds.has(log.round)) {
        rounds.set(log.round, {
            round: log.round,
            target_channel: log.target_channel,
            target_model: log.target_model,
            error: log.error,
        });
    }
    return [...rounds.values()].sort((a, b) => b.round - a.round);
}

// useClearLogs 清空已完成的内存日志。
export function useClearLogs() {
    return useMutation({
        mutationFn: () => apiRequest<null>('/api/v1/log/clear', { method: 'DELETE' }),
    });
}

// useStopRequest 按是否提供轮次参数, 中止单个轮次或整个请求。
export function useStopRequest() {
    return useMutation({
        mutationFn: ({ requestId, round }: { requestId: number; round?: number }) =>
            apiRequest<null>(`/api/v1/log/stop/${requestId}${round === undefined ? '' : `/${round}`}`, { method: 'POST' }),
    });
}

// useLogs 订阅进程内日志概览，并按 RequestID 更新同一条记录。
export function useLogs() {
    const [logs, setLogs] = useState<RelayLogOverview[]>([]);
    const [isLoading, setIsLoading] = useState(true);
    const [error, setError] = useState<Error | null>(null);

    useEffect(() => {
        const source = new EventSource('/api/v1/log/overview/stream', { withCredentials: true });

        source.onopen = () => {
            setError(null);
            setIsLoading(false);
        };
        source.addEventListener('log', (event) => {
            let next: RelayLogOverview;
            try {
                next = JSON.parse((event as MessageEvent<string>).data) as RelayLogOverview;
            } catch {
                setError(new Error('Invalid log update'));
                return;
            }
            setIsLoading(false);
            setError(null);
            // 列表始终按 ID 倒序: 命中已有记录时原地替换, 新记录插入到首个更小 ID 之前,
            // 由此避免每条更新重排整个列表, 并保留未变更记录的引用以跳过卡片重渲染。
            setLogs((current) => {
                const index = current.findIndex((item) => item.id === next.id);
                if (index >= 0) {
                    const updated = current.slice();
                    updated[index] = next;
                    return updated;
                }
                const position = current.findIndex((item) => item.id < next.id);
                if (position < 0) return [...current, next];
                return [...current.slice(0, position), next, ...current.slice(position)];
            });
        });
        source.onerror = () => {
            setIsLoading(false);
            setError(new Error('Log stream disconnected'));
        };

        return () => {
            source.close();
        };
    }, []);

    return { logs, isLoading, error };
}

// useLogRequestBody 在调用方启用时按需获取指定日志的请求体。
export function useLogRequestBody(id: number, startedAt: string, enabled: boolean) {
    return useQuery({
        queryKey: ['logs', id, startedAt, 'request-body'],
        queryFn: () => apiRequest<string>(`/api/v1/log/request-body/${id}`),
        enabled,
        staleTime: Infinity,
    });
}

// useLogResponseBody 在调用方启用时获取指定日志的最终响应体。
export function useLogResponseBody(id: number, startedAt: string, enabled: boolean) {
    return useQuery({
        queryKey: ['logs', id, startedAt, 'response-body'],
        queryFn: () => apiRequest<string>(`/api/v1/log/response-body/${id}`),
        enabled,
        staleTime: Infinity,
    });
}
