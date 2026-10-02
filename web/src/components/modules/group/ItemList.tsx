import { useEffect, useId, useRef, useState } from 'react';
import { Candy, HeartPulse, Layers, GripVertical, X, Trash2, ArrowUp, ArrowDown } from 'lucide-react';
import {
    DragDropContext,
    Draggable,
    Droppable,
    type DraggableProvided,
    type DropResult,
} from '@hello-pangea/dnd';
import { motion, AnimatePresence } from 'motion/react';
import { cn } from '@/lib/utils';
import { getModelIcon } from '@/lib/model-icons';
import { Tooltip, TooltipContent, TooltipTrigger } from '@/components/ui/tooltip';
import { IconButton } from '@/components/common/IconButton';
import { ResultIconButton, describeIQ, describeProbe } from '@/components/common/ProbeButtons';
import { useTranslations } from 'use-intl';
import type { Group } from '@/api/group';
import { MemberStatus, freshProbe } from './MemberStatus';

export interface SelectedMember {
    id: string;
    channel_grant_id: number;
    name: string;
    enabled: boolean;
    channel_id: number;
    channel_name: string;
    key_name: string;
    protocols: number;
    item_id?: number;
}

function reorderList<T>(list: T[], startIndex: number, endIndex: number): T[] {
    const result = [...list];
    const [removed] = result.splice(startIndex, 1);
    result.splice(endIndex, 0, removed);
    return result;
}

type MemberItemDnd = {
    innerRef: DraggableProvided['innerRef'];
    draggableProps: DraggableProvided['draggableProps'];
    dragHandleProps: DraggableProvided['dragHandleProps'];
    isDragging: boolean;
};

// MemberItem 渲染可拖拽成员、编辑排序操作及删除确认状态。
function MemberItem({
    member,
    members,
    index,
    onReorder,
    onRemove,
    onActivate,
    onProbe,
    probing,
    onProbeIQ,
    probingIQ,
    isActive,
    group,
    now,
    isRemoving,
    showConfirmDelete = true,
    layoutScope,
    dnd,
}: {
    member: SelectedMember;
    members: SelectedMember[]; // 完整成员顺序，用于首尾移动。
    index: number; // 当前成员在排序数组中的位置。
    onReorder: (members: SelectedMember[]) => void; // 更新成员顺序。
    onRemove: (id: string) => void;
    onActivate?: (itemId: number) => void;
    onProbe?: (itemId: number) => void;
    probing?: boolean; // probing 表示该成员正在测活中, 按钮转为加载态并禁用重复提交。
    onProbeIQ?: (itemId: number) => void; // onProbeIQ 问该成员一道智商题(糖果测试)。
    probingIQ?: boolean; // probingIQ 表示该成员的糖果测试正在跑, 只让糖果按钮转圈, 不牵连心跳按钮。
    isActive?: boolean;
    group?: Group; // group 提供成员当前的冷却和亲和时间。
    now: number; // now 是成员列表共享的当前 Unix 毫秒时间。
    isRemoving?: boolean;
    showConfirmDelete?: boolean;
    layoutScope?: string;
    dnd: MemberItemDnd;
}) {
    const t = useTranslations('group');
    // 结论与徽标同源: 后端把测活和糖果测试都落在同一个成员槽位上, 所以两颗灯读同一个 probe,
    // 区别只在要不要看它的 .iq。
    const tCard = useTranslations('group.card');
    const probe = group ? freshProbe(group, member.item_id, now) : undefined;
    const iq = probe?.iq;
    const { Icon, className: iconClassName } = getModelIcon(member.name);
    const [confirmDelete, setConfirmDelete] = useState(false);
    const isDisabled = member.enabled === false;

    return (
        <div
            // DnD libraries provide imperative refs/props; the hook lint rule (`react-hooks/refs`)
            // flags this pattern, but it's safe and required for correct drag behavior.
            // eslint-disable-next-line react-hooks/refs
            ref={dnd.innerRef}
            // eslint-disable-next-line react-hooks/refs
            {...dnd.draggableProps}
            className={cn('rounded-lg grid transition-[grid-template-rows] duration-200', isRemoving ? 'grid-rows-[0fr]' : 'grid-rows-[1fr]')}
            // eslint-disable-next-line react-hooks/refs
            style={{
                /* eslint-disable-next-line react-hooks/refs */
                ...(dnd.draggableProps?.style ?? {}),
                /* eslint-disable-next-line react-hooks/refs */
                ...(dnd.isDragging ? { zIndex: 50, boxShadow: '0 8px 32px rgba(0,0,0,0.15)' } : null),
            }}
        >
            <div className={cn(
                'flex items-center gap-2 rounded-lg bg-background px-2.5 py-2 select-none transition-[background-color,opacity] duration-200 relative overflow-hidden',
                isRemoving && 'opacity-0',
                isDisabled && 'opacity-60 grayscale',
                onActivate && member.item_id !== undefined && 'cursor-pointer'
            )}
                onClick={() => member.item_id !== undefined && onActivate?.(member.item_id)}
                onKeyDown={(event) => {
                    if (event.target !== event.currentTarget || member.item_id === undefined || !onActivate || (event.key !== 'Enter' && event.key !== ' ')) return;
                    event.preventDefault();
                    onActivate(member.item_id);
                }}
                role={onActivate && member.item_id !== undefined ? 'button' : undefined}
                tabIndex={onActivate && member.item_id !== undefined ? 0 : undefined}
            >
                <div
                    className={cn(
                        'p-0.5 rounded touch-none transition-colors',
                        isDisabled
                            ? 'cursor-grab active:cursor-grabbing hover:bg-muted/60'
                            : 'cursor-grab active:cursor-grabbing hover:bg-muted'
                    )}
                    // eslint-disable-next-line react-hooks/refs
                    {...dnd.dragHandleProps}
                    onClick={(event) => event.stopPropagation()}
                >
                    <GripVertical className="size-3.5 text-muted-foreground" />
                </div>

                <span className={cn(isDisabled && 'opacity-70')}>
                    <Icon aria-hidden="true" className={iconClassName} width={18} height={18} />
                </span>

                <div className="flex flex-col min-w-0 flex-1">
                    <Tooltip>
                        <TooltipTrigger asChild>
                            <span className={cn(
                                'w-fit max-w-full text-sm font-medium truncate leading-tight',
                                isDisabled && 'text-muted-foreground'
                            )}>
                                {member.name}
                            </span>
                        </TooltipTrigger>
                        <TooltipContent key={member.name} side="top" sideOffset={10} align="center">
                            {member.name}
                        </TooltipContent>
                    </Tooltip>
                    <span className="text-[10px] text-muted-foreground truncate leading-tight">
                        {member.key_name ? `${member.channel_name} · ${member.key_name}` : member.channel_name}
                    </span>
                </div>

                {/* showProbe={false}: 这一行右侧自己带着会变色的心跳按钮, 结论已经在颜色里了。
                    再挂一枚体检徽标就是同一行两颗心说同一件事, 还多占一份宽度。 */}
                {group && <MemberStatus group={group} itemId={member.item_id} now={now} active={isActive} activeClassName="p-1" showProbe={false} />}

                {/* 两颗结论灯只在成员已落库(item_id 存在)且调用方愿意接收时出现:
                    编辑器里还没提交的新成员没有主键, 后端无从探测。
                    各管一件事: 心跳问"这条通道此刻通不通", 糖果问"它是不是变笨了"。
                    结论只在颜色里(绿=通过/正常, 玫红=没答上话, 琥珀=答错降智, 灰=还没测过),
                    耗时、上游报错正文、模型答了几 —— 全部只在悬停提示里, 不占这一行的正文位置。 */}
                {onProbe && member.item_id !== undefined && (
                    <ResultIconButton
                        icon={HeartPulse}
                        tone={!probe ? 'idle' : probe.ok ? 'ok' : 'bad'}
                        tip={probe ? describeProbe(probe, tCard) : tCard('probeNotYet')}
                        label={tCard('probe')}
                        pending={probing}
                        onClick={(event) => {
                            event.stopPropagation();
                            onProbe(member.item_id as number);
                        }}
                    />
                )}

                {/* 糖果测试与心跳分成两个独立的 pending: 共用一颗会让人以为"测活还没回来",
                    其实两条请求各跑各的, 一个转圈时另一个照样能点。 */}
                {onProbeIQ && member.item_id !== undefined && (
                    <ResultIconButton
                        icon={Candy}
                        tone={!iq ? 'idle' : iq.correct ? 'ok' : 'dumb'}
                        tip={iq ? describeIQ({ correct: iq.correct, answer: iq.answer }, tCard) : tCard('iqNotYet')}
                        label={tCard('probeIQ')}
                        pending={probingIQ}
                        onClick={(event) => {
                            event.stopPropagation();
                            onProbeIQ(member.item_id as number);
                        }}
                    />
                )}

                {!group && (
                    <>
                        <IconButton
                            disabled={index === 0 || isRemoving || dnd.isDragging}
                            onClick={(event) => {
                                event.stopPropagation();
                                onReorder(reorderList(members, index, 0));
                            }}
                            className="size-5 shrink-0 rounded hover:bg-muted"
                        >
                            <ArrowUp className="size-3" />
                        </IconButton>
                        <IconButton
                            disabled={index === members.length - 1 || isRemoving || dnd.isDragging}
                            onClick={(event) => {
                                event.stopPropagation();
                                onReorder(reorderList(members, index, members.length - 1));
                            }}
                            className="size-5 shrink-0 rounded hover:bg-muted"
                        >
                            <ArrowDown className="size-3" />
                        </IconButton>
                    </>
                )}

                {(!showConfirmDelete || !confirmDelete) && (
                    <motion.button
                        layoutId={`delete-btn-member-${layoutScope ?? 'default'}-${member.id}`}
                        type="button"
                        onClick={(event) => {
                            event.stopPropagation();
                            if (showConfirmDelete) setConfirmDelete(true);
                            else onRemove(member.id);
                        }}
                        className="p-1 rounded hover:text-destructive transition-colors"
                        transition={{ duration: 0.15 }}
                        style={{ pointerEvents: 'auto' }}
                    >
                        <X className="size-3" />
                    </motion.button>
                )}

                <AnimatePresence>
                    {showConfirmDelete && confirmDelete && (
                        <motion.div
                            layoutId={`delete-btn-member-${layoutScope ?? 'default'}-${member.id}`}
                            className="absolute inset-0 flex items-center justify-center gap-2 bg-destructive p-1.5 rounded-lg"
                            onClick={(event) => event.stopPropagation()}
                            transition={{ type: 'spring', stiffness: 400, damping: 30 }}
                        >
                            <button
                                type="button"
                                onClick={(event) => {
                                    event.stopPropagation();
                                    setConfirmDelete(false);
                                }}
                                className="flex h-6 w-6 items-center justify-center rounded-md bg-destructive-foreground/20 text-destructive-foreground transition-all hover:bg-destructive-foreground/30 active:scale-95"
                            >
                                <X className="h-3 w-3" />
                            </button>
                            <button
                                type="button"
                                onClick={(event) => {
                                    event.stopPropagation();
                                    onRemove(member.id);
                                }}
                                className="flex-1 h-6 flex items-center justify-center gap-1.5 rounded-md bg-destructive-foreground text-destructive text-xs font-semibold transition-all hover:bg-destructive-foreground/90 active:scale-[0.98]"
                            >
                                <Trash2 className="h-3 w-3" />
                            </button>
                        </motion.div>
                    )}
                </AnimatePresence>
            </div>
        </div>
    );
}

interface MemberListProps {
    members: SelectedMember[];
    onReorder: (members: SelectedMember[]) => void;
    onRemove: (id: string) => void;
    onActivate?: (itemId: number) => void;
    onProbe?: (itemId: number) => void;
    probingItemIds?: Set<number>;
    onProbeIQ?: (itemId: number) => void; // onProbeIQ 问该成员一道智商题(糖果测试)。
    probingIQItemIds?: Set<number>; // probingIQItemIds 记录糖果测试正在跑的成员, 与测活的集合分开。
    activeItemId?: number;
    group?: Group; // group 提供当前模式和成员运行状态。
    now?: number; // now 是页面共享的当前 Unix 毫秒时间，仅展示运行态时需要。
    /**
     * When true, auto-scroll the list to bottom when a *new visible* member appears
     * (i.e. a new member id is added). Useful in "editor" flows. Defaults to true.
     */
    autoScrollOnAdd?: boolean;
    onDragStart?: () => void;
    /**
     * Called only when a drop results in a different order (i.e. commit reorder).
     * Useful for persisting the new order.
     */
    onDrop?: (members: SelectedMember[]) => void;
    /**
     * Called whenever a drag ends (including cancel / same-index drop).
     * Useful for lifecycle cleanup (e.g. clearing "isDragging" flags).
     */
    onDragFinish?: () => void;
    removingIds?: Set<string>;
    /**
     * When true, show a confirmation overlay before removing an item.
     * When false, clicking the delete button removes the item immediately.
     * Defaults to true.
     */
    showConfirmDelete?: boolean;
    layoutScope?: string;
}

export function MemberList({
    members,
    onReorder,
    onRemove,
    onActivate,
    onProbe,
    probingItemIds = new Set(),
    onProbeIQ,
    probingIQItemIds = new Set(),
    activeItemId,
    group,
    now = 0,
    autoScrollOnAdd = true,
    onDragStart,
    onDrop,
    onDragFinish,
    removingIds = new Set(),
    showConfirmDelete = true,
    layoutScope: externalLayoutScope,
}: MemberListProps) {
    const internalLayoutScope = useId();
    const layoutScope = externalLayoutScope ?? internalLayoutScope;
    const scrollContainerRef = useRef<HTMLDivElement | null>(null);
    const prevMemberCountRef = useRef<number>(0);
    const hasMountedRef = useRef(false);

    const visibleCount = members.filter((m) => !removingIds.has(m.id)).length;
    const isEmpty = visibleCount === 0;
    const t = useTranslations('group');

    useEffect(() => {
        // Skip the initial mount so we don't auto-scroll on first render / initial data load.
        if (!hasMountedRef.current) {
            hasMountedRef.current = true;
            prevMemberCountRef.current = members.length;
            return;
        }

        if (!autoScrollOnAdd) {
            prevMemberCountRef.current = members.length;
            return;
        }

        const hasNewMember = members.length > prevMemberCountRef.current;

        // Auto-scroll only when member count increases (i.e. added; not reorder / not "unhide").
        if (hasNewMember) {
            // Wait a tick for DOM/placeholder/layout to settle.
            requestAnimationFrame(() => {
                const el = scrollContainerRef.current;
                if (!el) return;
                el.scrollTo({ top: el.scrollHeight, behavior: 'smooth' });
            });
        }

        prevMemberCountRef.current = members.length;
    }, [members.length, autoScrollOnAdd]);

    const handleDragEnd = (result: DropResult) => {
        try {
            const { destination, source } = result;
            if (!destination) return;
            if (destination.index === source.index) return;

            const next = reorderList(members, source.index, destination.index);
            onReorder(next);
            onDrop?.(next);
        } finally {
            // Ensure drag lifecycle always finishes, even when drop is canceled.
            onDragFinish?.();
        }
    };

    return (
        <div className="relative h-full min-h-0">
            <div
                className={cn(
                    'absolute inset-0 flex flex-col items-center justify-center gap-2 text-muted-foreground',
                    'transition-opacity duration-200 ease-out',
                    isEmpty ? 'opacity-100' : 'opacity-0 pointer-events-none'
                )}
            >
                <Layers className="size-10 opacity-40" />
                <span className="text-sm">{t('card.empty')}</span>
            </div>

            <div
                className={cn(
                    'h-full overflow-y-auto transition-opacity duration-200',
                    isEmpty ? 'opacity-0' : 'opacity-100'
                )}
                ref={scrollContainerRef}
            >
                <DragDropContext
                    onDragStart={() => onDragStart?.()}
                    onDragEnd={handleDragEnd}
                >
                    <Droppable
                        droppableId={`members-${layoutScope}`}
                        renderClone={(draggableProvided, snapshot, rubric) => (
                            <MemberItem
                                member={members[rubric.source.index]}
                                members={members}
                                index={rubric.source.index}
                                onReorder={onReorder}
                                onRemove={onRemove}
                                onActivate={onActivate}
                                onProbe={onProbe}
                                probing={members[rubric.source.index].item_id !== undefined && probingItemIds.has(members[rubric.source.index].item_id as number)}
                                onProbeIQ={onProbeIQ}
                                probingIQ={members[rubric.source.index].item_id !== undefined && probingIQItemIds.has(members[rubric.source.index].item_id as number)}
                                isActive={members[rubric.source.index].item_id === activeItemId}
                                group={group}
                                now={now}
                                isRemoving={false}
                                showConfirmDelete={showConfirmDelete}
                                layoutScope={layoutScope}
                                dnd={{
                                    innerRef: draggableProvided.innerRef,
                                    draggableProps: draggableProvided.draggableProps,
                                    dragHandleProps: draggableProvided.dragHandleProps,
                                    isDragging: snapshot.isDragging,
                                }}
                            />
                        )}
                    >
                        {(droppableProvided) => (
                            <div
                                ref={droppableProvided.innerRef}
                                {...droppableProvided.droppableProps}
                                className="p-2 flex flex-col space-y-1.5"
                            >
                                {members.map((member, index) => (
                                    <Draggable
                                        key={member.id}
                                        draggableId={member.id}
                                        index={index}
                                        isDragDisabled={removingIds.has(member.id)}
                                    >
                                        {(draggableProvided, snapshot) => (
                                            <MemberItem
                                                member={member}
                                                members={members}
                                                index={index}
                                                onReorder={onReorder}
                                                onRemove={onRemove}
                                                onActivate={onActivate}
                                                onProbe={onProbe}
                                                probing={member.item_id !== undefined && probingItemIds.has(member.item_id)}
                                                onProbeIQ={onProbeIQ}
                                                probingIQ={member.item_id !== undefined && probingIQItemIds.has(member.item_id)}
                                                isActive={member.item_id === activeItemId}
                                                group={group}
                                                now={now}
                                                isRemoving={removingIds.has(member.id)}
                                                showConfirmDelete={showConfirmDelete}
                                                layoutScope={layoutScope}
                                                dnd={{
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
                            </div>
                        )}
                    </Droppable>
                </DragDropContext>
            </div>
        </div>
    );
}
