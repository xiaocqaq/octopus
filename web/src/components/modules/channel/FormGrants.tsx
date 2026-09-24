import { useMemo, useState } from 'react';
import { ArrowDownUp, CheckCheck, ChevronRight, ChevronsDownUp, ChevronsUpDown, Eraser, FolderPlus, HeartPulse, LoaderCircle, Plus, RefreshCw, Trash2, type LucideIcon } from 'lucide-react';
import { toast } from 'sonner';
import { useTranslations } from 'use-intl';
import { Protocol, useAssignChannelModels, useProbeChannelModels } from '@/api/channel';
import { useGroupList } from '@/api/group';
import { Checkbox } from '@/components/ui/checkbox';
import { Input } from '@/components/ui/input';
import { Button } from '@/components/ui/button';
import { IconButton } from '@/components/common/IconButton';
import { useModelProbe } from './probe';
import { grantKey, type ChannelFormState } from './state';

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
export function FormGrants({ state, setState, channelId }: {
    state: ChannelFormState;
    setState: (next: ChannelFormState) => void;
    channelId?: number;
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
    const activeKey = isAllKeys ? (keyNames[0] || '') : selectedKey;
    const visibleModels = useMemo(() => {
        const source = isAllKeys
            ? state.models
            : state.models.filter((name) => (state.grants.get(grantKey(name, selectedKey)) ?? 0) !== 0);
        const term = searchTerm.trim().toLowerCase();
        return term ? source.filter((name) => name.toLowerCase().includes(term)) : source;
    }, [isAllKeys, searchTerm, selectedKey, state.grants, state.models]);
    const allExpanded = visibleModels.length > 0 && visibleModels.every((m) => expanded.has(m));
    const defaultGroups = useMemo(() => new Map(groups.map((group) => [group.name, group.id])), [groups]);
    const targetGroupID = (modelName: string) => Object.prototype.hasOwnProperty.call(manualGroups, modelName)
        ? manualGroups[modelName] ?? 0
        : defaultGroups.get(modelName) ?? 0;
    const targetGroupValue = (modelName: string) => Object.prototype.hasOwnProperty.call(manualGroups, modelName)
        ? manualGroups[modelName] === null ? '' : String(manualGroups[modelName])
        : defaultGroups.has(modelName) ? String(defaultGroups.get(modelName)) : '';
    const selectedModelSet = useMemo(() => {
        const next = new Set(selectedModels);
        for (const modelName of state.models) {
            const targetID = Object.prototype.hasOwnProperty.call(manualGroups, modelName)
                ? manualGroups[modelName] ?? 0
                : defaultGroups.get(modelName) ?? 0;
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
        if (!name || state.models.includes(name) || !activeKey) return;
        let protocols = 0;
        for (const modelName of state.models) protocols |= state.grants.get(grantKey(modelName, activeKey)) ?? 0;
        const grants = new Map(state.grants);
        grants.set(grantKey(name, activeKey), protocols || Protocol.OpenAIResponse);
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
        setSelectedModels((previous) => {
            const next = new Set(previous);
            for (const modelName of visibleModels) {
                if (next.has(modelName)) next.delete(modelName); else next.add(modelName);
            }
            return next;
        });
        setSelectionOverrides((previous) => {
            const next = new Set(previous);
            for (const modelName of visibleModels) {
                if (selectedModelSet.has(modelName)) next.add(modelName); else next.delete(modelName);
            }
            return next;
        });
    };

    const handleAssign = () => {
        if (!channelId || selectedVisibleModels.length === 0) return;
        assignModels.mutate({
            channelId,
            assignments: selectedVisibleModels.map((modelName) => ({ model_name: modelName, group_id: targetGroupID(modelName) })),
        }, {
            onSuccess: (results) => {
                const assigned = results.filter((result) => !result.skipped && result.grant_count > 0).length;
                const skipped = results.filter((result) => result.skipped || result.grant_count === 0).length;
                toast.success(t('modelAssignDone', { assigned, skipped }));
                setSelectedModels(new Set());
                setSelectionOverrides((previous) => new Set([...previous, ...selectedVisibleModels]));
            },
            onError: (error) => toast.error(t('modelAssignFailed'), { description: error.message }),
        });
    };

    const handleProbe = (modelNames: string[]) => {
        if (!channelId || modelNames.length === 0) return;
        setProbingModels((previous) => new Set([...previous, ...modelNames]));
        probeModels.mutate({ channelId, modelNames, keyName: isAllKeys ? '' : selectedKey }, {
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
    };

    if (state.keys.length === 0) {
        return <p className="flex h-full items-center justify-center text-sm text-muted-foreground">{t('keysRequiredFirst')}</p>;
    }

    return (
        <div className="relative flex flex-col gap-3 h-full min-h-0">
            <div className="flex items-center gap-2 overflow-x-auto shrink-0">
                <select value={selectedKey} onChange={(event) => setSelectedKey(event.target.value)} className="h-8 w-28 shrink-0 rounded-lg border border-input bg-transparent px-2 text-xs outline-none focus:border-input focus:ring-0">
                    <option value={ALL_KEYS}>{t('grantAllKeys')}</option>
                    {state.keys.map((key) => <option key={key.name} value={key.name}>{key.name}</option>)}
                </select>
                <Input
                    value={searchTerm}
                    onChange={(event) => setSearchTerm(event.target.value)}
                    placeholder={t('modelSearchPlaceholder')}
                    className="h-8 min-w-24 flex-1 rounded-lg px-2 text-xs"
                />
                <IconButton onClick={() => setAddDialogOpen(true)} className="size-8 shrink-0" tip={t('modelAdd')}>
                    <Plus className="size-3.5" />
                </IconButton>
                <IconButton onClick={() => probe(state, setState, activeKey)} disabled={pendingKey !== null || !state.base_url.trim()} className="size-8 shrink-0" tip={t('modelRefresh')}>
                    <RefreshCw className={`size-3.5 ${pendingKey !== null ? 'animate-spin' : ''}`} />
                </IconButton>
                <span className="mx-1 h-5 w-px shrink-0 bg-border" />
                <IconButton onClick={toggleAll} disabled={visibleModels.length === 0} className="size-8 shrink-0" tip={t('modelSelectAll')}><CheckCheck className="size-3.5" /></IconButton>
                <IconButton onClick={invertSelection} disabled={visibleModels.length === 0} className="size-8 shrink-0" tip={t('modelInvertSelection')}><ArrowDownUp className="size-3.5" /></IconButton>
                <IconButton onClick={handleAssign} disabled={!channelId || selectedVisibleModels.length === 0 || assignModels.isPending} className="size-8 shrink-0" tip={t('modelAssignSelected')}>
                    {assignModels.isPending ? <LoaderCircle className="size-3.5 animate-spin" /> : <FolderPlus className="size-3.5" />}
                </IconButton>
                <IconButton onClick={() => handleProbe(selectedVisibleModels)} disabled={!channelId || selectedVisibleModels.length === 0 || probeModels.isPending} className="size-8 shrink-0" tip={t('modelProbeSelected')}>
                    {probeModels.isPending ? <LoaderCircle className="size-3.5 animate-spin" /> : <HeartPulse className="size-3.5" />}
                </IconButton>
                <span className="ml-auto shrink-0 px-1 text-[11px] text-muted-foreground tabular-nums">{selectedVisibleModels.length}/{visibleModels.length}</span>
            </div>

            <div className="flex-1 min-h-0 flex flex-col rounded-xl border border-border overflow-hidden">
                <div className="flex items-center gap-1 px-3 py-2 border-b border-border bg-muted/30 shrink-0">
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

                <div className="flex-1 min-h-0 overflow-y-auto overscroll-contain">
                    {visibleModels.length === 0 ? (
                        <p className="px-3 py-6 text-center text-sm text-muted-foreground">{state.models.length === 0 ? t('modelNoSelected') : t('modelNoneForKey')}</p>
                    ) : visibleModels.map((modelName) => {
                        const isOpen = expanded.has(modelName);
                        const granted = keyNames.filter((keyName) => (state.grants.get(grantKey(modelName, keyName)) ?? 0) !== 0).length;
                        const mark = Object.entries(probeMarks).find(([key]) => key.startsWith(`${modelName}\0`))?.[1];
                        return (
                            <div key={modelName} className="border-b border-border last:border-0">
                                <div className="flex items-center gap-1 px-3 py-2">
                                    <Checkbox checked={selectedModelSet.has(modelName)} onCheckedChange={(checked) => {
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
                                    }} aria-label={t('modelSelect', { model: modelName })} />
                                    <button type="button" onClick={() => setExpanded((previous) => {
                                        const next = new Set(previous);
                                        if (!next.delete(modelName)) next.add(modelName);
                                        return next;
                                    })} className="flex items-center gap-1.5 min-w-0 flex-1 text-left">
                                        <ChevronRight className={`size-3.5 shrink-0 text-muted-foreground transition-transform ${isOpen ? 'rotate-90' : ''}`} />
                                        <span className="text-sm truncate">{modelName}</span>
                                        {isAllKeys && <span className="text-xs text-muted-foreground tabular-nums shrink-0">{granted}/{keyNames.length}</span>}
                                    </button>
                                    <select value={targetGroupValue(modelName)} onChange={(event) => {
                                        const value = event.target.value;
                                        const groupID = value === '' ? null : Number(value);
                                        setManualGroups((previous) => ({ ...previous, [modelName]: groupID }));
                                        setSelectedModels((previous) => {
                                            const next = new Set(previous);
                                            if (groupID && groupID > 0) next.add(modelName); else next.delete(modelName);
                                            return next;
                                        });
                                        setSelectionOverrides((previous) => {
                                            const next = new Set(previous);
                                            if (groupID && groupID > 0) next.delete(modelName); else next.add(modelName);
                                            return next;
                                        });
                                    }} aria-label={t('modelGroupFor', { model: modelName })} className="h-8 w-32 shrink-0 rounded-lg border border-input bg-background px-2 text-xs outline-none focus:ring-2 focus:ring-ring">
                                        <option value="">{t('modelGroupUnmatched')}</option>
                                        {groups.map((group) => <option key={group.id} value={String(group.id)}>{group.name}</option>)}
                                        <option value="0">{t('modelGroupNone')}</option>
                                    </select>
                                    <IconButton
                                        onClick={() => handleProbe([modelName])}
                                        disabled={!channelId || probingModels.has(modelName)}
                                        className={`size-8 shrink-0 ${mark ? (mark.ok ? 'text-emerald-500' : 'text-destructive') : ''}`}
                                        tip={mark ? (mark.ok ? t('modelProbePassed', { ms: mark.latency_ms }) : t('modelProbeFailed')) : t('modelProbeOne')}
                                    >
                                        {probingModels.has(modelName) ? <LoaderCircle className="size-3.5 animate-spin" /> : <HeartPulse className="size-3.5" />}
                                    </IconButton>
                                    <GrantCells state={state} setState={setState} models={[modelName]} keyNames={isAllKeys ? keyNames : [selectedKey]} remove={isAllKeys ? () => removeModel(modelName) : () => removeGrant(modelName, selectedKey)} icon={Trash2} tip={isAllKeys ? t('modelRemove') : t('grantRemove')} />
                                </div>
                                {isOpen && state.keys.map((channelKey) => {
                                    const protocols = state.grants.get(grantKey(modelName, channelKey.name)) ?? 0;
                                    return (
                                        <div key={channelKey.name} className={`flex items-center gap-1 pl-14 pr-3 py-1.5 bg-muted/20 ${protocols === 0 ? 'opacity-45' : ''}`}>
                                            <span className="flex-1 text-xs text-muted-foreground truncate">{channelKey.name}</span>
                                            <GrantCells state={state} setState={setState} models={[modelName]} keyNames={[channelKey.name]} remove={protocols !== 0 ? () => removeGrant(modelName, channelKey.name) : undefined} icon={Trash2} tip={t('grantRemove')} />
                                        </div>
                                    );
                                })}
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
