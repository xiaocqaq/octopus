import { memo, useState, useMemo, useCallback, useEffect, useLayoutEffect, useRef } from 'react';
import { createPortal } from 'react-dom';
import { Hand, HeartPulse, LoaderCircle, Shuffle, Trash2, X, Pencil } from 'lucide-react';
import { motion, AnimatePresence } from 'motion/react';
import { type Group, type GroupMode, type GroupUpdateRequest, useDeleteGroup, useUpdateGroup, useProbeGroup, useProbeGroupItem } from '@/api/group';
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
    // 成员区默认收起, 悬停展开: 展开态由全页面共享, 同一时间只有一张卡片展开。
    // 展开/收起各有 100ms 延迟(见 syncHover), 划过一排分组时不连环闪;
    // 而"移到另一张分组"这条路径由共享状态直接接管(那边一置位, 这张就不再是 active), 切换依旧利落。
    const activeGroupID = useGroupHoverStore((state) => state.activeGroupID);
    const setActiveGroup = useGroupHoverStore((state) => state.setActiveGroup);
    const expanded = activeGroupID === group.id;
    const isDragging = useRef(false);
    const cardRef = useRef<HTMLElement>(null); // 浮层的定位基准: 与卡片外框严丝合缝地拼成同一张卡。
    const overlayRef = useRef<HTMLElement>(null); // 浮层自身, 量真实高度用于上下方空间判断。
    // 指针是否分别停在卡片与浮层上。用两个标志而非计数: 浮层不在卡片的 DOM 子树里, 两者各自的
    // enter/leave 由 React 分别派发, 先后顺序不定, 靠"取消计时器"去对抗顺序会出现鼠标还在浮层上却折了。
    // 每次事件只改自己那个标志再从真实状态重算, 与顺序无关。
    const overCardRef = useRef(false);
    const overOverlayRef = useRef(false);
    // 浮层用 fixed 定位挂在屏外一份, 故需自己算出位置; null 表示尚未测量, 此时隐藏且不接收指针。
    const [overlayRect, setOverlayRect] = useState<{ top: number; left: number; width: number; side: 'below' | 'above' } | null>(null);


    // syncHover 依据两个标志调度展开/收起, 两个方向各有 100ms 防抖: 快速划过一排分组时不连环闪层。
    // 唯一的例外: 已有分组正展开时"换一张"立即生效——共享状态一置位, 旧的自动收起, 卡片间切换不吃延迟。
    // 鼠标从卡片挪到相切的浮层时, 两边的 leave/enter 在同一次鼠标移动里同步派发, 后到的 enter 会取消
    // 前脚刚挂上的收起计时, 不会闪断; 拖拽成员期间不收起(拖出列表范围时 mouseleave 会先触发)。
    // 定时器只有一份且先到先得地互相取消: 同一张卡片上"进-出-进"永远以最后的事件为准。
    const hoverTimer = useRef<number | null>(null);
    const clearHoverTimer = useCallback(() => {
        if (hoverTimer.current !== null) {
            window.clearTimeout(hoverTimer.current);
            hoverTimer.current = null;
        }
    }, []);
    const syncHover = useCallback(() => {
        const hovered = overCardRef.current || overOverlayRef.current;
        clearHoverTimer();
        if (hovered) {
            if (useGroupHoverStore.getState().activeGroupID !== null) {
                setActiveGroup(group.id);
                return;
            }
            hoverTimer.current = window.setTimeout(() => {
                hoverTimer.current = null;
                setActiveGroup(group.id);
            }, 100);
            return;
        }
        if (isDragging.current) return;
        hoverTimer.current = window.setTimeout(() => {
            hoverTimer.current = null;
            // 只收自己这一次: 期间指针可能已移到别的分组, 那次的展开不能被这里误清。
            if (useGroupHoverStore.getState().activeGroupID === group.id) setActiveGroup(null);
        }, 100);
    }, [group.id, setActiveGroup, clearHoverTimer]);

    const handleCardEnter = useCallback(() => { overCardRef.current = true; syncHover(); }, [syncHover]);
    const handleCardLeave = useCallback(() => { overCardRef.current = false; syncHover(); }, [syncHover]);
    const handleOverlayEnter = useCallback(() => { overOverlayRef.current = true; syncHover(); }, [syncHover]);
    const handleOverlayLeave = useCallback(() => { overOverlayRef.current = false; syncHover(); }, [syncHover]);

    // 浮层收起后清掉它的悬停标志并复位生长方向: 卸载不会补发 leave, 留着会让下一次悬停永远等不到"两个都离开";
    // 方向不复位的话, 上一张在底部翻过向上的卡片会带着 above 直接盖住自己的头部。
    // 位置一并清空: 收起期间卡片可能被滚走, 下次展开要重新量过再出现, 不吃上一次的旧坐标。
    useLayoutEffect(() => {
        if (!expanded) {
            overOverlayRef.current = false;
            overlaySideRef.current = 'below';
            setOverlayRect(null);
        }
    }, [expanded]);

    // 展开的当帧就用卡片外框定出"贴下方生长"的矩形, 且浮层在拿到矩形之前根本不渲染(见下方 portal 的
    // 条件渲染)。两者缺一不可: 卡片外框此刻同步可量, 浮层高度要挂上去才知道, 所以先按 below 落位,
    // 由 follow 循环量到真实高度后再决定要不要翻到上方 —— 现有滞回逻辑原样保留。
    // 若让浮层先以"未测量"的兜底样式(left/top 0, width 0)挂上去, 那份零宽布局会被成员行删除按钮的
    // layoutId 投影当成起点快照, 拿到真实矩形后 Framer Motion 便把这个 X 从行中间一路补间到右端
    // (实测 418.66px / 0.25s), 看起来就是"删除 X 在乱跑"。
    useLayoutEffect(() => {
        if (!expanded) return;
        const card = cardRef.current;
        if (!card) return;
        const rect = card.getBoundingClientRect();
        overlaySideRef.current = 'below';
        setOverlayRect({ top: rect.bottom, left: rect.left, width: rect.width, side: 'below' });
    }, [expanded]);

    // 浮层默认贴在卡片外框正下方, 左右与宽度照抄卡片外框: 两侧边框与圆角由此接得上, 视觉上是同一张卡在向下生长。
    // 下方空间放不下整份成员列表时翻到卡片上方生长: 贴底的卡片被裁是看不见的, 宁可向上也不能遮住成员。
    // 翻转判断带滞回: 两个方向都装得下时维持现状, 否则"上方放不下"的浮层翻上去后量自身又触发翻回, 两态来回抖动。
    const overlaySideRef = useRef<'below' | 'above'>('below');
    const updateOverlayRect = useCallback(() => {
        const card = cardRef.current;
        if (!card) return;
        const rect = card.getBoundingClientRect();
        const height = overlayRef.current?.offsetHeight ?? 0;
        const gap = 16; // 视口上下边缘各留的呼吸位, 与弹窗的 2rem 习惯一致取半。
        if (height > 0) {
            const fitsBelow = rect.bottom + height + gap <= window.innerHeight;
            const fitsAbove = rect.top - height - gap >= 0;
            if (overlaySideRef.current === 'below' ? !fitsBelow && fitsAbove : !fitsAbove && fitsBelow) {
                overlaySideRef.current = overlaySideRef.current === 'below' ? 'above' : 'below';
            }
        }
        const nextRect: { top: number; left: number; width: number; side: 'below' | 'above' } = overlaySideRef.current === 'below'
            ? { top: rect.bottom, left: rect.left, width: rect.width, side: 'below' }
            : { top: rect.top - height, left: rect.left, width: rect.width, side: 'above' };
        setOverlayRect((previous) => (
            previous && previous.top === nextRect.top && previous.left === nextRect.left
                && previous.width === nextRect.width && previous.side === nextRect.side
                ? previous
                : nextRect
        ));
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

    useEffect(() => () => {
        // 卸载时先掐掉待触发的悬停计时器: 卡片被移除(换页/过滤)时计时器还挂着的话,
        // 100ms 后它仍会回调, 对着一个已经不在屏幕上的卡片置位或清零, 造成莫名的展开/收起。
        clearHoverTimer();
        if (useGroupHoverStore.getState().activeGroupID === group.id) setActiveGroup(null);
    }, [group.id, setActiveGroup, clearHoverTimer]);

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

    // 测活（体检）：拿最小的一次真实上游调用问一句"这个成员此刻通不通"。
    // 结论由后端写进路由状态并经事件流广播，前端不落本地副本 —— 单条与一键共用同一份结论展示。
    const probeOne = useProbeGroupItem();
    const probeAll = useProbeGroup();
    // 正在测活的成员集合。只做按钮级反馈，不设全局串行锁：后端一条与一键是各自独立的一次调用，
    // 用户点第二个成员不应该被第一条挡住。
    const [probingItemIds, setProbingItemIds] = useState<Set<number>>(() => new Set());
    const markProbing = useCallback((itemId: number, on: boolean) => {
        setProbingItemIds((previous) => {
            const next = new Set(previous);
            if (on) next.add(itemId);
            else next.delete(itemId);
            return next;
        });
    }, []);

    const handleProbe = useCallback((itemId: number) => {
        markProbing(itemId, true);
        probeOne.mutate(
            { groupId: group.id, itemId },
            {
                onError: (error) => toast.error(t('toast.probeFailed'), { description: error.message }),
                onSettled: () => markProbing(itemId, false),
            },
        );
    }, [group.id, probeOne, markProbing, t]);

    // 一键测活：不传成员即测全部。结论本身由后端推送，这里只汇报"几条通几条不通"。
    const handleProbeAll = useCallback(() => {
        probeAll.mutate({ groupId: group.id }, {
            onSuccess: (results) => {
                const failed = results.filter((result) => !result.ok).length;
                if (results.length === 0) return;
                if (failed === 0) toast.success(t('toast.probeAllOk', { count: results.length }));
                else toast.warning(t('toast.probeAllPartial', { ok: results.length - failed, failed }));
            },
            onError: (error) => toast.error(t('toast.probeFailed'), { description: error.message }),
        });
    }, [group.id, probeAll, t]);

    const handleSubmitEdit = useCallback((values: GroupEditorValues, onDone?: () => void) => {
        const payload: GroupUpdateRequest & { id: number } = { id: group.id };

        if (values.name !== group.name) payload.name = values.name;
        if (values.mode !== group.mode) payload.mode = values.mode;
        if (
            values.relay_config.member_max_attempts !== group.relay_config.member_max_attempts ||
            values.relay_config.member_retry_interval_seconds !== group.relay_config.member_retry_interval_seconds ||
            values.relay_config.member_non_stream_response_timeout_seconds !== group.relay_config.member_non_stream_response_timeout_seconds ||
            values.relay_config.member_stream_first_event_timeout_seconds !== group.relay_config.member_stream_first_event_timeout_seconds ||
            values.relay_config.member_stream_total_timeout_seconds !== group.relay_config.member_stream_total_timeout_seconds ||
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
            // 展开时把相切一侧边框设为透明并调整圆角: 浮层接着那一处生长, 同时保留卡片原有尺寸避免网格重排。
            className={cn(
                'flex flex-col border border-border bg-card p-4 text-card-foreground',
                expanded
                    ? overlayRect?.side === 'above'
                        ? 'rounded-b-3xl border-t-transparent'
                        : 'rounded-t-3xl border-b-transparent'
                    : 'rounded-3xl',
            )}
        >
            <header className={cn(
                'flex items-start justify-between relative overflow-visible rounded-xl -mx-1 px-1 -my-1 py-1',
                'mb-3',
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
            浮层与卡片无缝拼成同一张卡: 默认向下生长时顶部方角无上边框, 接着卡片的去底边版本往下长;
            翻到上方生长时镜像处理, 底部方角无下边框, 卡片改为去顶边。左右边框与宽度照抄卡片外框。
            高度固定且不参与卡片布局, 卡片高度因此恒等于收起态, 网格不会被撑变形。
            层级取 z-40: 高于网格行, 低于拖拽克隆体(5000)与弹窗(z-50), 拖拽和弹窗都不会被它挡住。 */}
        {/* overlayRect 是浮层能被正确落位的唯一凭据: 没量到就不挂载, 不能用兜底坐标挂上去 ——
            零宽布局会被成员的 layoutId 投影当作起点, 展开时那个 X 会从行中间滑到右端。 */}
        {expanded && overlayRect && createPortal(
            <section
                ref={overlayRef}
                // 浮层不在卡片的 DOM 子树里, 故需自己维系悬停: 两条热区各记各的标志, 指针停在任一处都保持展开。
                onMouseEnter={handleOverlayEnter}
                onMouseLeave={handleOverlayLeave}
                // 外层给不透明实底: bg-muted/30 只有 30% 不透明度, 直接当最外层背景会让下方卡片整个透出来,
                // 它必须像原版那样铺在卡片实底之上, 故退到内层面板。
                className={cn(
                    'fixed z-40 border-border bg-card text-card-foreground',
                    (overlayRect?.side ?? 'below') === 'below'
                        ? 'rounded-b-3xl border-x border-b px-4 pb-4 pt-3'
                        : 'rounded-t-3xl border-x border-t px-4 pb-3 pt-4',
                )}
                style={{
                    top: overlayRect?.top ?? 0,
                    left: overlayRect?.left ?? 0,
                    width: overlayRect?.width ?? 0,
                    visibility: overlayRect ? 'visible' : 'hidden',
                    pointerEvents: overlayRect ? 'auto' : 'none',
                }}
            >
                {/* 一键测活: 把整组挨个体检一遍。放在成员列表上方而不是卡片标题栏 —— 它是"对这组做一件事",
                    与标题栏那排管理动作(切模式/编辑/复制/删除)不同层。 */}
                <div className="mb-2 flex items-center justify-between gap-2 px-0.5">
                    <span className="truncate text-[10px] font-medium text-muted-foreground">{t('form.items')}</span>
                    <Tooltip>
                        <TooltipTrigger asChild>
                            <button
                                type="button"
                                disabled={probeAll.isPending}
                                onClick={handleProbeAll}
                                className={cn(
                                    'flex shrink-0 items-center gap-1 rounded-md px-1.5 py-0.5 text-[10px] font-medium transition-colors',
                                    'text-muted-foreground hover:bg-primary/10 hover:text-primary',
                                    probeAll.isPending && 'opacity-50 cursor-not-allowed hover:bg-transparent hover:text-muted-foreground'
                                )}
                            >
                                {probeAll.isPending
                                    ? <LoaderCircle className="size-3 animate-spin" />
                                    : <HeartPulse className="size-3" />}
                                {t(probeAll.isPending ? 'card.probingAll' : 'card.probeAll')}
                            </button>
                        </TooltipTrigger>
                        <TooltipContent side="top" sideOffset={8} align="center">
                            {t('card.probeAllHint')}
                        </TooltipContent>
                    </Tooltip>
                </div>

                <div className="h-101 overflow-hidden rounded-xl border border-border/50 bg-muted/30">
                    <MemberList
                        members={members}
                        onReorder={setMembers}
                        onRemove={handleRemoveMember}
                        // 两种模式都可点选: 手动模式指定当前成员, 故障转移模式强制优先使用。
                        onActivate={handleActivate}
                        onProbe={handleProbe}
                        probingItemIds={probingItemIds}
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
