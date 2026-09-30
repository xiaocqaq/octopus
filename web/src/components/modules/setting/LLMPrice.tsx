import { useEffect, useState, useRef } from 'react';
import { useTranslations } from 'use-intl';
import { ChevronRight, Clock, DatabaseBackup, DollarSign, RefreshCw } from 'lucide-react';
import { Input } from '@/components/ui/input';
import { Button } from '@/components/ui/button';
import { Tooltip, TooltipContent, TooltipTrigger } from '@/components/ui/tooltip';
import { useSettingList, useSetSetting, SettingKey } from '@/api/setting';
import { useRebuildModelPrice, useUpdateModelPrice, useLastUpdateTime } from '@/api/model';
import { useAppStore } from '@/stores/app';
import { preloadPage } from '@/lib/page-preload';
import { toast } from 'sonner';

export function SettingLLMPrice() {
    const t = useTranslations('setting');
    const setCurrentPage = useAppStore((state) => state.setCurrentPage);
    const { data: settings } = useSettingList();
    const setSetting = useSetSetting();
    const updatePrice = useUpdateModelPrice();
    const rebuildPrice = useRebuildModelPrice();
    const { data: lastUpdateTime } = useLastUpdateTime();

    const [updateInterval, setUpdateInterval] = useState('');
    const [confirmRebuild, setConfirmRebuild] = useState(false);
    const [rebuildCountdown, setRebuildCountdown] = useState(0);
    const initialUpdateInterval = useRef('');

    useEffect(() => {
        if (settings) {
            const interval = settings.find(s => s.key === SettingKey.ModelInfoUpdateInterval);
            if (interval) {
                queueMicrotask(() => setUpdateInterval(interval.value));
                initialUpdateInterval.current = interval.value;
            }
        }
    }, [settings]);

    useEffect(() => {
        if (!confirmRebuild || rebuildCountdown <= 0) return;
        const timer = window.setTimeout(() => setRebuildCountdown((seconds) => seconds - 1), 1000);
        return () => window.clearTimeout(timer);
    }, [confirmRebuild, rebuildCountdown]);

    const handleSave = (key: string, value: string, initialValue: string) => {
        if (value === initialValue) return;

        setSetting.mutate({ key, value }, {
            onSuccess: () => {
                toast.success(t('saved'));
                initialUpdateInterval.current = value;
            }
        });
    };

    const handleManualUpdate = () => {
        updatePrice.mutate(undefined, {
            onSuccess: () => {
                toast.success(t('llmPrice.updateSuccess'));
            },
            onError: () => {
                toast.error(t('llmPrice.updateFailed'));
            }
        });
    };

    const handleRebuild = () => {
        if (rebuildCountdown > 0) return;
        rebuildPrice.mutate(undefined, {
            onSuccess: ({ count }) => {
                setConfirmRebuild(false);
                setRebuildCountdown(0);
                toast.success(t('llmPrice.rebuild.success', { count }));
            },
            onError: () => {
                setConfirmRebuild(false);
                setRebuildCountdown(0);
                toast.error(t('llmPrice.rebuild.failed'));
            }
        });
    };

    const formatLastUpdateTime = (timeStr: string | undefined) => {
        if (!timeStr) return t('llmPrice.neverUpdated');
        const date = new Date(timeStr);
        if (date.getFullYear() === 1) return t('llmPrice.neverUpdated');
        return date.toLocaleString();
    };

    return (
        <div className="rounded-3xl border border-border bg-card p-6 space-y-5">
            <div className="flex items-center justify-between gap-4">
                <h2 className="text-lg font-bold text-card-foreground flex items-center gap-2">
                    <DollarSign className="h-5 w-5" />
                    {t('llmPrice.title')}
                </h2>
                {/* 价格列表页没有自己的导航按钮了, 入口收在这里:
                    这张卡片管的是价格的"更新与重建", 逐条改价在列表页, 两者本就是同一件事的两半。 */}
                <Button
                    variant="outline"
                    size="sm"
                    onClick={() => {
                        preloadPage('model');
                        setCurrentPage('model');
                    }}
                    className="rounded-xl"
                >
                    {t('llmPrice.manage')}
                    <ChevronRight className="size-4" />
                </Button>
            </div>

            {/* 更新间隔 */}
            <div className="flex items-center justify-between gap-4">
                <div className="flex items-center gap-3">
                    <Clock className="h-5 w-5 text-muted-foreground" />
                    <span className="text-sm font-medium">{t('llmPrice.updateInterval.label')}</span>
                </div>
                <Input
                    type="number"
                    value={updateInterval}
                    onChange={(e) => setUpdateInterval(e.target.value)}
                    onBlur={() => handleSave(SettingKey.ModelInfoUpdateInterval, updateInterval, initialUpdateInterval.current)}
                    placeholder={t('llmPrice.updateInterval.placeholder')}
                    className="w-48 rounded-xl"
                />
            </div>

            {/* 手动更新 */}
            <div className="flex items-center justify-between gap-4">
                <div className="flex flex-col gap-1">
                    <div className="flex items-center gap-3">
                        <RefreshCw className="h-5 w-5 text-muted-foreground" />
                        <span className="text-sm font-medium">{t('llmPrice.manualUpdate.label')}</span>
                    </div>
                    <span className="text-xs text-muted-foreground ml-8">
                        {t('llmPrice.lastUpdate')}: {formatLastUpdateTime(lastUpdateTime)}
                    </span>
                </div>
                <Button
                    variant="outline"
                    size="sm"
                    onClick={handleManualUpdate}
                    disabled={updatePrice.isPending}
                    className="rounded-xl"
                >
                    {updatePrice.isPending ? t('llmPrice.manualUpdate.updating') : t('llmPrice.manualUpdate.button')}
                </Button>
            </div>

            {/* 重建价格数据 */}
            <div className="flex items-center justify-between gap-4">
                <div className="flex items-center gap-3">
                    <DatabaseBackup className="h-5 w-5 text-muted-foreground" />
                    <span className="text-sm font-medium">{t('llmPrice.rebuild.label')}</span>
                </div>
                {!confirmRebuild ? (
                    <Button
                        variant="outline"
                        size="sm"
                        onClick={() => {
                            setRebuildCountdown(3);
                            setConfirmRebuild(true);
                        }}
                        disabled={rebuildPrice.isPending}
                        className="rounded-xl"
                    >
                        {t('llmPrice.rebuild.button')}
                    </Button>
                ) : (
                    <div className="flex items-center gap-2">
                        <Button
                            variant="ghost"
                            size="sm"
                            onClick={() => {
                                setConfirmRebuild(false);
                                setRebuildCountdown(0);
                            }}
                            disabled={rebuildPrice.isPending}
                            className="rounded-xl"
                        >
                            {t('llmPrice.rebuild.cancel')}
                        </Button>
                        <Tooltip open>
                            <TooltipTrigger asChild>
                                <span className="inline-flex">
                                    <Button
                                        variant="destructive"
                                        size="sm"
                                        onClick={handleRebuild}
                                        disabled={rebuildCountdown > 0 || rebuildPrice.isPending}
                                        className="min-w-24 rounded-xl"
                                    >
                                        {rebuildPrice.isPending
                                            ? t('llmPrice.rebuild.rebuilding')
                                            : rebuildCountdown > 0
                                                ? `${rebuildCountdown}s`
                                                : t('llmPrice.rebuild.confirm')}
                                    </Button>
                                </span>
                            </TooltipTrigger>
                            <TooltipContent side="top" sideOffset={10} align="center" className="whitespace-pre-line text-center">
                                {t('llmPrice.rebuild.description')}
                            </TooltipContent>
                        </Tooltip>
                    </div>
                )}
            </div>
        </div>
    );
}
