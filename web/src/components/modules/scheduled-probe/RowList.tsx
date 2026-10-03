import { useState } from 'react';
import { DragDropContext, Draggable, Droppable, type DraggableProvided, type DropResult } from '@hello-pangea/dnd';
import { Candy, GripVertical, HeartPulse, X } from 'lucide-react';
import { useTranslations } from 'use-intl';
import { toast } from 'sonner';
import {
    useProbeGrantIQNow,
    useProbeGrantNow,
    useSetScheduledProbeCredential,
    useSetScheduledProbeOrder,
    type ScheduledProbeRow,
} from '@/api/scheduled-probe';
import { IconButton } from '@/components/common/IconButton';
import { ResultIconButton, describeIQ, describeProbe } from '@/components/common/ProbeButtons';
import { Tooltip, TooltipContent, TooltipTrigger } from '@/components/ui/tooltip';
import { cn } from '@/lib/utils';
import { describeProbedAt } from './format';

// 本文件负责卡片里的行列表：一行 = 一条凭据，可单独测、可单独删、可拖着排顺序。
//
// 单列一个文件是因为"顺序"要落到后端去：拖出来的次序就是测活轮转的次序(见 RowList 的说明),
// 让它和拖拽实现待在同一个文件里, 改动时才不容易把这条约定漏掉。

type RowDnd = {
    innerRef: DraggableProvided['innerRef'];
    draggableProps: DraggableProvided['draggableProps'];
    dragHandleProps: DraggableProvided['dragHandleProps'];
    isDragging: boolean;
};

// moveWithin 把第 startIndex 项挪到 endIndex 处; 拖动排序只需要这一个动作。
function moveWithin<T>(list: T[], startIndex: number, endIndex: number): T[] {
    const next = [...list];
    const [moved] = next.splice(startIndex, 1);
    next.splice(endIndex, 0, moved);
    return next;
}

// sameGrantOrder 判断两份行是不是同一个顺序(按授权主键比)。
// 比主键而不是比整行: 测活结论随时在变, 比整行会让"顺序已经同步"永远判不成立。
function sameGrantOrder(left: ScheduledProbeRow[], right: ScheduledProbeRow[]): boolean {
    return left.length === right.length && left.every((row, index) => row.grant_id === right[index]?.grant_id);
}

// RowList 逐行列出各条凭据。一行 = 一条凭据，行尾的闪电只测这一条、× 只删这一条。
//
// 凭据行可以拖着排顺序, 而且这个顺序不只是好看: 后端按同一份顺序轮转, 拖到最前的那条就是
// 下一拍最先被测的那条。所以这里拖完必须回传服务端 —— 只改本地渲染等于骗人。
//
// now 是页面共享的当前时刻, 只为把「多久以前」算准而往下传(见 useProbeClock)。
export function RowList({ probeId, rows, now }: { probeId: number; rows: ScheduledProbeRow[]; now: number }) {
    const t = useTranslations('scheduledProbe');
    const setOrder = useSetScheduledProbeOrder();
    // 拖动之后到服务端回话之前, 先按用户拖出来的顺序渲染: 否则卡片会弹回旧顺序,
    // 看起来就像拖动根本没生效。
    const [dragged, setDragged] = useState<ScheduledProbeRow[] | null>(null);
    const credits = dragged && dragged.length === rows.length && !sameGrantOrder(dragged, rows) ? dragged : rows;

    if (rows.length === 0) {
        return <p className="px-1 py-2 text-xs text-muted-foreground/70">{t('noTarget')}</p>;
    }

    const handleDragEnd = (result: DropResult) => {
        const { destination, source } = result;
        if (!destination || destination.index === source.index) {
            return;
        }

        const next = moveWithin(credits, source.index, destination.index);
        setDragged(next);
        setOrder.mutate(
            {
                id: probeId,
                credits: next.map((row) => ({
                    channel_id: row.channel_id,
                    model_name: row.model_name,
                    key_name: row.key_name,
                })),
            },
            {
                onError: (error) => {
                    setDragged(null);
                    toast.error(error.message);
                },
            },
        );
    };

    return (
        <DragDropContext onDragEnd={handleDragEnd}>
            <Droppable droppableId={`probe-credits-${probeId}`}>
                {(droppableProvided) => (
                    <ul ref={droppableProvided.innerRef} {...droppableProvided.droppableProps} className="flex flex-col gap-1">
                        {credits.map((row, index) => (
                            <Draggable key={row.grant_id} draggableId={String(row.grant_id)} index={index}>
                                {(draggableProvided, snapshot) => (
                                    <Row
                                        probeId={probeId}
                                        row={row}
                                        now={now}
                                        dnd={{
                                            // 这三个都必须落到 DOM 上, 少一个都拖不动:
                                            // innerRef 让拖动项登记进注册表, draggableProps 带上它的标识与位移,
                                            // dragHandleProps 才是指定的着手点。
                                            // 只铺 dragHandleProps 时握把上该有的属性一个不少, 看起来完全正常,
                                            // 但拖动项从未登记, 按下永远不会抬起来 —— 一个只靠读代码很难发现的坑。
                                            innerRef: draggableProvided.innerRef,
                                            draggableProps: draggableProvided.draggableProps,
                                            dragHandleProps: draggableProvided.dragHandleProps,
                                            isDragging: snapshot.isDragging,
                                        }}
                                    />
                                )}
                            </Draggable>
                        ))}
                        {droppableProvided.placeholder}
                    </ul>
                )}
            </Droppable>
        </DragDropContext>
    );
}

// Row 渲染一条凭据：左侧是「渠道名/凭据名」(大字)、右侧是「心跳 + 糖果」两颗结论灯与只删这一条的 ×。
// 行首的握把是拖动的落点: 整行可拖会和行内的两个按钮抢手势, 也会把文字选择一起吞掉。
function Row({ probeId, row, now, dnd }: { probeId: number; row: ScheduledProbeRow; now: number; dnd?: RowDnd }) {
    const t = useTranslations('scheduledProbe');
    const probeGrant = useProbeGrantNow();
    const probeIQGrant = useProbeGrantIQNow();
    const setCredential = useSetScheduledProbeCredential();
    const label = row.key_name || `#${row.grant_id}`;

    const handleProbe = () => {
        probeGrant.mutate(row.grant_id, {
            onSuccess: () => toast.success(t('toast.probeDone')),
            onError: (error) => toast.error(error.message),
        });
    };

    // handleProbeIQ 只问这一条凭据一道智商题。
    // 三类分开汇报: 请求失败的既不是正常也不是降智(它根本没答上话), 混进任何一档都是假结论;
    // 出现降智用 warning 而不是 error: 降智是"模型变笨了"这个观察结果, 不是一次失败的操作。
    const handleProbeIQ = () => {
        probeIQGrant.mutate(row.grant_id, {
            onSuccess: (verdict) => {
                if (!verdict.iq) {
                    toast.error(t('toast.iqFailed', { message: verdict.message || t('probeUnknownError') }));
                    return;
                }
                const conclusion = describeIQ(verdict.iq, t);
                if (verdict.iq.correct) {
                    toast.success(conclusion);
                    return;
                }
                toast.warning(conclusion);
            },
            onError: (error) => toast.error(error.message),
        });
    };

    // 删除不再确认：这个动作只影响当前凭据行，不会删除渠道本身的授权。
    const handleRemove = () => {
        setCredential.mutate(
            {
                id: probeId,
                channelId: row.channel_id,
                modelName: row.model_name,
                keyName: row.key_name,
                excluded: true,
            },
            {
                onSuccess: () => toast.success(t('toast.credentialRemoved')),
                onError: (error) => toast.error(error.message),
            },
        );
    };

    return (
        <li
            ref={dnd?.innerRef}
            {...dnd?.draggableProps}
            className={cn(
                'flex items-center gap-1.5 rounded-lg border border-border/60 px-1.5 py-1',
                // 被拎起来的那一行浮在上层, 不加阴影的话它会和下面的行糊在一起, 看不出正在拖哪一行。
                dnd?.isDragging && 'border-primary/40 bg-card shadow-md',
            )}
        >
            {dnd && (
                <span
                    {...dnd.dragHandleProps}
                    aria-label={t('dragToSort')}
                    title={t('dragToSort')}
                    className="-ml-0.5 flex size-4 shrink-0 cursor-grab items-center justify-center text-muted-foreground/40 transition-colors hover:text-muted-foreground active:cursor-grabbing"
                >
                    <GripVertical className="size-3" />
                </span>
            )}

            {/* 三段并排: 渠道名 / 凭据名 / 模型名。
                卡片排到 3 列后宽度有限, 三段的取舍必须明确: 渠道名与凭据名是"哪条通道的哪把钥匙",
                是这一行的身份, 用 shrink-0 保住; 模型名最长、也最容易从上下文推回来, 由它单独承担省略号。
                三段都靠外层 overflow-hidden 兜底: 万一渠道名本身就超长, 至少边界是干净的而不是溢出到结论上。
                完整文本始终在悬停提示里, 截断只影响扫视, 不影响核对。 */}
            <Tooltip>
                <TooltipTrigger asChild>
                    <span className="flex min-w-0 flex-1 items-center overflow-hidden text-sm">
                        <span className="shrink-0 text-foreground">{row.channel_name || `#${row.channel_id}`}</span>
                        <span className="shrink-0 text-muted-foreground/50">/</span>
                        <span className="shrink-0 text-foreground">{label}</span>
                        {row.model_name && (
                            <>
                                <span className="shrink-0 text-muted-foreground/50">/</span>
                                <span className="min-w-0 truncate text-muted-foreground">{row.model_name}</span>
                            </>
                        )}
                    </span>
                </TooltipTrigger>
                <TooltipContent side="top" sideOffset={10} align="start">
                    {`${row.channel_name || `#${row.channel_id}`} / ${label}`}
                    {row.model_name ? ` / ${row.model_name}` : ''}
                </TooltipContent>
            </Tooltip>

            {/* 结论产生于多久以前必须留在正文里: 只看两颗灯的颜色, 分不出"刚才测的"和"昨天测的"——
                颜色一样, 但可信度完全不同(结论不设有效期, 只会被下一次测活覆盖)。
                只写相对时间, 不写耗时: "6792ms" 属于详情, 写在行里会把身份挤没, 它留在悬停提示里。 */}
            {row.probed && (
                <span className="shrink-0 text-[11px] text-muted-foreground/60 tabular-nums">
                    {describeProbedAt(row.probed_at, now, t)}
                </span>
            )}

            {/* 两颗结论灯: 心跳在前(原来的闪电就站在这个位置, 职责也相同 —— 只测这一条凭据),
                糖果在后。两个都是"既是按钮又是结论": 点它发起测试, 颜色说上一次的结论。
                耗时、过期时间、模型答了几仍然只在悬停提示里 —— 正文只留"多久以前"。 */}
            <ResultIconButton
                icon={HeartPulse}
                tone={!row.probed ? 'idle' : row.ok ? 'ok' : 'bad'}
                tip={
                    row.probed
                        ? `${describeProbe(row, t)} · ${describeProbedAt(row.probed_at, now, t)}`
                        : t('probeThis')
                }
                label={t('probeThis')}
                pending={probeGrant.isPending}
                onClick={handleProbe}
            />

            {/* 糖果测试按 iq_asked 而不是 iq_answer 判断"测过没有": "问了但答不出数字"的答案也是空串,
                按答案判空会把这一种状态整个吞掉 —— 模型胡言乱语反而在界面上不留痕迹。 */}
            <ResultIconButton
                icon={Candy}
                tone={!row.iq_asked ? 'idle' : row.iq_correct ? 'ok' : 'dumb'}
                tip={row.iq_asked ? describeIQ({ correct: row.iq_correct, answer: row.iq_answer }, t) : t('iqThis')}
                label={t('iqThis')}
                pending={probeIQGrant.isPending}
                onClick={handleProbeIQ}
            />

            {/* 行尾的 × 只删这一条凭据；删除后该行从任务中消失。 */}
            <IconButton
                onClick={handleRemove}
                disabled={setCredential.isPending}
                tip={t('removeCredential')}
                aria-label={t('removeCredential')}
                className="size-6 hover:text-destructive"
            >
                <X className="size-3" />
            </IconButton>
        </li>
    );
}
