import { useTranslations } from 'use-intl';
import { toast } from 'sonner';
import { useUpdateScheduledProbe, type ScheduledProbe, type ScheduledProbeInput } from '@/api/scheduled-probe';
import { MorphingDialogDescription, useMorphingDialog } from '@/components/ui/morphing-dialog';
import { ProbeForm, type ProbeFormValues } from './ProbeForm';

export interface EditDialogContentProps {
    initial: ScheduledProbe;
}

// EditDialogContent 把一条已有任务还原到 ProbeForm，提交时整体替换。
//
// 更新是整体替换（见后端的 updateScheduledProbe）: 表单里缺的字段就会被清空，
// 故 initial 必须把任务的全部字段带上 —— 尤其是 enabled，不要在这里把它硬重置成 true。
export function EditDialogContent({ initial }: EditDialogContentProps) {
    const { setIsOpen } = useMorphingDialog();
    const t = useTranslations('scheduledProbe');
    const updateProbe = useUpdateScheduledProbe();

    const handleSubmit = async (values: ProbeFormValues) => {
        const input: ScheduledProbeInput = {
            name: values.name,
            targets: values.targets,
            interval_minutes: values.interval_minutes,
            // 编辑保留提交过来的 enabled：开关在卡片上，表单不参与启停，
            // 否则勾一下框又被表单悄悄关掉，就看不懂为什么了。
            enabled: values.enabled,
            weekdays: values.weekdays,
            start_hour: values.start_hour,
            end_hour: values.end_hour,
        };
        try {
            await updateProbe.mutateAsync({ ...input, id: initial.id });
            toast.success(t('toast.updated'));
            setIsOpen(false);
        } catch (error) {
            toast.error((error as Error).message);
        }
    };

    return (
        // 与新建弹窗同样的两层约束: 自己可收缩(min-h-0 flex-1), 且自己是 flex 容器,
        // 否则表单撑高后会把提交按钮顶出弹窗、被外层 overflow-hidden 裁掉。
        <MorphingDialogDescription className="flex min-h-0 flex-1 flex-col">
            <ProbeForm
                initial={{
                    name: initial.name,
                    targets: initial.targets,
                    interval_minutes: initial.interval_minutes,
                    enabled: initial.enabled,
                    weekdays: initial.weekdays,
                    start_hour: initial.start_hour,
                    end_hour: initial.end_hour,
                }}
                submitText={t('submit')}
                submittingText={t('submitting')}
                isSubmitting={updateProbe.isPending}
                onCancel={() => setIsOpen(false)}
                onSubmit={handleSubmit}
            />
        </MorphingDialogDescription>
    );
}
