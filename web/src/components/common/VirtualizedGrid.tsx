import {
    type ReactNode,
    useCallback,
    useEffect,
    useMemo,
    useRef,
    useState,
} from 'react';
import { useVirtualizer } from '@tanstack/react-virtual';

const BREAKPOINTS = {
    sm: 640,
    md: 768,
    lg: 960,
    xl: 1280,
    '2xl': 1536,
} as const;

type Breakpoint = keyof typeof BREAKPOINTS;
type ResponsiveColumns = Partial<Record<Breakpoint | 'default', number>>;

interface VirtualizedGridProps<T> {
    items: T[];
    layout?: 'grid' | 'list';
    columns: ResponsiveColumns;
    estimateItemHeight: number;
    gap?: number;
    overscan?: number;
    getItemKey: (item: T, index: number) => string | number;
    renderItem: (item: T, index: number) => ReactNode;
    footer?: ReactNode;
    onReachEnd?: () => void;
    reachEndEnabled?: boolean;
    reachEndOffset?: number;
}

function getColumnsForWidth(
    width: number,
    columns: ResponsiveColumns,
): number {
    if (width >= BREAKPOINTS['2xl'] && columns['2xl'] !== undefined) return columns['2xl'];
    if (width >= BREAKPOINTS.xl && columns.xl !== undefined) return columns.xl;
    if (width >= BREAKPOINTS.lg && columns.lg !== undefined) return columns.lg;
    if (width >= BREAKPOINTS.md && columns.md !== undefined) return columns.md;
    if (width >= BREAKPOINTS.sm && columns.sm !== undefined) return columns.sm;
    return columns.default ?? 1;
}

export function VirtualizedGrid<T>({
    items,
    layout = 'grid',
    columns,
    estimateItemHeight,
    gap = 16,
    overscan = 4,
    getItemKey,
    renderItem,
    footer = null,
    onReachEnd,
    reachEndEnabled = false,
    reachEndOffset = 1,
}: VirtualizedGridProps<T>) {
    'use no memo';

    const [containerWidth, setContainerWidth] = useState(() =>
        typeof window === 'undefined' ? 1024 : window.innerWidth
    );
    const containerRef = useRef<HTMLDivElement | null>(null);
    const reachEndTriggeredRef = useRef(false);

    useEffect(() => {
        const el = containerRef.current;
        if (!el) return;

        const updateWidth = () => {
            const nextWidth = el.clientWidth;
            setContainerWidth((prev) => (prev === nextWidth ? prev : nextWidth));
        };

        updateWidth();

        if (typeof ResizeObserver === 'undefined') return;
        const observer = new ResizeObserver(updateWidth);
        observer.observe(el);

        return () => {
            observer.disconnect();
        };
    }, []);

    const columnCount = useMemo(() => {
        if (layout === 'list') return 1;
        return Math.max(1, getColumnsForWidth(containerWidth, columns));
    }, [layout, containerWidth, columns]);

    const itemRowCount = useMemo(
        () => (items.length === 0 ? 0 : Math.ceil(items.length / columnCount)),
        [items.length, columnCount]
    );
    const hasFooterRow = footer !== null;
    const rowCount = itemRowCount + (hasFooterRow ? 1 : 0);
    const getVirtualRowKey = useCallback((rowIndex: number) => {
        if (hasFooterRow && rowIndex === itemRowCount) {
            return '__virtual-footer__';
        }

        const rowStartIndex = rowIndex * columnCount;
        const firstItem = items[rowStartIndex];
        if (!firstItem) return `row-empty-${rowIndex}`;

        // Keep row keys stable across prepend/append updates (especially log stream updates),
        // otherwise virtualizer measurements are constantly invalidated and spacing falls back to estimates.
        return `row-${String(getItemKey(firstItem, rowStartIndex))}`;
    }, [hasFooterRow, itemRowCount, columnCount, items, getItemKey]);

    // eslint-disable-next-line react-hooks/incompatible-library
    const rowVirtualizer = useVirtualizer({
        count: rowCount,
        getScrollElement: () => containerRef.current,
        getItemKey: getVirtualRowKey,
        estimateSize: () => estimateItemHeight + gap,
        // Use layout height (not transformed visual height) to avoid scale-animation
        // shrinking measurements during page enter transitions.
        measureElement: (element) =>
            element instanceof HTMLElement
                ? element.offsetHeight
                : element.getBoundingClientRect().height,
        overscan,
    });

    const virtualRows = rowVirtualizer.getVirtualItems();

    useEffect(() => {
        if (!onReachEnd || !reachEndEnabled || itemRowCount === 0) return;

        const lastVirtualIndex = virtualRows.length > 0 ? virtualRows[virtualRows.length - 1]!.index : -1;
        const triggerIndex = Math.max(0, itemRowCount - 1 - reachEndOffset);
        if (lastVirtualIndex < triggerIndex) {
            reachEndTriggeredRef.current = false;
            return;
        }
        if (reachEndTriggeredRef.current) return;

        reachEndTriggeredRef.current = true;
        onReachEnd();
    }, [onReachEnd, reachEndEnabled, itemRowCount, reachEndOffset, virtualRows]);

    return (
        <div className="relative h-full min-h-0 w-full">
            <div
                ref={containerRef}
                className="relative h-full w-full overflow-y-auto overscroll-contain rounded-t-3xl"
            >
                {rowCount === 0 ? null : (
                    <div className="relative w-full" style={{ height: `${rowVirtualizer.getTotalSize()}px` }}>
                        {virtualRows.map((virtualRow) => {
                            if (hasFooterRow && virtualRow.index === itemRowCount) {
                                return (
                                    <div
                                    key={virtualRow.key}
                                    data-index={virtualRow.index}
                                    ref={rowVirtualizer.measureElement}
                                    className="absolute left-0 top-0 w-full"
                                    // 用 top 而不是 transform: translateY 定位。
                                    //
                                    // transform 会让这个行容器成为 position: fixed 后代的包含块。
                                    // @hello-pangea/dnd 拖动时把行设成 position: fixed, 并按视口坐标算出 left/top;
                                    // 一旦包含块变成了行容器, 那份视口坐标会被叠加在行自身的偏移上 ——
                                    // 实测拖动中的行 left 从 266 变成 543, 整整偏出一个卡片宽度, 看起来就是"拖起来就异位"。
                                    // 换成 top 定位后这个行容器不再建立包含块, 拖拽项才按视口坐标落位。
                                    //
                                    // 代价是滚动时触发的是布局而非合成。此处每屏不过十余张卡, 用正确性换这点开销是划算的;
                                    // 若将来列表规模大到需要 transform 合成, 应改为把拖拽层渲染到 body 上, 而不是把 transform 加回来。
                                    style={{
                                        top: `${virtualRow.start}px`,
                                    }}
                                >
                                    {footer}
                                </div>
                                );
                            }

                            const rowStartIndex = virtualRow.index * columnCount;
                            const rowEndIndex = Math.min(rowStartIndex + columnCount, items.length);
                            const rowItems = items.slice(rowStartIndex, rowEndIndex);
                            const rowPaddingBottom = virtualRow.index === itemRowCount - 1 && !hasFooterRow ? 0 : gap;

                            return (
                                <div
                                    key={virtualRow.key}
                                    data-index={virtualRow.index}
                                    ref={rowVirtualizer.measureElement}
                                    className="absolute left-0 top-0 w-full"
                                    // 与 footer 行同理: 这里必须用 top 定位, 不能用 transform: translateY,
                                    // 否则本行成为 position: fixed 后代的包含块, 拖拽中的行会被二次偏移。
                                    style={{
                                        top: `${virtualRow.start}px`,
                                    }}
                                >
                                    <div
                                        className="grid"
                                        style={{
                                            gridTemplateColumns: `repeat(${columnCount}, minmax(0, 1fr))`,
                                            gap: `${gap}px`,
                                            paddingBottom: `${rowPaddingBottom}px`,
                                        }}
                                    >
                                        {rowItems.map((item, columnIndex) => {
                                            const itemIndex = rowStartIndex + columnIndex;
                                            return (
                                                <div key={String(getItemKey(item, itemIndex))} className="min-w-0">
                                                    {renderItem(item, itemIndex)}
                                                </div>
                                            );
                                        })}
                                    </div>
                                </div>
                            );
                        })}
                    </div>
                )}
            </div>
        </div>
    );
}
