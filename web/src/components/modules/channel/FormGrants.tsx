import { useEffect, useMemo, useRef, useState } from 'react';
import { createPortal } from 'react-dom';
import { ArrowDownUp, CheckCheck, ChevronDown, ChevronRight, ChevronsDownUp, ChevronsUpDown, Eraser, FolderPlus, HeartPulse, LoaderCircle, Plus, RefreshCw, Trash2, type LucideIcon } from 'lucide-react';
import { toast } from 'sonner';
import { useTranslations } from 'use-intl';
import { Protocol, useAssignChannelModels, useProbeChannelModels } from '@/api/channel';
import { useGroupList } from '@/api/group';
import { Checkbox } from '@/components/ui/checkbox';
import { Input } from '@/components/ui/input';
import { Button } from '@/components/ui/button';
import { IconButton } from '@/components/common/IconButton';
import { useModelProbe } from './probe';
import { addModelGrantKeys, grantKey, type ChannelFormState } from './state';

type SelectOption = { value: string; label: string };

function ThemeSelect({ value, options, onChange, ariaLabel, className }: {
    value: string;
    options: SelectOption[];
    onChange: (value: string) => void;
    ariaLabel: string;
    className?: string;
}) {
    const [open, setOpen] = useState(false);
    const buttonRef = useRef<HTMLButtonElement>(null);
    const menuRef = useRef<HTMLDivElement>(null);
    const [box, setBox] = useState({ top: 0, left: 0, width: 0, maxHeight: 240 });
    const selected = options.find((option) => option.value === value) ?? options[0];

    const place = () => {
        const rect = buttonRef.current?.getBoundingClientRect();
        if (!rect) return;
        const spaceBelow = window.innerHeight - rect.bottom;
        const maxHeight = Math.min(240, Math.max(120, spaceBelow > 160 ? spaceBelow - 12 : rect.top - 12));
        const top = spaceBelow > 160 ? rect.bottom + 4 : Math.max(8, rect.top - maxHeight - 4);
        setBox({ top, left: rect.left, width: Math.max(rect.width, 176), maxHeight });
    };

    useEffect(() => {
        if (!open) return;
        place();
        const onPointer = (event: MouseEvent) => {
            const target = event.target as Node;
            if (buttonRef.current?.contains(target) || menuRef.current?.contains(target)) return;
            setOpen(false);
        };
        const onKey = (event: KeyboardEvent) => {
            if (event.key === 'Escape') setOpen(false);
        };
        document.addEventListener('mousedown', onPointer);
        document.addEventListener('keydown', onKey);
        window.addEventListener('resize', place);
        window.addEventListener('scroll', place, true);
        return () => {
            document.removeEventListener('mousedown', onPointer);
            document.removeEventListener('keydown', onKey);
            window.removeEventListener('resize', place);
            window.removeEventListener('scroll', place, true);
        };
    }, [open]);

    return (
        <div className={className}>
            <button ref={buttonRef} type="button" role="combobox" aria-label={ariaLabel} aria-expanded={open} onClick={() => setOpen((previous) => !previous)} className="flex h-8 w-full items-center justify-between gap-2 rounded-lg border border-border bg-card px-2 text-left text-xs text-card-foreground">
                <span className="min-w-0 truncate">{selected?.label}</span>
                <ChevronDown className={`size-3.5 shrink-0 text-muted-foreground transition-transform ${open ? 'rotate-180' : ''}`} />
            </button>
            {open && createPortal(
                <div ref={menuRef} role="listbox" aria-label={ariaLabel} style={{ top: box.top, left: box.left, width: box.width, maxHeight: box.maxHeight }} className="fixed z-[80] overflow-y-auto rounded-lg border border-border bg-popover p-1 text-popover-foreground shadow-lg">
                    {options.map((option) => (
                        <button key={`${option.value}:${option.label}`} type="button" role="option" aria-selected={option.value === value} onClick={() => { onChange(option.value); setOpen(false); }} className={`flex min-h-8 w-full items-center rounded-md px-2 text-left text-xs ${option.value === value ? 'bg-accent text-accent-foreground' : 'text-popover-foreground hover:bg-muted hover:text-foreground'}`}>
                            <span className="min-w-0 truncate">{option.label}</span>
                        </button>
                    ))}
                </div>,
                document.body,
            )}
        </div>
    );
}

// GrantCells 渲染右侧固定的五格: chat, response, message, image 四个协议勾选和一个删除。
function GrantCells({ state, setState, models, keyNames, remove, icon: Icon, tip }: {
    state: ChannelFormState;
    setState: (next: ChannelFormState) => void;
    models: string[];
    keyNames: string[];
    remove?: () => void;
    icon: LucideIcon;
    tip: string;
}) {
    const cell = (bit: number) => {
        let on = 0;
        for (const modelName of models) {
            for (const keyName of keyNames) {
                if ((state.grants.get(grantKey(modelName, keyName)) ?? 0) & bit) on += 1;
            }
        }
        const total = models.length * keyNames.length;
        const value = total === 0 || on === 0 ? false : on === total ? true : 'indeterminate';
        return (
            <span className="w-7 flex justify-center">
                <Checkbox
                    checked={value}
                    disabled={total === 0}
                    onCheckedChange={() => {
                        const grants = new Map(state.grants);
                        for (const modelName of models) {
                            for (const keyName of keyNames) {
                                const mapKey = grantKey(modelName, keyName);
                                const current = grants.get(mapKey) ?? 0;
                                const protocols = value === true ? current & ~bit : current | bit;
                                if (protocols === 0) grants.delete(mapKey);
                                else grants.set(mapKey, protocols);
                            }
                        }
                        setState({ ...state, grants });
                    }}
                />
            </span>
        );
    };

    return (
        <>
            {cell(Protocol.OpenAIChatCompletion)}
            {cell(Protocol.OpenAIResponse)}
            {cell(Protocol.AnthropicMessage)}
            {cell(Protocol.OpenAIImage)}
            <span className="w-7 flex justify-center">
                {remove && (
                    <IconButton onClick={remove} disabled={models.length === 0} className="size-7 hover:text-destructive" tip={tip}>
                        <Icon className="size-3.5" />
                    </IconButton>
                )}
            </span>
        </>
    );
}

const ALL_KEYS = '__all__';

type ProbeMark = { ok: boolean; latency_ms: number; message?: string };

// 模型页管理当前渠道的模型、授权矩阵，以及模型分组和测活操作。
export function FormGrants({ state, setState, channelId, ensureSaved }: {
    state: ChannelFormState;
    setState: (next: ChannelFormState) => void;
    channelId?: number;
    ensureSaved?: () => Promise<number | undefined>;
}) {
    const t = useTranslations('channel.form');
    const { probe, pendingKey } = useModelProbe();
    const { data: groups = [] } = useGroupList();
    const assignModels = useAssignChannelModels();
    const probeModels = useProbeChannelModels();
    const [expanded, setExpanded] = useState<Set<string>>(new Set());
    const [searchTerm, setSearchTerm] = useState('');
    const [selectedKey, setSelectedKey] = useState(ALL_KEYS);
    const [selectedModels, setSelectedModels] = useState<Set<string>>(new Set());
    const [manualGroups, setManualGroups] = useState<Record<string, number | null>>({});
    const [selectionOverrides, setSelectionOverrides] = useState<Set<string>>(new Set());
    const [probeMarks, setProbeMarks] = useState<Record<string, ProbeMark>>({});
    const [probingModels, setProbingModels] = useState<Set<string>>(new Set());
    const [addDialogOpen, setAddDialogOpen] = useState(false);
    const [newModelName, setNewModelName] = useState('');

    const keyNames = state.keys.map((k) => k.name);
    const isAllKeys = selectedKey === ALL_KEYS;
    // 全部凭据模式下新增模型要覆盖全部凭据, 而非仅第一个: 只写第一个的话, 该模型的其余凭据没有授权,
    // 后续「分组」就只会带进一个 key。刷新按凭据逐个探测(见 probe.ts)。
    const writableKeys = addModelGrantKeys(keyNames, isAllKeys, selectedKey);
    const visibleModels = useMemo(() => {
        const source = isAllKeys
            ? state.models
            : state.models.filter((name) => (state.grants.get(grantKey(name, selectedKey)) ?? 0) !== 0);
        const term = searchTerm.trim().toLowerCase();
        return term ? source.filter((name) => name.toLowerCase().includes(term)) : source;
    }, [isAllKeys, searchTerm, selectedKey, state.grants, state.models]);
    const allExpanded = visibleModels.length > 0 && visibleModels.every((m) => expanded.has(m));
    const defaultGroups = useMemo(() => new Map(groups.map((group) => [group.name.toLowerCase(), group.id])), [groups]);
    const defaultGroupID = (modelName: string) => defaultGroups.get(modelName.toLowerCase()) ?? 0;
    const assignedGroupID = (modelName: string) => {
        if (!channelId) return 0;
        const want = modelName.toLowerCase();
        let fallback = 0;
        for (const group of groups) {
            const matched = (group.items ?? []).some((item) => item.channel_id === channelId && item.model_name.toLowerCase() === want);
            if (!matched) continue;
            if (group.name.toLowerCase() === want) return group.id;
            if (!fallback) fallback = group.id;
        }
        return fallback;
    };
    const targetGroupID = (modelName: string) => Object.prototype.hasOwnProperty.call(manualGroups, modelName)
        ? manualGroups[modelName] ?? 0
        : assignedGroupID(modelName) || defaultGroupID(modelName);
    const targetGroupValue = (modelName: string) => {
        if (Object.prototype.hasOwnProperty.call(manualGroups, modelName)) {
            return manualGroups[modelName] === null ? '' : String(manualGroups[modelName]);
        }
        const id = assignedGroupID(modelName) || defaultGroupID(modelName);
        return id > 0 ? String(id) : '';
    };
    const selectedModelSet = useMemo(() => {
        const next = new Set(selectedModels);
        for (const modelName of state.models) {
            const targetID = Object.prototype.hasOwnProperty.call(manualGroups, modelName)
                ? manualGroups[modelName] ?? 0
                : defaultGroups.get(modelName.toLowerCase()) ?? 0;
            if (!selectionOverrides.has(modelName) && targetID > 0) next.add(modelName);
        }
        return next;
    }, [defaultGroups, manualGroups, selectedModels, selectionOverrides, state.models]);
    const selectedVisibleModels = visibleModels.filter((modelName) => selectedModelSet.has(modelName));

    const removeGrant = (modelName: string, keyName: string) => {
        const grants = new Map(state.grants);
        grants.delete(grantKey(modelName, keyName));
        setState({ ...state, grants });
    };

    const removeModel = (modelName: string) => {
        const grants = new Map(state.grants);
        for (const keyName of keyNames) grants.delete(grantKey(modelName, keyName));
        setSelectedModels((previous) => {
            const next = new Set(previous);
            next.delete(modelName);
            return next;
        });
        setSelectionOverrides((previous) => {
            const next = new Set(previous);
            next.delete(modelName);
            return next;
        });
        setState({ ...state, models: state.models.filter((m) => m !== modelName), grants });
    };

    const addModel = () => {
        const name = newModelName.trim();
        if (!name || state.models.includes(name) || writableKeys.length === 0) return;
        const grants = new Map(state.grants);
        for (const keyName of writableKeys) {
            const mapKey = grantKey(name, keyName);
            grants.set(mapKey, (grants.get(mapKey) ?? 0) || Protocol.OpenAIResponse);
        }
        setState({ ...state, models: [...state.models, name], grants });
        setNewModelName('');
        setSearchTerm('');
        setAddDialogOpen(false);
    };

    const toggleAll = () => {
        setSelectedModels((previous) => {
            const next = new Set(previous);
            for (const modelName of visibleModels) next.add(modelName);
            return next;
        });
        setSelectionOverrides((previous) => {
            const next = new Set(previous);
            for (const modelName of visibleModels) next.delete(modelName);
            return next;
        });
    };

    const invertSelection = () => {
        const shown = new Set(selectedVisibleModels);
        setSelectedModels((previous) => {
            const next = new Set(previous);
            for (const modelName of visibleModels) {
                if (shown.has(modelName)) next.delete(modelName);
                else next.add(modelName);
            }
            return next;
        });
        setSelectionOverrides((previous) => {
            const next = new Set(previous);
            for (const modelName of visibleModels) {
                if (shown.has(modelName)) next.add(modelName);
                else next.delete(modelName);
            }
            return next;
        });
    };

    const toggleModelSelection = (modelName: string, checked: boolean) => {
        setSelectedModels((previous) => {
            const next = new Set(previous);
            if (checked) next.add(modelName); else next.delete(modelName);
            return next;
        });
        setSelectionOverrides((previous) => {
            const next = new Set(previous);
            if (checked) next.delete(modelName); else next.add(modelName);
            return next;
        });
    };

    const changeModelGroup = (modelName: string, value: string) => {
        const groupID = value === '' ? null : Number(value);
        setManualGroups((previous) => ({ ...previous, [modelName]: groupID }));
        toggleModelSelection(modelName, groupID !== null && groupID > 0);
    };

    const assignReason = (reason?: string) => {
        switch (reason) {
            case 'model_not_saved': return t('modelAssignReasonUnsaved');
            case 'no_available_grants': return t('modelAssignReasonUnavailable');
            case 'no_target_group': return t('modelAssignReasonNoGroup');
            case 'group_not_found': return t('modelAssignReasonMissingGroup');
            case 'already_in_group': return t('modelAssignReasonAlready');
            default: return reason || t('modelAssignFailed');
        }
    };

    const handleAssign = () => {
        if (selectedVisibleModels.length === 0) return;
        const missingGroup = selectedVisibleModels.filter((modelName) => targetGroupID(modelName) <= 0);
        if (missingGroup.length === selectedVisibleModels.length) {
            toast.error(t('modelAssignNeedGroup'));
            return;
        }
        const keyNamesForAssign = isAllKeys ? keyNames : [selectedKey];
        void (ensureSaved?.() ?? Promise.resolve(channelId)).then((id) => {
            if (!id) {
                toast.error(t('keysRequiredFirst'));
                return;
            }
            assignModels.mutate({
                channelId: id,
                keyName: isAllKeys ? '' : selectedKey,
                assignments: selectedVisibleModels.map((modelName) => ({
                    model_name: modelName,
                    group_id: targetGroupID(modelName),
                    grants: keyNamesForAssign
                        .map((keyName) => ({ model_name: modelName, key_name: keyName, protocols: state.grants.get(grantKey(modelName, keyName)) ?? 0 }))
                        .filter((grant) => grant.protocols !== 0),
                })),
            }, {
                onSuccess: (results) => {
                    const assigned = results.filter((result) => !result.skipped && result.grant_count > 0);
                    const skipped = results.filter((result) => result.skipped || result.grant_count === 0);
                    const groupNames = [...new Set(assigned.map((result) => result.group_name || String(result.group_id)))];
                    const reasons = [...new Set(skipped.map((result) => assignReason(result.reason)))].join('；');
                    if (assigned.length === 0) {
                        toast.error(t('modelAssignNone', { reason: reasons || t('modelAssignFailed') }));
                        return;
                    }
                    if (skipped.length > 0) toast.warning(t('modelAssignPartial', { assigned: assigned.length, skipped: skipped.length, groups: groupNames.join('、'), reason: reasons }));
                    else toast.success(t('modelAssignDone', { assigned: assigned.length, groups: groupNames.join('、') }));
                    setManualGroups((previous) => {
                        const next = { ...previous };
                        for (const result of assigned) next[result.model_name] = result.group_id;
                        return next;
                    });
                    setSelectedModels((previous) => {
                        const next = new Set(previous);
                        for (const result of assigned) next.delete(result.model_name);
                        return next;
                    });
                    setSelectionOverrides((previous) => new Set([...previous, ...assigned.map((result) => result.model_name)]));
                },
                onError: (error) => toast.error(t('modelAssignFailed'), { description: error.message }),
            });
        }).catch(() => undefined);
    };

    const handleProbe = (modelNames: string[]) => {
        if (modelNames.length === 0) return;
        void (ensureSaved?.() ?? Promise.resolve(channelId)).then((id) => {
            if (!id) {
                toast.error(t('keysRequiredFirst'));
                return;
            }
            setProbingModels((previous) => new Set([...previous, ...modelNames]));
            probeModels.mutate({ channelId: id, modelNames, keyName: isAllKeys ? '' : selectedKey }, {
                onSuccess: (results) => {
                    setProbeMarks((previous) => {
                        const next = { ...previous };
                        for (const result of results) next[`${result.model_name}\0${result.key_name}`] = result;
                        return next;
                    });
                    const failed = results.filter((result) => !result.ok).length;
                    if (results.length > 0) {
                        if (failed === 0) toast.success(t('modelProbeAllOk', { count: results.length }));
                        else toast.warning(t('modelProbePartial', { ok: results.length - failed, failed }));
                    }
                },
                onError: (error) => toast.error(t('modelProbeFailed'), { description: error.message }),
                onSettled: () => setProbingModels((previous) => {
                    const next = new Set(previous);
                    for (const modelName of modelNames) next.delete(modelName);
                    return next;
                }),
            });
        }).catch(() => undefined);
    };

    if (state.keys.length === 0) {
        return <p className="flex h-full items-center justify-center text-sm text-muted-foreground">{t('keysRequiredFirst')}</p>;
    }

    return (
        <div className="relative flex flex-col gap-3 h-full min-h-0">
            {/* 宽屏下这一排必须单行: 计数从 0/139 变 139/139 会多出一个字符的宽度,
                一旦允许换行, 整排图标会被挤到第二行, 表单凭空长高一层。窄屏仍换行,
                图标行自带 w-full, 本就该独占一行。 */}
            <div className="flex flex-wrap md:flex-nowrap items-center gap-2 shrink-0">
                <ThemeSelect
                    className="min-w-0 flex-1 md:w-36 md:flex-none"
                    value={selectedKey}
                    onChange={setSelectedKey}
                    ariaLabel={t('grantAllKeys')}
                    options={[{ value: ALL_KEYS, label: t('grantAllKeys') }, ...state.keys.map((key) => ({ value: key.name, label: key.name }))]}
                />
                <Input
                    value={searchTerm}
                    onChange={(event) => setSearchTerm(event.target.value)}
                    placeholder={t('modelSearchPlaceholder')}
                    className="h-8 min-w-0 flex-1 basis-32 rounded-lg px-2 text-xs"
                />
                <div className="flex w-full items-center gap-1 md:w-auto md:gap-2">
                    <IconButton onClick={() => setAddDialogOpen(true)} className="size-8 shrink-0" tip={t('modelAdd')}>
                        <Plus className="size-3.5" />
                    </IconButton>
                    <IconButton onClick={() => probe(state, setState, isAllKeys ? '' : selectedKey)} disabled={pendingKey !== null || !state.base_url.trim()} className="size-8 shrink-0" tip={t('modelRefresh')}>
                        <RefreshCw className={`size-3.5 ${pendingKey !== null ? 'animate-spin' : ''}`} />
                    </IconButton>
                    <span className="mx-1 h-5 w-px shrink-0 bg-border" />
                    <IconButton onClick={toggleAll} disabled={visibleModels.length === 0} className="size-8 shrink-0" tip={t('modelSelectAll')}><CheckCheck className="size-3.5" /></IconButton>
                    <IconButton onClick={invertSelection} disabled={visibleModels.length === 0} className="size-8 shrink-0" tip={t('modelInvertSelection')}><ArrowDownUp className="size-3.5" /></IconButton>
                    <IconButton onClick={handleAssign} disabled={selectedVisibleModels.length === 0 || assignModels.isPending} className="size-8 shrink-0" tip={t('modelAssignSelected')}>
                        {assignModels.isPending ? <LoaderCircle className="size-3.5 animate-spin" /> : <FolderPlus className="size-3.5" />}
                    </IconButton>
                    <IconButton onClick={() => handleProbe(selectedVisibleModels)} disabled={selectedVisibleModels.length === 0 || probeModels.isPending} className="size-8 shrink-0" tip={t('modelProbeSelected')}>
                        {probeModels.isPending ? <LoaderCircle className="size-3.5 animate-spin" /> : <HeartPulse className="size-3.5" />}
                    </IconButton>
                    {/* 计数占固定宽度: 位数变化只改数字本身, 不去挤旁边的搜索框。 */}
                    <span className="ml-auto shrink-0 px-1 min-w-12 text-right text-[11px] text-muted-foreground tabular-nums">{selectedVisibleModels.length}/{visibleModels.length}</span>
                </div>
            </div>

            <div className="flex-1 min-h-0 flex flex-col rounded-xl border border-border overflow-hidden">
                <div className="hidden md:flex items-center gap-1 px-3 py-2 border-b border-border bg-muted/30 shrink-0">
                    <IconButton onClick={() => setExpanded(allExpanded ? new Set() : new Set(visibleModels))} disabled={visibleModels.length === 0} className="size-5" tip={allExpanded ? t('grantCollapseAll') : t('grantExpandAll')}>
                        {allExpanded ? <ChevronsDownUp className="size-3.5" /> : <ChevronsUpDown className="size-3.5" />}
                    </IconButton>
                    <span className="ml-2 text-xs text-muted-foreground">{t('modelGroupColumn')}</span>
                    <span className="ml-auto min-w-0 truncate text-xs text-muted-foreground">chat / response / message / image</span>
                    <GrantCells
                        state={state} setState={setState} models={isAllKeys ? state.models : visibleModels} keyNames={isAllKeys ? keyNames : [selectedKey]}
                        remove={isAllKeys ? () => setState({ ...state, models: [], grants: new Map() }) : () => {
                            const grants = new Map(state.grants);
                            for (const modelName of state.models) grants.delete(grantKey(modelName, selectedKey));
                            setState({ ...state, grants });
                        }}
                        icon={Eraser} tip={isAllKeys ? t('grantClearAll') : t('grantClearCurrentKey')}
                    />
                </div>
                <div className="flex md:hidden items-center gap-1 border-b border-border bg-muted/30 px-3 py-2 shrink-0">
                    <IconButton onClick={() => setExpanded(allExpanded ? new Set() : new Set(visibleModels))} disabled={visibleModels.length === 0} className="size-6" tip={allExpanded ? t('grantCollapseAll') : t('grantExpandAll')}>
                        {allExpanded ? <ChevronsDownUp className="size-3.5" /> : <ChevronsUpDown className="size-3.5" />}
                    </IconButton>
                    <div className="ml-auto flex items-center">
                        <GrantCells
                            state={state} setState={setState} models={isAllKeys ? state.models : visibleModels} keyNames={isAllKeys ? keyNames : [selectedKey]}
                            remove={isAllKeys ? () => setState({ ...state, models: [], grants: new Map() }) : () => {
                                const grants = new Map(state.grants);
                                for (const modelName of state.models) grants.delete(grantKey(modelName, selectedKey));
                                setState({ ...state, grants });
                            }}
                            icon={Eraser} tip={isAllKeys ? t('grantClearAll') : t('grantClearCurrentKey')}
                        />
                    </div>
                </div>

                <div className="flex-1 min-h-0 overflow-y-auto overscroll-contain">
                    {visibleModels.length === 0 ? (
                        <p className="px-3 py-6 text-center text-sm text-muted-foreground">{state.models.length === 0 ? t('modelNoSelected') : t('modelNoneForKey')}</p>
                    ) : visibleModels.map((modelName) => {
                        const isOpen = expanded.has(modelName);
                        const granted = keyNames.filter((keyName) => (state.grants.get(grantKey(modelName, keyName)) ?? 0) !== 0).length;
                        const mark = Object.entries(probeMarks).find(([key]) => key.startsWith(`${modelName}\0`))?.[1];
                        return (
                            <div key={modelName} className="border-b border-border last:border-0">
                                <div className="flex md:hidden flex-col gap-2 px-3 py-2">
                                    <div className="flex items-center gap-1">
                                        <Checkbox checked={selectedModelSet.has(modelName)} onCheckedChange={(checked) => toggleModelSelection(modelName, checked === true)} aria-label={t('modelSelect', { model: modelName })} />
                                        <button type="button" onClick={() => setExpanded((previous) => {
                                            const next = new Set(previous);
                                            if (!next.delete(modelName)) next.add(modelName);
                                            return next;
                                        })} className="flex min-w-0 flex-1 items-center gap-1.5 text-left">
                                            <ChevronRight className={`size-3.5 shrink-0 text-muted-foreground transition-transform ${isOpen ? 'rotate-90' : ''}`} />
                                            <span className="truncate text-sm">{modelName}</span>
                                            {isAllKeys && <span className="shrink-0 text-xs tabular-nums text-muted-foreground">{granted}/{keyNames.length}</span>}
                                        </button>
                                        <IconButton
                                            onClick={() => handleProbe([modelName])}
                                            disabled={probingModels.has(modelName)}
                                            className={`size-8 shrink-0 ${mark ? (mark.ok ? 'text-emerald-500' : 'text-destructive') : ''}`}
                                            tip={mark ? (mark.ok ? t('modelProbePassed', { ms: mark.latency_ms }) : t('modelProbeFailed')) : t('modelProbeOne')}
                                        >
                                            {probingModels.has(modelName) ? <LoaderCircle className="size-3.5 animate-spin" /> : <HeartPulse className="size-3.5" />}
                                        </IconButton>
                                        <IconButton onClick={isAllKeys ? () => removeModel(modelName) : () => removeGrant(modelName, selectedKey)} className="size-8 shrink-0 hover:text-destructive" tip={isAllKeys ? t('modelRemove') : t('grantRemove')}>
                                            <Trash2 className="size-3.5" />
                                        </IconButton>
                                    </div>
                                    <div className="pl-7">
                                        <ThemeSelect className="min-w-0 w-full" value={targetGroupValue(modelName)} onChange={(value) => changeModelGroup(modelName, value)} ariaLabel={t('modelGroupFor', { model: modelName })} options={[{ value: '', label: t('modelGroupUnmatched') }, ...groups.map((group) => ({ value: String(group.id), label: group.name })), { value: '0', label: t('modelGroupNone') }]} />
                                    </div>
                                    {isOpen && (
                                        <div className="ml-7 space-y-1 rounded-lg border border-border/70 bg-muted/20 p-2">
                                                {state.keys.map((channelKey) => {
                                                    const protocols = state.grants.get(grantKey(modelName, channelKey.name)) ?? 0;
                                                    return (
                                                        <div key={channelKey.name} className={`grid grid-cols-[minmax(0,1fr)_repeat(5,1.75rem)] items-center gap-1 ${protocols === 0 ? 'opacity-45' : ''}`}>
                                                            <span className="min-w-0 truncate text-xs text-muted-foreground">{channelKey.name}</span>
                                                            <GrantCells state={state} setState={setState} models={[modelName]} keyNames={[channelKey.name]} remove={protocols !== 0 ? () => removeGrant(modelName, channelKey.name) : undefined} icon={Trash2} tip={t('grantRemove')} />
                                                        </div>
                                                    );
                                                })}
                                        </div>
                                    )}
                                </div>
                                <div className="hidden md:flex items-center gap-1 px-3 py-2">
                                    <Checkbox checked={selectedModelSet.has(modelName)} onCheckedChange={(checked) => toggleModelSelection(modelName, checked === true)} aria-label={t('modelSelect', { model: modelName })} />
                                    <button type="button" onClick={() => setExpanded((previous) => {
                                        const next = new Set(previous);
                                        if (!next.delete(modelName)) next.add(modelName);
                                        return next;
                                    })} className="flex items-center gap-1.5 min-w-0 flex-1 text-left">
                                        <ChevronRight className={`size-3.5 shrink-0 text-muted-foreground transition-transform ${isOpen ? 'rotate-90' : ''}`} />
                                        <span className="text-sm truncate">{modelName}</span>
                                        {isAllKeys && <span className="text-xs text-muted-foreground tabular-nums shrink-0">{granted}/{keyNames.length}</span>}
                                    </button>
                                    <ThemeSelect className="w-40 shrink-0" value={targetGroupValue(modelName)} onChange={(value) => changeModelGroup(modelName, value)} ariaLabel={t('modelGroupFor', { model: modelName })} options={[{ value: '', label: t('modelGroupUnmatched') }, ...groups.map((group) => ({ value: String(group.id), label: group.name })), { value: '0', label: t('modelGroupNone') }]} />
                                    <IconButton onClick={() => handleProbe([modelName])} disabled={probingModels.has(modelName)} className={`size-8 shrink-0 ${mark ? (mark.ok ? 'text-emerald-500' : 'text-destructive') : ''}`} tip={mark ? (mark.ok ? t('modelProbePassed', { ms: mark.latency_ms }) : t('modelProbeFailed')) : t('modelProbeOne')}>
                                        {probingModels.has(modelName) ? <LoaderCircle className="size-3.5 animate-spin" /> : <HeartPulse className="size-3.5" />}
                                    </IconButton>
                                    <GrantCells state={state} setState={setState} models={[modelName]} keyNames={isAllKeys ? keyNames : [selectedKey]} remove={isAllKeys ? () => removeModel(modelName) : () => removeGrant(modelName, selectedKey)} icon={Trash2} tip={isAllKeys ? t('modelRemove') : t('grantRemove')} />
                                </div>
                                {isOpen && (
                                    <div className="hidden md:block">
                                        {state.keys.map((channelKey) => {
                                    const protocols = state.grants.get(grantKey(modelName, channelKey.name)) ?? 0;
                                    return (
                                        <div key={channelKey.name} className={`flex items-center gap-1 pl-10 pr-3 py-1.5 bg-muted/20 md:pl-14 ${protocols === 0 ? 'opacity-45' : ''}`}>
                                            <span className="flex-1 text-xs text-muted-foreground truncate">{channelKey.name}</span>
                                            <GrantCells state={state} setState={setState} models={[modelName]} keyNames={[channelKey.name]} remove={protocols !== 0 ? () => removeGrant(modelName, channelKey.name) : undefined} icon={Trash2} tip={t('grantRemove')} />
                                        </div>
                                    );
                                })}
                                    </div>
                                )}
                            </div>
                        );
                    })}
                </div>
            </div>

            {addDialogOpen && (
                <div className="fixed inset-0 z-[100] flex items-center justify-center bg-black/35 p-4" onClick={() => setAddDialogOpen(false)}>
                    <div className="w-full max-w-sm rounded-2xl border border-border bg-card p-4 shadow-2xl" onClick={(event) => event.stopPropagation()} role="dialog" aria-modal="true">
                        <h3 className="text-sm font-semibold">{t('modelAddTitle')}</h3>
                        <Input autoFocus value={newModelName} onChange={(event) => setNewModelName(event.target.value)} onKeyDown={(event) => { if (event.key === 'Enter') { event.preventDefault(); addModel(); } }} placeholder={t('modelCustomPlaceholder')} className="mt-3 rounded-xl" />
                        <div className="mt-4 flex gap-2">
                            <Button type="button" variant="secondary" onClick={() => setAddDialogOpen(false)} className="flex-1 rounded-xl">{t('cancel')}</Button>
                            <Button type="button" onClick={addModel} disabled={!newModelName.trim() || state.models.includes(newModelName.trim())} className="flex-1 rounded-xl">{t('modelAddConfirm')}</Button>
                        </div>
                    </div>
                </div>
            )}
        </div>
    );
}
