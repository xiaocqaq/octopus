import { Fragment } from 'react';
import { useTranslations } from 'use-intl';
import { Tabs, TabsList, TabsTrigger } from '@/components/ui/tabs';
import type { MetricKey } from './store';

// MetricTabs 渲染以 / 分隔的统计维度切换, 供趋势图使用。
// 排行榜已改为三指标同时展示, 不再需要维度切换。
export function MetricTabs({ value, onChange }: { value: MetricKey; onChange: (value: MetricKey) => void }) {
    const t = useTranslations('home.metric');

    return (
        <Tabs value={value} onValueChange={(next) => onChange(next as MetricKey)}>
            <TabsList variant="text" className="p-0">
                {/* 首尾只留内侧内边距, 使 / 分隔号与文字贴合。 */}
                {([['cost', 'pr-0'], ['count', 'px-0'], ['tokens', 'pl-0']] as const).map(([key, padding], index) => (
                    <Fragment key={key}>
                        {index > 0 && (
                            <span aria-hidden="true" className="mx-1 inline-flex h-full -translate-y-px items-center text-sm font-medium leading-none text-muted-foreground/50">/</span>
                        )}
                        <TabsTrigger value={key} className={padding}>{t(key)}</TabsTrigger>
                    </Fragment>
                ))}
            </TabsList>
        </Tabs>
    );
}
