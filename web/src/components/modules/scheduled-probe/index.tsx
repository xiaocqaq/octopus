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
import { Item } from './Item';

// ScheduledProbeActions 向稳定顶栏提供新建入口。
// 这里没有搜索与视图选项: 列表是'几条到几十条'的手工维护清单, 排序由后端的轮转顺序定稿,
// 加上排序与筛选反而会让人以为改的是轮转次序。
export function ScheduledProbeActions() {
    const t = useTranslations('scheduledProbe');

    return (
        <div className="flex items-center gap-2">
            <MorphingDialog>
                <MorphingDialogTrigger
                    className={buttonVariants({ variant: 'ghost', size: 'icon', className: 'rounded-xl transition-none text-muted-foreground hover:bg-transparent hover:text-foreground' })}
                    aria-label={t('createTitle')}
                >
                    <Plus className="size-4 transition-colors duration-300" />
                </MorphingDialogTrigger>
                <MorphingDialogContainer>
                    <MorphingDialogContent
                        dismissOnClickOutside={false}
                        className="flex max-h-[calc(100vh-2rem)] w-fit max-w-full flex-col overflow-hidden rounded-3xl bg-card px-6 py-4 text-card-foreground"
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
            // 一行放 3~4 个: 卡片头撤掉间隔徽标后, 每张卡只需要容纳"标题 + 几行凭据",
            // 宽屏下排到 4 列仍看得清每行的渠道名/凭据名, 一屏能扫到的任务数却翻了一倍。
            // md(768) 起就上 3 列而不等 lg: 这一档宽度已能让"渠道名/凭据名 + 结论"完整显示。
            columns={{ default: 1, sm: 2, md: 3, xl: 4 }}
            // 卡片头一行 + 两条凭据行的高度; 实际高度由 VirtualizedGrid 自行测量。
            estimateItemHeight={156}
            getItemKey={(probe) => `scheduled-probe-${probe.id}`}
            renderItem={(probe) => <Item key={probe.id} probe={probe} />}
        />
    );
}
