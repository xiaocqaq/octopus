import { useEffect, useState } from 'react';
import { useTranslations } from 'use-intl';
import { toast } from 'sonner';
import { Settings } from 'lucide-react';
import { SettingKey, useSetSetting, useSettingList } from '@/api/setting';
import { Button, buttonVariants } from '@/components/ui/button';
import { Field, FieldDescription, FieldGroup, FieldLabel } from '@/components/ui/field';
import { Input } from '@/components/ui/input';
import {
    MorphingDialog,
    MorphingDialogContainer,
    MorphingDialogContent,
    MorphingDialogDescription,
    MorphingDialogTitle,
    MorphingDialogTrigger,
    useMorphingDialog,
} from '@/components/ui/morphing-dialog';

// 题面是长文本, 而 ui 目录里没有 textarea 组件: 按 Input 的边框与聚焦样式手写一个,
// 只把高度与换行打开, 免得同一个弹窗里出现两种输入框观感。
const TEXTAREA_CLASS =
    'min-h-32 w-full resize-y rounded-xl border border-input bg-transparent px-3 py-2 text-sm shadow-xs transition-[color,box-shadow] outline-none placeholder:text-muted-foreground focus-visible:border-ring focus-visible:ring-[3px] focus-visible:ring-ring/50 disabled:cursor-not-allowed disabled:opacity-50 dark:bg-input/30';

// IQSettingsDialogContent 渲染糖果测试的设置: 题面与标准答案。
//
// 为什么要能改: 题目本身只是"随便问一道"的探针, 换题、换答案属于日常调整,
// 不该为了改一句话重新编译部署。判分口径也随之变成"回答里出现标准答案就算过"(见后端 gradeIQAnswer),
// 所以这两个值必须成对地由用户掌握 —— 只有题面可改而答案写死, 换题就等于全判错。
export function IQSettingsDialogContent() {
    const t = useTranslations('scheduledProbe');
    const { setIsOpen } = useMorphingDialog();
    const { data: settings } = useSettingList();
    const setSetting = useSetSetting();

    const [prompt, setPrompt] = useState('');
    const [answer, setAnswer] = useState('');
    // 设置列表是异步到的, 首次渲染时还没有值 —— 直接回填会把空串写进输入框, 用户一保存就把题面清空了。
    // 因此只在"确实拿到了值"之后灌一次, 之后输入框归用户。
    const [loaded, setLoaded] = useState(false);

    useEffect(() => {
        if (loaded || !settings) return;
        const values = new Map(settings.map((setting) => [setting.key, setting.value]));
        setPrompt(values.get(SettingKey.IQProbePrompt) ?? '');
        setAnswer(values.get(SettingKey.IQProbeAnswer) ?? '');
        setLoaded(true);
    }, [loaded, settings]);

    // 题面与答案都不能为空: 空题面会让探测请求变成一条没有内容的对话, 空答案会让判分永远不通过,
    // 两者都会把"设置没填"表现成"模型降智"。后端也各自拦一道(model.Setting.Validate)。
    const canSave = prompt.trim() !== '' && answer.trim() !== '' && !setSetting.isPending;

    const handleSubmit = async (event: React.FormEvent<HTMLFormElement>) => {
        event.preventDefault();
        if (!canSave) return;
        try {
            // 两次写分开: 后端一次只收一个 key(与设置页其他项同一套接口), 任一失败就整体报错。
            await setSetting.mutateAsync({ key: SettingKey.IQProbePrompt, value: prompt.trim() });
            await setSetting.mutateAsync({ key: SettingKey.IQProbeAnswer, value: answer.trim() });
            toast.success(t('iqSettings.saved'));
            setIsOpen(false);
        } catch (error) {
            toast.error((error as Error).message);
        }
    };

    return (
        // 这一层得自己就是可收缩的 flex 列: 弹窗高度被 max-h 卡住后, flex 子项默认 min-height:auto
        // 会拒绝缩到内容高度以下, 于是底部的保存按钮被顶出弹窗、再被外层 overflow-hidden 裁掉。
        <MorphingDialogDescription className="flex min-h-0 flex-1 flex-col">
            <MorphingDialogTitle className="shrink-0 pb-3 text-sm font-medium">{t('iqSettings.title')}</MorphingDialogTitle>
            <form onSubmit={handleSubmit} className="flex min-h-0 flex-1 flex-col">
                <div className="min-h-0 flex-1 overflow-y-auto overscroll-contain pr-1">
                    <FieldGroup className="gap-4">
                        <Field>
                            <FieldLabel htmlFor="iq-probe-prompt">{t('iqSettings.prompt')}</FieldLabel>
                            <textarea
                                id="iq-probe-prompt"
                                value={prompt}
                                onChange={(event) => setPrompt(event.target.value)}
                                placeholder={t('iqSettings.promptPlaceholder')}
                                spellCheck={false}
                                rows={8}
                                className={TEXTAREA_CLASS}
                            />
                            <FieldDescription>{t('iqSettings.promptHint')}</FieldDescription>
                        </Field>
                        <Field>
                            <FieldLabel htmlFor="iq-probe-answer">{t('iqSettings.answer')}</FieldLabel>
                            <Input
                                id="iq-probe-answer"
                                value={answer}
                                onChange={(event) => setAnswer(event.target.value)}
                                placeholder={t('iqSettings.answerPlaceholder')}
                                className="rounded-xl"
                            />
                            <FieldDescription>{t('iqSettings.answerHint')}</FieldDescription>
                        </Field>
                    </FieldGroup>
                </div>
                <div className="mt-4 flex shrink-0 gap-2">
                    <Button type="button" variant="secondary" onClick={() => setIsOpen(false)} className="h-11 flex-1 rounded-xl">
                        {t('cancel')}
                    </Button>
                    <Button type="submit" disabled={!canSave} className="h-11 flex-1 rounded-xl">
                        {t('iqSettings.save')}
                    </Button>
                </div>
            </form>
        </MorphingDialogDescription>
    );
}

// IQSettingsButton 是模型监控页顶栏的设置入口: 一个齿轮, 与旁边的「+」同一套观感。
// 为什么放在顶栏而不是卡片里: 题面与答案是全局的, 只此一份, 放在某张卡片上会让人以为改的是那一条。
export function IQSettingsButton() {
    const t = useTranslations('scheduledProbe');
    return (
        <MorphingDialog>
            <MorphingDialogTrigger
                className={buttonVariants({ variant: 'ghost', size: 'icon', className: 'rounded-xl transition-none text-muted-foreground hover:bg-transparent hover:text-foreground' })}
                aria-label={t('iqSettings.open')}
            >
                <Settings className="size-4 transition-colors duration-300" />
            </MorphingDialogTrigger>
            <MorphingDialogContainer>
                <MorphingDialogContent
                    dismissOnClickOutside={false}
                    className="relative flex h-[calc(100dvh-2rem)] w-screen max-w-full flex-col overflow-hidden rounded-3xl bg-card px-4 py-3 text-card-foreground md:h-auto md:max-h-[calc(100vh-2rem)] md:max-w-2xl md:px-6 md:py-4"
                >
                    <IQSettingsDialogContent />
                </MorphingDialogContent>
            </MorphingDialogContainer>
        </MorphingDialog>
    );
}
