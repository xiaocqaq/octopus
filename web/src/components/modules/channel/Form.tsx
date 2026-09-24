import { useEffect, useRef, useState } from 'react';
import { ChevronLeft, Plus, Trash2 } from 'lucide-react';
import { toast } from 'sonner';
import { useTranslations } from 'use-intl';
import {
    type ChannelDetail,
    useChannelDetail,
    useCreateChannel,
    useUpdateChannel,
} from '@/api/channel';
import { CHANNEL_PRESETS, IMG_EDIT, IMG_GEN, type ChannelPreset } from '@/lib/channel-presets';
import { useMorphingDialog } from '@/components/ui/morphing-dialog';
import { Button } from '@/components/ui/button';
import { Input } from '@/components/ui/input';
import { Label } from '@/components/ui/label';
import { Switch } from '@/components/ui/switch';
import { FormGrants } from './FormGrants';
import { FormKeys } from './FormKeys';
import { IconButton } from '@/components/common/IconButton';
import {
    emptyFormState,
    fromChannel,
    toChannelDetail,
    type ChannelFormState,
} from './state';

type StepID = 'preset' | 'connection' | 'keys' | 'grants' | 'advanced';

// ChannelForm 新建与编辑共用; 编辑时按 id 取回整份配置, 到齐后再进表单。
// 配置不随渠道列表下发, 故编辑必然要等这一趟请求; 占位与表单同高, 弹窗不会因此跳动。
export function ChannelForm({ channelId, onBack }: {
    channelId?: number;
    onBack?: () => void; // 编辑态由统计视图切入, 给出返回入口; 新建态没有可返回的视图。
}) {
    const t = useTranslations('channel.form');
    const { data: detail, isPending, isError } = useChannelDetail(channelId);

    if (channelId === undefined) {
        return <ChannelFormFields />;
    }
    if (!detail) {
        return (
            <div className="flex items-center justify-center h-[min(29rem,calc(100vh-10rem))]">
                <p className="text-sm text-muted-foreground">
                    {isError ? t('detailFailed') : isPending ? t('detailLoading') : null}
                </p>
            </div>
        );
    }
    return <ChannelFormFields channel={detail} onBack={onBack} />;
}

// ChannelFormFields 承载表单本体; 初始状态在挂载时定稿, 故必须等配置到齐后才渲染。
// 新建从模板步骤起, 编辑跳过模板直接进连接步骤; 步骤顺序即数据依赖顺序:
// 模板给出地址与路径 -> 拉取需要地址与凭据 -> 授权引用凭据与模型。
function ChannelFormFields({ channel, onBack }: { channel?: ChannelDetail; onBack?: () => void }) {
    const t = useTranslations('channel.form');
    const { setIsOpen } = useMorphingDialog();
    const createChannel = useCreateChannel();
    const updateChannel = useUpdateChannel();
    const [state, setState] = useState<ChannelFormState>(channel ? fromChannel(channel) : emptyFormState);
    const [step, setStep] = useState<StepID>(channel ? 'connection' : 'preset');
    const [channelId, setChannelId] = useState(channel?.id);
    const stateRef = useRef(state);
    const idRef = useRef(channel?.id);
    const dirtyRef = useRef(false);
    const persistChain = useRef(Promise.resolve());
    const saveRef = useRef<() => Promise<number | undefined>>(async () => undefined);
    const isPending = createChannel.isPending || updateChannel.isPending;
    // 新建与编辑弹窗可同时存在, 控件 id 需按渠道隔离。
    const idPrefix = channel ? `channel-${channel.id}` : 'new-channel';

    // 凭据一旦填完就落库，不要求先有模型，也不必再点底部保存。底部保存只负责收尾关闭。
    const persistable = (next: ChannelFormState) => next.name.trim() !== ''
        && next.base_url.trim() !== ''
        && [
            next.openai_chat_completion_path,
            next.openai_response_path,
            next.anthropic_message_path,
            next.openai_image_generation_path,
            next.openai_image_edit_path,
        ].every((path) => path === '' || path.startsWith('/'))
        && next.keys.some((key) => key.name.trim() !== '' && key.key.trim() !== '');

    const ensureSaved = () => {
        const run = persistChain.current.then(async () => {
            const next = stateRef.current;
            if (!persistable(next)) return idRef.current;
            const id = idRef.current ?? 0;
            const saved = id
                ? await updateChannel.mutateAsync(toChannelDetail(next, id))
                : await createChannel.mutateAsync(toChannelDetail(next, 0));
            idRef.current = saved.id;
            setChannelId(saved.id);
            dirtyRef.current = false;
            return saved.id;
        }).catch((error: unknown) => {
            toast.error(t('saveFailed'), { description: error instanceof Error ? error.message : undefined });
            throw error;
        });
        persistChain.current = run.then(() => undefined, () => undefined);
        return run;
    };

    const updateState = (next: ChannelFormState) => {
        dirtyRef.current = true;
        setState(next);
    };

    useEffect(() => {
        stateRef.current = state;
        saveRef.current = ensureSaved;
    });

    useEffect(() => {
        if (!dirtyRef.current || !persistable(state)) return;
        const timer = window.setTimeout(() => { void saveRef.current().catch(() => undefined); }, 400);
        return () => window.clearTimeout(timer);
    }, [state]);

    const steps: { id: StepID; label: string }[] = [
        ...(channel ? [] : [{ id: 'preset' as StepID, label: t('stepPreset') }]),
        { id: 'connection', label: t('stepConnection') },
        { id: 'keys', label: t('stepKeys') },
        { id: 'grants', label: t('stepGrants') },
        { id: 'advanced', label: t('stepAdvanced') },
    ];

    const applyPreset = (preset: ChannelPreset) => {
        updateState({
            ...state,
            name: state.name || preset.label,
            dialect: preset.dialect,
            base_url: preset.base_url,
            openai_chat_completion_path: preset.openai_chat_completion_path,
            openai_response_path: preset.openai_response_path,
            anthropic_message_path: preset.anthropic_message_path,
            openai_image_generation_path: preset.openai_image_generation_path ?? IMG_GEN,
            openai_image_edit_path: preset.openai_image_edit_path ?? IMG_EDIT,
        });
        setStep('connection');
    };

    // 新建与编辑都一趟完成且都提交整份配置: 授权按名称引用, 与凭据和模型在同一次请求里原子生效。
    const submit = (event: React.FormEvent<HTMLFormElement>) => {
        event.preventDefault();
        if (!persistable(state)) return;
        void ensureSaved().then(() => setIsOpen(false)).catch(() => undefined);
    };

    // 表单高度固定, 否则切换步骤时弹窗会随内容高度跳动; 内容更高的步骤由步骤区内部滚动消化。
    // 29rem 是连接页恰好铺满所需: 首行留白 8px + 五个字段组 290px + 五道间距 80px + 开关行 20px + 底部按钮 52px。
    // 各步骤首行统一落在同一水平线: connection 与 advanced 的首行是无边框文案, 补 8px 才能与步骤导航的按钮文案对齐;
    // keys 与 grants 的首行是 36px 控件行, 文案居中后天然齐平, 无需补白。步骤区自带 4px 内边距供焦点环显示。
    // calc 一项夹住矮屏, 弹窗不提供滚动, 内容超出视口时底部按钮会点不到。详情视图取同一高度以对齐尺寸。
    return (
        <form onSubmit={submit} className="flex flex-col gap-3 md:flex-row md:gap-6 h-[min(29rem,calc(100dvh-5rem))]">
            <nav className="md:w-28 shrink-0 flex md:flex-col gap-1 overflow-x-auto pt-1">
                {steps.map((s) => (
                    <button
                        key={s.id}
                        type="button"
                        onClick={() => setStep(s.id)}
                        className={`rounded-xl px-3 py-2 text-left text-sm font-medium whitespace-nowrap transition-colors ${step === s.id ? 'bg-muted' : 'text-muted-foreground hover:bg-muted/50'}`}
                    >
                        {s.label}
                    </button>
                ))}
                {/* 返回统计的入口与步骤同列, 顶到底部与步骤拉开距离, 表明它不是其中一步。
                    窄屏下步骤横向排布, 此时它跟在末尾, mt-auto 不生效。 */}
                {onBack && (
                    <button
                        type="button"
                        onClick={onBack}
                        className="md:mt-auto flex items-center gap-1.5 rounded-xl px-3 py-2 text-left text-sm whitespace-nowrap text-muted-foreground transition-colors hover:bg-muted/50"
                    >
                        <ChevronLeft className="size-4 shrink-0" />
                        <span className="hidden md:inline">{t('backToStats')}</span>
                    </button>
                )}
            </nav>

            <div className="flex-1 min-w-0 flex flex-col min-h-0">
                <div className="flex-1 min-h-0 overflow-y-auto overscroll-contain p-1">
                    {step === 'preset' && (
                        <div className="grid grid-cols-1 sm:grid-cols-2 gap-2">
                            {CHANNEL_PRESETS.map((preset) => (
                                <button
                                    key={preset.id}
                                    type="button"
                                    onClick={() => applyPreset(preset)}
                                    className="flex items-center gap-4 rounded-xl border border-border px-5 py-4 hover:bg-muted/50 transition-colors"
                                >
                                    <preset.Icon className={`size-7 shrink-0 ${preset.iconClassName ?? ''}`} />
                                    <span className="text-base font-medium truncate">{preset.label}</span>
                                </button>
                            ))}
                        </div>
                    )}

                    {step === 'connection' && (
                        <div className="space-y-4 pt-2">
                            <div className="space-y-2">
                                <Label htmlFor={`${idPrefix}-name`}>{t('name')}</Label>
                                <Input
                                    id={`${idPrefix}-name`}
                                    value={state.name}
                                    onChange={(e) => updateState({ ...state, name: e.target.value })}
                                    className="rounded-xl"
                                    required
                                />
                            </div>
                            <div className="space-y-2">
                                <Label htmlFor={`${idPrefix}-base-url`}>{t('baseUrl')}</Label>
                                <Input
                                    id={`${idPrefix}-base-url`}
                                    type="url"
                                    value={state.base_url}
                                    onChange={(e) => updateState({ ...state, base_url: e.target.value })}
                                    className="rounded-xl"
                                    required
                                />
                            </div>
                            {([
                                ['openai_chat_completion_path', 'OpenAI Chat'],
                                ['openai_response_path', 'OpenAI Responses'],
                                ['anthropic_message_path', 'Anthropic'],
                                ['openai_image_generation_path', 'OpenAI Image Generation'],
                                ['openai_image_edit_path', 'OpenAI Image Edit'],
                            ] as const).map(([field, label]) => (
                                <div key={field} className="space-y-2">
                                    <Label htmlFor={`${idPrefix}-${field}`}>{label}</Label>
                                    <Input
                                        id={`${idPrefix}-${field}`}
                                        value={state[field]}
                                        onChange={(e) => updateState({ ...state, [field]: e.target.value })}
                                        aria-invalid={state[field] !== '' && !state[field].startsWith('/')}
                                        className="rounded-xl font-mono text-sm"
                                    />
                                </div>
                            ))}
                            <div className="flex flex-wrap items-center gap-6">
                                {([['enabled', t('enabled')], ['proxy', t('proxy')]] as const).map(([field, label]) => (
                                    <label key={field} className="flex items-center gap-2 cursor-pointer">
                                        <Switch
                                            checked={state[field]}
                                            onCheckedChange={(checked) => updateState({ ...state, [field]: checked })}
                                        />
                                        <span className="text-sm">{label}</span>
                                    </label>
                                ))}
                            </div>
                        </div>
                    )}

                    {step === 'keys' && <FormKeys state={state} setState={updateState} />}
                    {step === 'grants' && <FormGrants state={state} setState={updateState} channelId={channelId} ensureSaved={ensureSaved} />}

                    {step === 'advanced' && (
                        <div className="space-y-4 pt-2">
                            {([
                                ['channel_proxy', t('channelProxy')],
                                ['match_regex', t('matchRegex')],
                            ] as const).map(([field, label]) => (
                                <div key={field} className="space-y-2">
                                    <Label htmlFor={`${idPrefix}-${field}`}>{label}</Label>
                                    <Input
                                        id={`${idPrefix}-${field}`}
                                        value={state[field]}
                                        onChange={(e) => updateState({ ...state, [field]: e.target.value })}
                                        className="rounded-xl"
                                    />
                                </div>
                            ))}
                            <div className="space-y-2">
                                <Label htmlFor={`${idPrefix}-param-override`}>{t('paramOverride')}</Label>
                                <textarea
                                    id={`${idPrefix}-param-override`}
                                    value={state.param_override}
                                    onChange={(e) => updateState({ ...state, param_override: e.target.value })}
                                    className="min-h-24 w-full rounded-xl border border-border bg-background px-3 py-2 text-sm focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring"
                                />
                            </div>

                            <div className="space-y-2">
                                <div className="flex items-center justify-between">
                                    <Label>{t('customHeader')}</Label>
                                    <IconButton
                                        onClick={() => updateState({
                                            ...state,
                                            custom_header: [...state.custom_header, { header_key: '', header_value: '' }],
                                        })}
                                        className="size-9"
                                        tip={t('customHeaderAdd')}
                                    >
                                        <Plus className="size-4" />
                                    </IconButton>
                                </div>
                                {/* 列名只在有行时出现, 两列各占一半, 与下方输入框对齐; 末尾留出删除按钮的宽度。 */}
                                {state.custom_header.length > 0 && (
                                    <div className="flex items-center gap-2">
                                        <Label className="flex-1 text-muted-foreground">{t('customHeaderKey')}</Label>
                                        <Label className="flex-1 text-muted-foreground">{t('customHeaderValue')}</Label>
                                        <span className="size-9 shrink-0" />
                                    </div>
                                )}
                                {state.custom_header.map((header, idx) => (
                                    <div key={idx} className="flex items-center gap-2">
                                        {(['header_key', 'header_value'] as const).map((field) => (
                                            <Input
                                                key={field}
                                                value={header[field]}
                                                onChange={(e) => updateState({
                                                    ...state,
                                                    custom_header: state.custom_header.map((h, i) =>
                                                        i === idx ? { ...h, [field]: e.target.value } : h),
                                                })}
                                                className="rounded-xl flex-1"
                                            />
                                        ))}
                                        <IconButton
                                            onClick={() => updateState({
                                                ...state,
                                                custom_header: state.custom_header.filter((_, i) => i !== idx),
                                            })}
                                            className="size-9 hover:text-destructive"
                                            tip={t('delete')}
                                        >
                                            <Trash2 className="size-4" />
                                        </IconButton>
                                    </div>
                                ))}
                            </div>
                        </div>
                    )}
                </div>

                <div className="shrink-0 flex justify-end gap-2 pt-4">
                    <Button
                        type="button"
                        variant="secondary"
                        onClick={() => setIsOpen(false)}
                        className="rounded-xl h-9 px-4"
                    >
                        {t('cancel')}
                    </Button>
                    <Button type="submit" disabled={isPending || !persistable(state)} className="rounded-xl h-9 px-4">
                        {channel ? t('save') : t('submit')}
                    </Button>
                </div>
            </div>
        </form>
    );
}
