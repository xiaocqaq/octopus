import type { LucideIcon } from 'lucide-react';
import { LoaderCircle } from 'lucide-react';
import { IconButton } from '@/components/common/IconButton';
import { cn } from '@/lib/utils';

// 本文件是「结论只用颜色表达」这条约定的唯一落点。
//
// 一行成员里要塞下渠道 / 凭据 / 模型三段身份, 再挂上"通过 · 6792ms"或"降智"这样的正文,
// 身份信息就会被挤没(实测过: 成员行里的副标题整段消失)。所以结论收进图标的颜色里:
// 颜色负责扫视 —— 一眼看出哪条绿、哪条红; 悬停负责核对 —— 具体耗时、错误正文、模型答了几,
// 只在那一个 tooltip 里出现。
//
// 图标本身也是按钮: 点它就是发起这次测试。把"结论"与"入口"合成一个元素,
// 是为了不在同一行里放两套同样的图标(一个显示结论、一个用来点击), 那会让人分不清该点哪个。

// VerdictTone 是结论的色调。
//
// 分四档而不是两档: 心跳有"通 / 不通 / 没测过"三态, 糖果有"正常 / 降智 / 没测过"三态,
// 而"没测过"必须是中性色 —— 给它上绿色等于替模型宣布了一个它还没拿到的结论。
// dumb 与 bad 分开: 答错是"模型笨"(琥珀), 请求失败是"没答上话"(玫红), 混成一色就分不清了。
export type VerdictTone = 'ok' | 'dumb' | 'bad' | 'idle';

// TONE_CLASS 连 hover 一起写死: IconButton 的基础样式带 hover:text-foreground,
// 只在常态给颜色的话, 指针一放到图标上结论色就被抹成前景色 —— 而那一刻用户正要看 tooltip。
const TONE_CLASS: Record<VerdictTone, string> = {
    ok: 'text-emerald-600 hover:text-emerald-600 dark:text-emerald-400 dark:hover:text-emerald-400',
    dumb: 'text-amber-600 hover:text-amber-600 dark:text-amber-400 dark:hover:text-amber-400',
    bad: 'text-rose-600 hover:text-rose-600 dark:text-rose-400 dark:hover:text-rose-400',
    idle: '',
};

// ResultIconButton 是"既是按钮又是结论"的图标: 点它发起测试, 颜色表示上一次的结论, 详情在悬停里。
export function ResultIconButton({
    icon: Icon,
    tone,
    tip,
    label,
    pending,
    className,
    onClick,
}: {
    icon: LucideIcon;
    tone: VerdictTone; // 上一次的结论; 从没测过传 idle。
    tip: string; // 悬停提示 —— 结论详情唯一的出口。
    label: string; // 无障碍名称; 图标没有文字, 不给它读屏就只剩一个无名按钮。
    pending?: boolean;
    className?: string;
    onClick: (event: React.MouseEvent<HTMLButtonElement>) => void;
}) {
    return (
        <IconButton
            onClick={onClick}
            disabled={pending}
            tip={tip}
            aria-label={label}
            className={cn('size-6 shrink-0', TONE_CLASS[tone], className)}
        >
            {pending
                ? <LoaderCircle className="size-3 animate-spin" />
                : <Icon className="size-3" />}
        </IconButton>
    );
}

// describeIQ 把糖果题的结论说成一句话, 供悬停提示使用。
//
// "没答出数字"单列一句: 它与"答错"在结论上同属降智, 原因却不同(一个是算错, 一个是连数都没给),
// 并成同一句话会让人以为模型至少交出了一个数, 而实际拿到的是满篇废话。
export function describeIQ(
    iq: { correct: boolean; answer: string },
    t: (key: string, values?: Record<string, string | number>) => string,
) {
    if (iq.correct) return t('iqNormalHint', { answer: iq.answer });
    if (iq.answer === '') return t('iqDumbNoAnswerHint');
    return t('iqDumbHint', { answer: iq.answer });
}

// describeProbe 把心跳的结论说成一句话, 供悬停提示使用。
// 失败时把上游正文原样带上: 排查时唯一有用的就是那句话, 界面上再包一层"测活失败"就把它盖住了。
export function describeProbe(
    probe: { ok: boolean; latency_ms: number; message: string },
    t: (key: string, values?: Record<string, string | number>) => string,
) {
    return probe.ok
        ? t('probeOk', { ms: probe.latency_ms })
        : t('probeFailed', { message: probe.message || t('probeUnknownError') });
}
