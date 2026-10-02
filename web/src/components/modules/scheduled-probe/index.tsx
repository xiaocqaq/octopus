import { useMemo } from 'react';
import { useTranslations } from 'use-intl';
import { VirtualizedGrid } from '@/components/common/VirtualizedGrid';
import {
    MorphingDialog,
    MorphingDialogContainer,
    MorphingDialogContent,
    MorphingDialogTrigger,
} from '@/components/ui/morphing-dialog';
import { Plus } from 'lucide-react';
import { buttonVariants } from '@/components/ui/button';
import { useScheduledProbeList } from '@/api/scheduled-probe';
import { CreateDialogContent } from './Create';
import { IQSettingsButton } from './IQSettings';
import { Item } from './Item';
import { useProbeClock } from './clock';

// ScheduledProbeActions 向稳定顶栏提供新建入口与糖果测试设置入口。
// 这里没有搜索与视图选项: 列表是'几条到几十条'的手工维护清单, 排序由后端的轮转顺序定稿,
// 加上排序与筛选反而会让人以为改的是轮转次序。
//
// 设置(题面与标准答案)是全局的、只此一份, 所以放在顶栏: 挂到某张卡片上会让人以为改的是那一条。
export function ScheduledProbeActions() {
    const t = useTranslations('scheduledProbe');

    return (
        <div className="flex items-center gap-2">
            <IQSettingsButton />
            <MorphingDialog>
                <MorphingDialogTrigger
                    className={buttonVariants({ variant: 'ghost', size: 'icon', className: 'rounded-xl transition-none text-muted-foreground hover:bg-transparent hover:text-foreground' })}
                    aria-label={t('createTitle')}
                >
                    <Plus className="size-4 transition-colors duration-300" />
                </MorphingDialogTrigger>
                <MorphingDialogContainer>
                    {/* 宽度必须显式给: 弹窗是居中 flex 容器里的子项, 不给宽度就收缩到内容宽度,
                        而表单里的输入框全是 flex-1、本身不产生宽度需求, 于是整只弹窗塌成一条窄缝。
                        与分组页编辑弹窗同一套写法, 只是本表单字段少, 2xl 就够, max-w-full 兜住窄屏。 */}
                    <MorphingDialogContent
                        dismissOnClickOutside={false}
                        className="relative flex h-[calc(100dvh-2rem)] w-screen max-w-full flex-col overflow-hidden rounded-3xl bg-card px-4 py-3 text-card-foreground md:h-auto md:max-h-[calc(100vh-2rem)] md:max-w-2xl md:px-6 md:py-4"
                    >
                        <CreateDialogContent />
                    </MorphingDialogContent>
                </MorphingDialogContainer>
            </MorphingDialog>
        </div>
    );
}

// ScheduledProbe 渲染模型监控任务列表。
// 编辑弹窗由卡片自身管理，就像分组页卡片那样：trigger 必须在 Provider 内才能取 context，
// 自然而然就让每张卡片各挂一个 provider。
export function ScheduledProbe() {
    const t = useTranslations('scheduledProbe');
    const { data: probes } = useScheduledProbeList();
    // 页面级时钟: 各张卡片共享同一个"现在", 用来把「多久以前」算准(见 useProbeClock)。
    const now = useProbeClock();

    // 顺序直接用后端给的（主键升序）: 那就是轮转顺序, 界面上不该重排。
    const items = useMemo(() => probes ?? [], [probes]);

    if (items.length === 0) {
        return (
            <div className="flex h-full min-h-0 items-center justify-center px-6">
                <p className="text-center text-sm text-muted-foreground">{t('empty')}</p>
            </div>
        );
    }

    return (
        <VirtualizedGrid
            items={items}
            layout="grid"
            // 一行 3 个(宽屏): 用户要的是一屏多扫几条任务, 卡片的完整列宽让给数量。
            // 代价是卡片变窄后行内文字必然放不下, 故行内的截断规则另行安排 ——
            // 渠道名与凭据名优先保留, 模型名最后显示、也最先被省略(见 RowList)。
            // 960(lg) 起 3 列而不是更早: 再窄下去连"渠道/凭据"两段都保不住, 省略号会吞掉身份信息。
            columns={{ default: 1, sm: 2, lg: 3 }}
            // 卡片头一行 + 两条凭据行的高度; 实际高度由 VirtualizedGrid 自行测量。
            estimateItemHeight={156}
            getItemKey={(probe) => `scheduled-probe-${probe.id}`}
            renderItem={(probe) => <Item key={probe.id} probe={probe} now={now} />}
        />
    );
}
