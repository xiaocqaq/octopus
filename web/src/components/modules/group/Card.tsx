import { memo, useState, useMemo, useCallback, useEffect, useLayoutEffect, useRef } from 'react';
import { createPortal } from 'react-dom';
import { Hand, Shuffle, Trash2, X, Pencil } from 'lucide-react';
import { motion, AnimatePresence } from 'motion/react';
import { type Group, type GroupMode, type GroupUpdateRequest, useDeleteGroup, useUpdateGroup } from '@/api/group';
import { useTranslations } from 'use-intl';
import { toast } from 'sonner';
import { cn } from '@/lib/utils';
import { CopyIconButton } from '@/components/common/CopyButton';
import { IconButton } from '@/components/common/IconButton';
import { Tooltip, TooltipContent, TooltipTrigger } from '@/components/ui/tooltip';
import type { SelectedMember } from './ItemList';
import { MemberList } from './ItemList';
import { useGroupHoverStore } from './hover';
import { GroupEditor, type GroupEditorValues } from './Editor';
import {
    MorphingDialog,
    MorphingDialogContainer,
    MorphingDialogContent,
    MorphingDialogDescription,
    MorphingDialogTrigger,
    useMorphingDialog,
} from '@/components/ui/morphing-dialog';

interface EditDialogContentProps {
    group: Group;
    displayMembers: SelectedMember[];
    isSubmitting: boolean;
    onSubmit: (values: GroupEditorValues, onDone?: () => void) => void;
}

function EditDialogContent({ group, displayMembers, isSubmitting, onSubmit }: EditDialogContentProps) {
    const { setIsOpen } = useMorphingDialog();
    const t = useTranslations('group');
    return (
        <MorphingDialogDescription className="flex-1 min-h-0 overflow-hidden">
            <GroupEditor
                key={`edit-group-${group.id}`}
                initial={{
                    name: group.name,
                    mode: group.mode,
                    relay_config: group.relay_config,
                    members: displayMembers,
                }}
                submitText={t('detail.actions.save')}
                submittingText={t('create.submitting')}
                isSubmitting={isSubmitting}
                onCancel={() => setIsOpen(false)}
                onSubmit={(v) => onSubmit(v, () => setIsOpen(false))}
            />
        </MorphingDialogDescription>
    );
}

export const GroupCard = memo(function GroupCard({ group, now }: { group: Group; now: number }) {
    const t = useTranslations('group');
    const updateGroup = useUpdateGroup();
    const activateItem = useUpdateGroup(); // 与配置提交分开持有: 共用一个实例会让点选成员点亮编辑弹窗的提交态。
    const deleteGroup = useDeleteGroup();

    const updateMode = useUpdateGroup(); // 与配置提交和点选成员分开持有: 三者的 pending 各自驱动不同控件。

    const [confirmDelete, setConfirmDelete] = useState(false);
    const [members, setMembers] = useState<SelectedMember[]>([]);
    // 成员区默认收起, 悬停即展开: 展开态由全页面共享, 同一时间只有一张卡片展开,
    // 指针移到另一张分组上时上一张立刻收起, 不靠各自计时。
    const activeGroupID = useGroupHoverStore((state) => state.activeGroupID);
    const setActiveGroup = useGroupHoverStore((state) => state.setActiveGroup);
    const expanded = activeGroupID === group.id;
    const isDragging = useRef(false);
    const collapseTimer = useRef(0); // 离开后的延迟收起计时器, 让鼠标短暂擦过卡片时不至于闪开闪关。
    const cardRef = useRef<HTMLElement>(null); // 浮层的定位基准: 与卡片外框严丝合缝地拼成同一张卡。
    // 指针是否分别停在卡片与浮层上。用两个标志而非计数: 浮层不在卡片的 DOM 子树里, 两者各自的
    // enter/leave 由 React 分别派发, 先后顺序不定, 靠"取消计时器"去对抗顺序会出现鼠标还在浮层上却折了。
    // 每次事件只改自己那个标志再从真实状态重算, 与顺序无关。
    const overCardRef = useRef(false);
    const overOverlayRef = useRef(false);
    // 浮层用 fixed 定位挂在屏外一份, 故需自己算出位置; null 表示尚未测量, 此时不渲染。
    const [overlayRect, setOverlayRect] = useState<{ top: number; left: number; width: number } | null>(null);


    // syncHover 依据两个标志重算展开状态: 仍在任一热区内就展开自己, 都已离开才延迟收起。
    // 展开写共享状态, 故指针移到另一张卡片时那边一置位, 这张就不再是 active, 立刻收起。
    const syncHover = useCallback(() => {
        window.clearTimeout(collapseTimer.current);
        if (overCardRef.current || overOverlayRef.current) {
            setActiveGroup(group.id);
            return;
        }
        collapseTimer.current = window.setTimeout(() => {
            // 计时期间指针又回到任一热区, 或正在拖拽成员, 都不收起: 拖出列表范围时 mouseleave 会先触发, 收了拖拽就断了。
            if (overCardRef.current || overOverlayRef.current || isDragging.current) return;
            // 只清自己这一次: 期间指针可能已移到别的分组, 那次的展开不能被这里误清。
            if (useGroupHoverStore.getState().activeGroupID === group.id) setActiveGroup(null);
        }, 1000);
    }, [group.id, setActiveGroup]);

    const handleCardEnter = useCallback(() => { overCardRef.current = true; syncHover(); }, [syncHover]);
    const handleCardLeave = useCallback(() => { overCardRef.current = false; syncHover(); }, [syncHover]);
    const handleOverlayEnter = useCallback(() => { overOverlayRef.current = true; syncHover(); }, [syncHover]);
    const handleOverlayLeave = useCallback(() => { overOverlayRef.current = false; syncHover(); }, [syncHover]);

    // 浮层收起后清掉它的悬停标志: 卸载不会补发 leave, 留着会让下一次悬停永远等不到"两个都离开"。
    useLayoutEffect(() => {
        if (!expanded) overOverlayRef.current = false;
    }, [expanded]);

    // 浮层贴在卡片外框正下方, 左右与宽度照抄卡片外框: 两侧边框与圆角由此接得上, 视觉上是同一张卡在向下生长。
    // 不做贴底钳制: 浮层随滚动重新贴合, 靠近视口底部时滚动即可看到, 强行上移反而会与卡片错位露出接缝。
    const updateOverlayRect = useCallback(() => {
        const card = cardRef.current;
        if (!card) return;
        const rect = card.getBoundingClientRect();
        setOverlayRect({ top: rect.bottom, left: rect.left, width: rect.width });
    }, []);

    // 浮层随卡片滚动/窗口缩放重新贴合; scroll 不冒泡, 故用捕获阶段接住内层滚动容器的滚动。
    // 另外在展开初期逐帧重算一小段时间: 页面进场等祖先动画带 transform 平移卡片, 动画中量到的外框
    // 是移动途中的位置, 只量一次会把浮层钉在错位处; 动画结束后位置即稳定, 故只跟随前 800ms。
    useLayoutEffect(() => {
        if (!expanded) return;
        let frame = 0;
        const startedAt = performance.now();
        const follow = () => {
            updateOverlayRect();
            if (performance.now() - startedAt < 800) frame = requestAnimationFrame(follow);
        };
        frame = requestAnimationFrame(follow);

        window.addEventListener('scroll', updateOverlayRect, true);
        window.addEventListener('resize', updateOverlayRect);
        return () => {
            cancelAnimationFrame(frame);
            window.removeEventListener('scroll', updateOverlayRect, true);
            window.removeEventListener('resize', updateOverlayRect);
        };
    }, [expanded, updateOverlayRect]);

    useEffect(() => () => window.clearTimeout(collapseTimer.current), []);

    // 卡片卸载时归还共享的展开态。展开态是模块级的, 不随组件卸载复位, 而卸载时也补不上 mouseleave:
    // 切走页面再切回, 或卡片被虚拟列表移出渲染范围, 残留的 id 会让卡片凭空呈展开态且再也收不起来。
    // 只清自己这一次, 免得误清别的卡片刚设上的展开。
    useEffect(() => () => {
        if (useGroupHoverStore.getState().activeGroupID === group.id) setActiveGroup(null);
    }, [group.id, setActiveGroup]);

    // 成员的名称, 所属渠道与可用性由后端随分组给出, 此处只做展示形状的转换。
    // 不可用的成员同样列出: 否则用户看不到它的存在也就无法移除。
    const displayMembers = useMemo((): SelectedMember[] =>
        (group.items || []).map((item) => ({
            id: String(item.channel_grant_id),
            channel_grant_id: item.channel_grant_id,
            name: item.model_name,
            enabled: item.available,
            channel_id: item.channel_id,
            channel_name: item.channel_name,
            key_name: item.key_name,
            protocols: item.protocols,
            item_id: item.id,
        })),
        [group.items]
    );

    useEffect(() => {
        if (!isDragging.current) setMembers([...displayMembers]);
    }, [displayMembers]);

    const onSuccess = useCallback(() => toast.success(t('toast.updated')), [t]);
    const onError = useCallback((error: Error) => toast.error(t('toast.updateFailed'), { description: error.message }), [t]);

    const handleDragStart = useCallback(() => { isDragging.current = true; }, []);
    const handleDragFinish = useCallback(() => {
        isDragging.current = false;
        // 拖拽期间指针可能在卡片外, 那时 mouseleave 已经算过一次"离开"; 在此按当前标志位重算一次。
        syncHover();
    }, [syncHover]);

    // 成员为整体替换, 拖拽与移除都直接提交当前排列, 优先级由提交顺序决定。
    const submitMembers = useCallback((next: SelectedMember[]) => {
        updateGroup.mutate(
            { id: group.id, items: next.map((m) => ({ channel_grant_id: m.channel_grant_id })) },
            { onSuccess, onError },
        );
    }, [group.id, updateGroup, onSuccess, onError]);

    const handleRemoveMember = useCallback((id: string) => {
        submitMembers(members.filter((m) => m.id !== id));
    }, [members, submitMembers]);

    // 点选成员在两种模式下语义不同: 手动模式指定当前成员, 故障转移模式强制优先使用该成员。
    // 两者都以"再点一次即取消"处理, 提交 0。
    const handleActivate = useCallback((itemId: number) => {
        if (activateItem.isPending) return;
        const payload: GroupUpdateRequest & { id: number } = { id: group.id };
        if (group.mode === 'manual') {
            payload.active_item_id = itemId === group.runtime.current_item_id ? 0 : itemId;
        } else {
            payload.pinned_item_id = itemId === group.pinned_item_id ? 0 : itemId;
        }
        activateItem.mutate(payload, { onSuccess, onError });
    }, [activateItem, group.id, group.mode, group.pinned_item_id, group.runtime.current_item_id, onError, onSuccess]);

    // 快捷切换路由模式; 点当前模式不提交。
    const handleModeChange = useCallback((value: string) => {
        if (value === group.mode || updateMode.isPending) return;
        updateMode.mutate({ id: group.id, mode: value as GroupMode }, { onSuccess, onError });
    }, [group.id, group.mode, updateMode, onSuccess, onError]);

    const handleSubmitEdit = useCallback((values: GroupEditorValues, onDone?: () => void) => {
        const payload: GroupUpdateRequest & { id: number } = { id: group.id };

        if (values.name !== group.name) payload.name = values.name;
        if (values.mode !== group.mode) payload.mode = values.mode;
        if (
            values.relay_config.member_max_attempts !== group.relay_config.member_max_attempts ||
            values.relay_config.member_retry_interval_seconds !== group.relay_config.member_retry_interval_seconds ||
            values.relay_config.member_non_stream_response_timeout_seconds !== group.relay_config.member_non_stream_response_timeout_seconds ||
            values.relay_config.member_stream_first_event_timeout_seconds !== group.relay_config.member_stream_first_event_timeout_seconds ||
            values.relay_config.member_cooldown_seconds !== group.relay_config.member_cooldown_seconds ||
            values.relay_config.member_affinity_seconds !== group.relay_config.member_affinity_seconds
        ) payload.relay_config = values.relay_config;
        // 成员集合与顺序有任一处不同就整体提交; 后端按授权主键匹配, 已有成员保留其主键与统计。
        const nextGrantIDs = values.members.map((m) => m.channel_grant_id);
        const currentGrantIDs = (group.items || []).map((item) => item.channel_grant_id);
        if (nextGrantIDs.length !== currentGrantIDs.length || nextGrantIDs.some((id, i) => id !== currentGrantIDs[i])) {
            payload.items = nextGrantIDs.map((channel_grant_id) => ({ channel_grant_id }));
        }

        if (Object.keys(payload).length === 1) {
            onDone?.();
            return;
        }

        updateGroup.mutate(payload, {
            onSuccess: () => {
                onSuccess();
                onDone?.();
            },
            onError,
        });
    }, [group.id, group.items, group.mode, group.name, group.relay_config, onSuccess, onError, updateGroup]);

    return (
        <>
        <article
            ref={cardRef}
            onMouseEnter={handleCardEnter}
            onMouseLeave={handleCardLeave}
            // 展开时去掉底边框与下方圆角: 浮层接着这一处往下长, 两段拼起来才是原版那张完整的卡。
            className={cn(
                'flex flex-col border-border bg-card text-card-foreground',
                expanded
                    ? 'rounded-t-3xl border-x border-t px-4 pt-4'
                    : 'rounded-3xl border p-4',
            )}
        >
            <header className={cn(
                'flex items-start justify-between relative overflow-visible rounded-xl -mx-1 px-1 -my-1 py-1',
                !expanded && 'mb-3',
            )}>
                <div className="relative flex-1 mr-2 min-w-0 group/title">
                    <Tooltip>
                        <TooltipTrigger asChild>
                            <h3 className="text-lg font-bold truncate">{group.name}</h3>
                        </TooltipTrigger>
                        <TooltipContent key={group.name} side="top" sideOffset={10} align="center">
                            {group.name}
                        </TooltipContent>
                    </Tooltip>
                </div>

                <div className="flex items-center gap-1 shrink-0">
                    {/* 路由模式开关: 与右侧编辑/复制/删除同款图标按钮, 点一下即切换另一种模式。
                        图标即当前模式, 说明放在 Tooltip 里; 悬浮展开成员列表已足够表达"当前选了谁"。 */}
                    <IconButton
                        onClick={() => handleModeChange(group.mode === 'manual' ? 'failover' : 'manual')}
                        disabled={updateMode.isPending}
                        className="size-7"
                        tip={`${t(group.mode === 'manual' ? 'form.manual' : 'form.failover')} · ${t('card.modeToggleHint')}`}
                    >
                        {group.mode === 'manual'
                            ? <Hand className="size-4" />
                            : <Shuffle className="size-4" />}
                    </IconButton>

                    <MorphingDialog>
                        {/* trigger 自身渲染 motion.div 承担弹窗形变, 故由它出元素, IconButton 只补样式。 */}
                        <IconButton asChild className="size-7">
                            <MorphingDialogTrigger>
                                <Pencil className="size-4" />
                            </MorphingDialogTrigger>
                        </IconButton>

                        <MorphingDialogContainer>
                            <MorphingDialogContent
                                dismissOnClickOutside={false}
                                className="relative w-screen max-w-full md:max-w-4xl bg-card text-card-foreground px-6 py-4 rounded-3xl h-[calc(100vh-2rem)] flex flex-col overflow-hidden"
                            >
                                <EditDialogContent
                                    group={group}
                                    displayMembers={displayMembers}
                                    isSubmitting={updateGroup.isPending}
                                    onSubmit={handleSubmitEdit}
                                />
                            </MorphingDialogContent>
                        </MorphingDialogContainer>
                    </MorphingDialog>

                    {/* CopyIconButton 自带按钮元素与复制成功的图标切换, 故以 asChild 交给它渲染。 */}
                    <IconButton asChild className="size-7">
                        <CopyIconButton
                            text={group.name}
                            copyIconClassName="size-4"
                            checkIconClassName="size-4 text-primary"
                        />
                    </IconButton>
                    {/* asChild 保留 motion.button: 它与确认态共享 layoutId, 换成普通按钮会丢掉形变动画。 */}
                    {!confirmDelete && (
                        <IconButton asChild className="size-7 hover:text-destructive">
                            <motion.button layoutId={`delete-btn-group-${group.id}`} type="button" onClick={() => setConfirmDelete(true)}>
                                <Trash2 className="size-4" />
                            </motion.button>
                        </IconButton>
                    )}
                </div>

                <AnimatePresence>
                    {confirmDelete && (
                        <motion.div layoutId={`delete-btn-group-${group.id}`} className="absolute inset-0 flex items-center justify-center gap-2 bg-destructive p-2 rounded-xl" transition={{ type: 'spring', stiffness: 400, damping: 30 }}>
                            <button type="button" onClick={() => setConfirmDelete(false)} className="flex h-7 w-7 items-center justify-center rounded-lg bg-destructive-foreground/20 text-destructive-foreground transition-all hover:bg-destructive-foreground/30 active:scale-95">
                                <X className="size-4" />
                            </button>
                            <button type="button" onClick={() => deleteGroup.mutate(group.id, { onSuccess: () => toast.success(t('toast.deleted')) })} disabled={deleteGroup.isPending} className="flex-1 h-7 flex items-center justify-center gap-2 rounded-lg bg-destructive-foreground text-destructive text-sm font-semibold transition-all hover:bg-destructive-foreground/90 active:scale-[0.98] disabled:opacity-50 disabled:cursor-not-allowed">
                                <Trash2 className="size-3.5" />
                                {t('detail.actions.confirmDelete')}
                            </button>
                        </motion.div>
                    )}
                </AnimatePresence>
            </header>

        </article >

        {/* 成员浮层挂在 body 上, 而不是留在卡片内: VirtualizedGrid 的行带 transform, 会为每行建立层叠上下文,
            卡片内的任何 z-index 都被困在自己那一行里, 压不住后面渲染的行。挂到 body 才真正盖得住下方卡片。
            浮层与卡片无缝拼成同一张卡: 顶部方角无上边框, 接着卡片的去底边版本往下长, 左右边框与宽度照抄卡片外框,
            底部收成与卡片相同的圆角。高度固定且不参与卡片布局, 卡片高度因此恒等于收起态, 网格不会被撑变形。
            层级取 z-40: 高于网格行, 低于拖拽克隆体(5000)与弹窗(z-50), 拖拽和弹窗都不会被它挡住。 */}
        {expanded && overlayRect && createPortal(
            <section
                // 浮层不在卡片的 DOM 子树里, 故需自己维系悬停: 两条热区各记各的标志, 指针停在任一处都保持展开。
                onMouseEnter={handleOverlayEnter}
                onMouseLeave={handleOverlayLeave}
                // 外层给不透明实底: bg-muted/30 只有 30% 不透明度, 直接当最外层背景会让下方卡片整个透出来,
                // 它必须像原版那样铺在卡片实底之上, 故退到内层面板。
                className="fixed z-40 rounded-b-3xl border-x border-b border-border bg-card text-card-foreground px-4 pb-4 pt-3"
                style={{ top: overlayRect.top, left: overlayRect.left, width: overlayRect.width }}
            >
                <div className="h-101 overflow-hidden rounded-xl border border-border/50 bg-muted/30">
                    <MemberList
                        members={members}
                        onReorder={setMembers}
                        onRemove={handleRemoveMember}
                        // 两种模式都可点选: 手动模式指定当前成员, 故障转移模式强制优先使用。
                        onActivate={handleActivate}
                        activeItemId={group.runtime.current_item_id}
                        group={group}
                        now={now}
                        onDragStart={handleDragStart}
                        onDrop={submitMembers}
                        onDragFinish={handleDragFinish}
                        autoScrollOnAdd={false}
                        layoutScope={`card-${group.id}`}
                    />
                </div>
            </section>,
            document.body,
        )}
        </>
    );
});
