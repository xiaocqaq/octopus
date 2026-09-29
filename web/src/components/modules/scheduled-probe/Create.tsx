import { useTranslations } from 'use-intl';
import { toast } from 'sonner';
import { useCreateScheduledProbe, type ScheduledProbeInput } from '@/api/scheduled-probe';
import { MorphingDialogDescription, useMorphingDialog } from '@/components/ui/morphing-dialog';
import { ProbeForm, type ProbeFormValues } from './ProbeForm';

// CreateDialogContent 渲染新建模型监控任务的表单。
//
// 一条任务下面可挂多个目标: 用户要的是"给一个自定义标题下塞几条不同渠道的模型",
// 而不是像旧版那样"勾一堆渠道、勾一堆模型, 笛卡尔积展开成一堆任务"。
// 提交体对位后端 ScheduledProbeRequest: name + targets + interval + 时段。
export function CreateDialogContent() {
    const { setIsOpen } = useMorphingDialog();
    const t = useTranslations('scheduledProbe');
    const createProbe = useCreateScheduledProbe();

    const handleSubmit = async (values: ProbeFormValues) => {
        const input: ScheduledProbeInput = {
            name: values.name,
            targets: values.targets,
            interval_minutes: values.interval_minutes,
            // 新建时忽略 enabled 开关：用户点了创建就是要它跑起来，
            // 不该在停用状态下躺进列表，还得再去翻卡片开一次。
            enabled: true,
            weekdays: values.weekdays,
            start_hour: values.start_hour,
            end_hour: values.end_hour,
        };
        try {
            await createProbe.mutateAsync(input);
            toast.success(t('toast.created', { count: 1 }));
            setIsOpen(false);
        } catch (error) {
            toast.error((error as Error).message);
        }
    };

    return (
        <MorphingDialogDescription>
            <ProbeForm
                submitText={t('submit')}
                submittingText={t('submitting')}
                isSubmitting={createProbe.isPending}
                onCancel={() => setIsOpen(false)}
                onSubmit={handleSubmit}
            />
        </MorphingDialogDescription>
    );
}
