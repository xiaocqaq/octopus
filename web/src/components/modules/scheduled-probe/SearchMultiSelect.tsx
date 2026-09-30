import { useMemo, useState } from 'react';
import { useTranslations } from 'use-intl';
import { Check, ChevronsUpDown, Search } from 'lucide-react';
import { Popover, PopoverContent, PopoverTrigger } from '@/components/ui/popover';
import { Input } from '@/components/ui/input';
import { Checkbox } from '@/components/ui/checkbox';
import { Badge } from '@/components/ui/badge';
import { Button } from '@/components/ui/button';
import { cn } from '@/lib/utils';

// SearchMultiSelect 是一个可模糊搜索的多选下拉：触发器显示已选摘要，面板里逐项勾选。
//
// 共用一份实现而不是渠道、模型各写一个：两者只差选项来源与占位文案，形状完全一致，
// 分开写就要把"搜索、全选、清空、已选摘要、空结果提示"这套逻辑维护两遍。
// 也没引入 cmdk 之类的命令面板：本项目的下拉只需要"过滤 + 勾选"，
// Popover 与 Checkbox 都已在依赖里，为此多装一个包不划算。
//
// 备选项数量不大（渠道几条到几十条，单个渠道的模型同量级），故过滤用简单的 includes，
// 不做防抖也不做虚拟滚动：这点数据量下两者都只是额外的复杂度。
export type SearchOption = {
    value: string; // 提交给后端的原始值。
    label: string; // 展示用主标题。
    hint?: string; // 展示用副标题，如"12 个模型"。
    keywords?: string; // 参与搜索的额外文本；主标题与副标题始终参与匹配。
};

type SearchMultiSelectProps = {
    options: SearchOption[];
    selected: string[];
    onChange: (selected: string[]) => void;
    placeholder: string;
    searchPlaceholder: string;
    emptyText: string;
    disabled?: boolean;
    id?: string;
    // selectAllLabel / clearLabel 为空时对应按钮不显示。全选只作用于当前筛选出的可见项，
    // 于是"搜 DeepSeek 再点全选"就是"把 DeepSeek 的模型一次选上"，这正是批量录入最常用的路径。
    selectAllLabel?: string;
    clearLabel?: string;
    // confirmLabel 为空时不显示「完成」按钮。点击后收起面板——在移动端软键盘弹出时,
    // 点完成下拉框才会收起(键盘消失后 onOpenChange 不一定跟得上), 比点外部区域可靠。
    confirmLabel?: string;
};

// matchesQuery 判断一个备选项是否命中搜索词。
// 主标题、副标题与 keywords 一起匹配：用户可能记得的是"渠道名"、"模型数"或某个别名，
// 只匹配主标题会让"我明明看到过它"变成搜不到。
function matchesQuery(option: SearchOption, query: string) {
    if (query === '') return true;
    const haystack = `${option.label} ${option.hint ?? ''} ${option.keywords ?? ''}`.toLowerCase();
    return haystack.includes(query.toLowerCase());
}

export function SearchMultiSelect({
    options,
    selected,
    onChange,
    placeholder,
    searchPlaceholder,
    emptyText,
    disabled = false,
    id,
    selectAllLabel,
    clearLabel,
    confirmLabel,
}: SearchMultiSelectProps) {
    const t = useTranslations('scheduledProbe');
    const [open, setOpen] = useState(false);
    const [query, setQuery] = useState('');

    const filtered = useMemo(
        () => options.filter((option) => matchesQuery(option, query)),
        [options, query]
    );

    const selectedSet = useMemo(() => new Set(selected), [selected]);

    const toggle = (value: string) => {
        onChange(selectedSet.has(value) ? selected.filter((item) => item !== value) : [...selected, value]);
    };

    // 搜索词下的全选只作用于当前可见项：否则"全选"会把被搜索过滤掉的那些也一并选上，
    // 用户看到的和实际提交的就对不上了。
    const allFilteredSelected = filtered.length > 0 && filtered.every((option) => selectedSet.has(option.value));

    return (
        <Popover open={open} onOpenChange={setOpen}>
            <PopoverTrigger
                id={id}
                type="button"
                disabled={disabled}
                className={cn(
                    'flex min-h-9 w-full items-center justify-between gap-2 rounded-xl border border-input bg-transparent px-3 py-2 text-sm shadow-xs transition-[color,box-shadow] outline-none',
                    'focus-visible:border-ring focus-visible:ring-[3px] focus-visible:ring-ring/50',
                    'disabled:cursor-not-allowed disabled:opacity-50 dark:bg-input/30'
                )}
            >
                <span className="flex min-w-0 flex-1 flex-wrap items-center gap-1 text-left">
                    {selected.length === 0 ? (
                        <span className="truncate text-muted-foreground">{placeholder}</span>
                    ) : (
                        selected.map((value) => (
                            <Badge key={value} variant="secondary" className="max-w-full truncate px-1.5 py-0 text-xs font-normal">
                                {options.find((option) => option.value === value)?.label ?? value}
                            </Badge>
                        ))
                    )}
                </span>
                <ChevronsUpDown className="size-4 shrink-0 opacity-50" />
            </PopoverTrigger>

            <PopoverContent align="start" className="w-[var(--radix-popover-trigger-width)] min-w-64 p-0">
                <div className="flex items-center gap-2 border-b border-border px-3 py-2">
                    <Search className="size-4 shrink-0 opacity-50" />
                    <Input
                        autoFocus
                        value={query}
                        onChange={(event) => setQuery(event.target.value)}
                        placeholder={searchPlaceholder}
                        className="h-7 border-0 bg-transparent p-0 shadow-none focus-visible:ring-0 dark:bg-transparent"
                    />
                </div>

                {(selectAllLabel || clearLabel) && (
                    <div className="flex items-center justify-between gap-2 border-b border-border px-3 py-1.5 text-xs">
                        {selectAllLabel && (
                            <button
                                type="button"
                                onClick={() => {
                                    // 合并而非替换：搜索态下的全选只补上可见项，不清掉此前已选的其余项。
                                    const merged = new Set(selected);
                                    filtered.forEach((option) => merged.add(option.value));
                                    onChange([...merged]);
                                }}
                                disabled={filtered.length === 0 || allFilteredSelected}
                                className="text-muted-foreground hover:text-foreground disabled:opacity-40"
                            >
                                {selectAllLabel}
                            </button>
                        )}
                        {clearLabel && (
                            <button
                                type="button"
                                onClick={() => onChange([])}
                                disabled={selected.length === 0}
                                className="text-muted-foreground hover:text-foreground disabled:opacity-40"
                            >
                                {clearLabel}
                            </button>
                        )}
                    </div>
                )}

                <div className="max-h-64 overflow-y-auto overscroll-contain p-1">
                    {filtered.length === 0 ? (
                        <p className="px-2 py-6 text-center text-xs text-muted-foreground">{emptyText}</p>
                    ) : (
                        filtered.map((option) => {
                            const checked = selectedSet.has(option.value);
                            return (
                                <label
                                    key={option.value}
                                    className="flex cursor-pointer items-center gap-2 rounded-lg px-2 py-1.5 text-sm hover:bg-muted/50"
                                >
                                    <Checkbox checked={checked} onCheckedChange={() => toggle(option.value)} />
                                    <span className="min-w-0 flex-1 truncate">{option.label}</span>
                                    {option.hint && (
                                        <span className="shrink-0 text-xs text-muted-foreground">{option.hint}</span>
                                    )}
                                    {checked && <Check className="size-3.5 shrink-0 text-primary" />}
                                </label>
                            );
                        })
                    )}
                </div>

                <div className="flex items-center justify-between gap-2 border-t border-border px-3 py-2 text-xs">
                    <span className="text-muted-foreground">{t('form.selectedCount', { count: selected.length })}</span>
                    {confirmLabel && (
                        <Button type="button" variant="default" size="sm" onClick={() => setOpen(false)} className="h-11 px-3 text-xs font-medium sm:h-7">
                            {confirmLabel}
                        </Button>
                    )}
                </div>
            </PopoverContent>
        </Popover>
    );
}
