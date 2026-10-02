import { useMemo, useState } from 'react';
import { useTranslations } from 'use-intl';
import { useChannelGrantList } from '@/api/channel';
import {
    SCHEDULED_PROBE_DEFAULT_END_HOUR,
    SCHEDULED_PROBE_DEFAULT_INTERVAL,
    SCHEDULED_PROBE_DEFAULT_START_HOUR,
    SCHEDULED_PROBE_DEFAULT_WEEKDAYS,
    SCHEDULED_PROBE_HOURS,
    type ScheduledProbeTarget,
} from '@/api/scheduled-probe';
import { Button } from '@/components/ui/button';
import { Input } from '@/components/ui/input';
import { Field, FieldDescription, FieldGroup, FieldLabel } from '@/components/ui/field';
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from '@/components/ui/select';
import { Switch } from '@/components/ui/switch';
import { formatHour } from './format';
import { WeekdayPicker } from './WeekdayPicker';
import { SearchMultiSelect, type SearchOption } from './SearchMultiSelect';
import { buildTargetOptions, targetValuesFor, targetsOf } from './targets';

// ProbeFormValues 是这张表单的产出，与后端的提交体同形状（主键除外）。
export type ProbeFormValues = {
    name: string;
    // 后端的粒度只到 (渠道, 模型)；界面上的凭据那一段在提交前进了 excluded_keys。
    targets: ScheduledProbeTarget[];
    interval_minutes: number;
    enabled: boolean;
    // iq_disabled 关掉这条任务的糖果测试：开着时每一拍发的都是糖果题（题目的响应本身就带"通不通、多快"），
    // 关掉才回到只发 "hi" 的纯测活。字段名记的是"关掉"而不是"打开"，与后端一致：缺省即测糖果。
    iq_disabled: boolean;
    weekdays: number;
    start_hour: number;
    end_hour: number;
};

type ProbeFormProps = {
    initial?: ProbeFormValues;
    submitText: string;
    submittingText: string;
    isSubmitting: boolean;
    onCancel: () => void;
    onSubmit: (values: ProbeFormValues) => void;
};

// ProbeForm 是创建与编辑共用的表单：一个自定义名字 + 渠道/凭据/模型合并多选 + 间隔 + 时段。
//
// 合并为一份多选下拉，而不是分成渠道、凭据、模型三个下拉：一个模型可能被多个渠道提供，
// 拆成三份的话"渠道 × 凭据 × 模型"是个笛卡尔积，会产出用户没看见的组合；
// 合成一份「渠道名 凭据名 模型名」的选项，勾的是确确实实会监控的那一条。
//
// 选项来源是授权列表(channelGrantListQueryOptions)而不是渠道统计：统计只给到"某渠道有哪些模型"，
// 拿不到凭据名，而这正是这个下拉要展示的第三段。
export function ProbeForm({ initial, submitText, submittingText, isSubmitting, onCancel, onSubmit }: ProbeFormProps) {
    const t = useTranslations('scheduledProbe');
    const { data: candidates } = useChannelGrantList();

    // 选项列表：候选授权摊成 (渠道, 凭据, 模型) 三段，按标签排序。
    const options = useMemo(() => buildTargetOptions(candidates ?? []), [candidates]);
    const targetOptions: SearchOption[] = useMemo(
        () => options.map((option) => ({ value: option.value, label: option.label })),
        [options],
    );

    // 初始回填（编辑）：目标上只记了 (渠道, 模型)，凭据那段要靠候选补回真实存在的钥匙 ——
    // 而且要把该目标在测的**每一把**都补上（见 targetValuesFor）：只补一条的话，提交时
    // 其余凭据会被算成"用户没勾"而写进 excluded_keys，等于编辑一次就把监控范围改小了。
    //
    // 候选是异步到的：首次渲染时可能还是空的，只回填出"整条目标"的空凭据值（见 targetValueOf）。
    // 因此选中项在用户动手之前一直跟着候选推导（picked 为 null 就用 initialValues）：
    // 若在这里把它固化进 useState，候选到了也换不成真实凭据，而那个空凭据值提交时会被
    // 折算成"一把钥匙都没勾"、于是所有凭据都进 excluded_keys —— 整条目标会被清空，
    // 比少勾一条严重得多。
    const initialValues = useMemo(() => {
        const values: string[] = [];
        for (const target of initial?.targets ?? []) {
            for (const value of targetValuesFor(target, candidates ?? [])) {
                if (value && !values.includes(value)) values.push(value);
            }
        }
        return values;
    }, [initial, candidates]);

    // picked 为 null 表示用户还没动过多选，此时选中项由候选推导。
    const [picked, setPicked] = useState<string[] | null>(null);
    const selected = picked ?? initialValues;
    const [name, setName] = useState(initial?.name ?? '');
    const [interval, setIntervalValue] = useState(String(initial?.interval_minutes ?? SCHEDULED_PROBE_DEFAULT_INTERVAL));
    // 新建默认开着糖果测试: 糖果题的响应本身就回答了"通不通、多快", 它同时是这条链路信息量最大的探针;
    // 关掉只在"不想为这条通道花糖果题的钱"时才需要。
    const [iqDisabled, setIqDisabled] = useState(initial?.iq_disabled ?? false);
    const [weekdays, setWeekdays] = useState(initial?.weekdays ?? SCHEDULED_PROBE_DEFAULT_WEEKDAYS);
    const [startHour, setStartHour] = useState(initial?.start_hour ?? SCHEDULED_PROBE_DEFAULT_START_HOUR);
    const [endHour, setEndHour] = useState(initial?.end_hour ?? SCHEDULED_PROBE_DEFAULT_END_HOUR);

    const targets = targetsOf(selected, candidates ?? []);

    const intervalMinutes = Number(interval);
    const intervalValid = Number.isInteger(intervalMinutes) && intervalMinutes >= 1 && intervalMinutes <= 1440;
    const canSubmit = name.trim() !== '' && targets.length > 0 && intervalValid;

    const handleSubmit = (event: React.FormEvent<HTMLFormElement>) => {
        event.preventDefault();
        if (!canSubmit) return;
        onSubmit({
            name: name.trim(),
            targets,
            interval_minutes: intervalMinutes,
            // 编辑时保留原有的启停状态：开关在卡片上，表单不该顺手把它打开。
            enabled: initial?.enabled ?? true,
            iq_disabled: iqDisabled,
            weekdays,
            start_hour: startHour,
            end_hour: endHour,
        });
    };

    return (
        <form onSubmit={handleSubmit} className="flex min-h-0 flex-1 flex-col">
            {/* 表单可能很长，故让字段区自己滚、按钮固定在底部：
                否则目标一多，提交按钮就被挤出可视区，用户得先滚到底才能点。 */}
            <div className="min-h-0 flex-1 overflow-y-auto overscroll-contain pr-1">
                <FieldGroup className="gap-4">
                    <Field>
                        <FieldLabel htmlFor="scheduled-probe-name">{t('form.name')}</FieldLabel>
                        <Input
                            id="scheduled-probe-name"
                            value={name}
                            onChange={(event) => setName(event.target.value)}
                            placeholder={t('form.namePlaceholder')}
                            className="rounded-xl"
                        />
                        <FieldDescription>{t('form.nameHint')}</FieldDescription>
                    </Field>

                    {/* 合成一个多选：渠道、凭据与模型不再是三个独立下拉, 而是「渠道名 凭据名 模型名」的组合选项。
                        选它就是在说"这条通道的这个模型, 用这把钥匙测"。 */}
                    <Field>
                        <FieldLabel htmlFor="scheduled-probe-targets">{t('form.targets')}</FieldLabel>
                        <SearchMultiSelect
                            id="scheduled-probe-targets"
                            options={targetOptions}
                            selected={selected}
                            onChange={setPicked}
                            placeholder={t('form.targetPlaceholder')}
                            searchPlaceholder={t('form.searchTarget')}
                            emptyText={t('form.targetEmpty')}
                            selectAllLabel={t('form.selectAll')}
                            clearLabel={t('form.clear')}
                            confirmLabel={t('form.confirm')}
                        />
                        <FieldDescription>{t('form.targetHint')}</FieldDescription>
                    </Field>

                    <Field>
                        <FieldLabel htmlFor="scheduled-probe-interval">{t('form.interval')}</FieldLabel>
                        <Input
                            id="scheduled-probe-interval"
                            type="number"
                            min={1}
                            max={1440}
                            value={interval}
                            onChange={(event) => setIntervalValue(event.target.value)}
                            className="rounded-xl"
                        />
                        <FieldDescription>{t('form.intervalHint')}</FieldDescription>
                    </Field>

                    {/* 糖果开关紧跟在间隔之后: 它决定的是"这一拍发什么", 与上面那个"多久发一次"是同一组问题;
                        两者一起看才知道这条任务要花多少上游流量。 */}
                    <div className="flex items-start justify-between gap-4">
                        <div className="space-y-1">
                            <FieldLabel htmlFor="scheduled-probe-iq">{t('form.iq')}</FieldLabel>
                            <FieldDescription>{t('form.iqHint')}</FieldDescription>
                        </div>
                        <Switch
                            id="scheduled-probe-iq"
                            checked={!iqDisabled}
                            onCheckedChange={(checked) => setIqDisabled(!checked)}
                            className="mt-0.5"
                        />
                    </div>

                    <Field>
                        <FieldLabel>{t('form.window')}</FieldLabel>
                        <WeekdayPicker weekdays={weekdays} onChange={setWeekdays} />
                        <FieldDescription>{t('form.windowHint')}</FieldDescription>
                    </Field>

                    {weekdays !== 0 && (
                        <div className="grid grid-cols-2 gap-4">
                            <Field>
                                <FieldLabel htmlFor="scheduled-probe-start">{t('form.startHour')}</FieldLabel>
                                <Select value={String(startHour)} onValueChange={(value) => setStartHour(Number(value))}>
                                    <SelectTrigger id="scheduled-probe-start" className="w-full rounded-xl">
                                        <SelectValue />
                                    </SelectTrigger>
                                    <SelectContent>
                                        {SCHEDULED_PROBE_HOURS.map((hour) => (
                                            <SelectItem key={hour} value={String(hour)}>{formatHour(hour)}</SelectItem>
                                        ))}
                                    </SelectContent>
                                </Select>
                            </Field>
                            <Field>
                                <FieldLabel htmlFor="scheduled-probe-end">{t('form.endHour')}</FieldLabel>
                                <Select value={String(endHour)} onValueChange={(value) => setEndHour(Number(value))}>
                                    <SelectTrigger id="scheduled-probe-end" className="w-full rounded-xl">
                                        <SelectValue />
                                    </SelectTrigger>
                                    <SelectContent>
                                        {SCHEDULED_PROBE_HOURS.map((hour) => (
                                            <SelectItem key={hour} value={String(hour)}>{formatHour(hour)}</SelectItem>
                                        ))}
                                    </SelectContent>
                                </Select>
                            </Field>
                            <p className="col-span-2 text-xs text-muted-foreground">
                                {startHour > endHour ? t('form.windowCrossMidnight') : t('form.windowSameDay')}
                            </p>
                        </div>
                    )}
                </FieldGroup>
            </div>

            <div className="mt-4 flex shrink-0 gap-2">
                <Button type="button" variant="secondary" onClick={onCancel} className="h-11 flex-1 rounded-xl">
                    {t('cancel')}
                </Button>
                <Button type="submit" disabled={isSubmitting || !canSubmit} className="h-11 flex-1 rounded-xl">
                    {isSubmitting ? submittingText : submitText}
                </Button>
            </div>
        </form>
    );
}
