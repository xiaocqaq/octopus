import type { CSSProperties, ReactNode } from 'react';
import { useRef, useState } from 'react';
import { flushSync } from 'react-dom';
import { AnimatePresence, motion } from 'motion/react';
import { useTranslations } from 'use-intl';
import Logo from '@/components/modules/logo';
import { NAV_ITEMS, useAppStore } from '@/stores/app';
import { preloadPage } from '@/lib/page-preload';
import { cn } from '@/lib/utils';

// AppShell 作为普通用户界面的稳定布局层，统一渲染导航、顶栏和页面内容。
export function AppShell({ children, actions }: { children: ReactNode; actions?: ReactNode }) {
    const currentPage = useAppStore((state) => state.currentPage);
    const direction = useAppStore((state) => state.direction);
    const setCurrentPage = useAppStore((state) => state.setCurrentPage);
    const t = useTranslations('navbar');
    // 价格页没有自己的导航按钮(主导航固定六个), 它从设置进入, 高亮就归到设置项。
    // 不这样归一, findIndex 会返回 -1, 指示块会被推到导航栏外。
    const activeIndex = NAV_ITEMS.findIndex((route) => route.id === (currentPage === 'model' ? 'setting' : currentPage)); // activeIndex 表示选中项在 Dock 中的位置。
    const [hoveredIndex, setHoveredIndex] = useState<number | null>(null); // hoveredIndex 表示当前悬浮项的位置。
    const [isNavHovered, setIsNavHovered] = useState(false); // isNavHovered 表示悬浮背景是否显示。
    const hoverIndicatorRef = useRef<HTMLSpanElement>(null); // hoverIndicatorRef 用于在淡入前确认悬浮背景的新位置。

    return (
        <div className="mx-auto flex h-dvh max-w-6xl animate-in flex-col overflow-hidden px-3 fade-in duration-300 md:grid md:grid-cols-[auto_1fr] md:grid-rows-[auto_minmax(0,1fr)] md:gap-x-6 md:px-6">
            <div className="relative z-50 md:row-span-2 md:min-h-screen">
                <nav
                    aria-label="Main Navigation"
                    className={cn(
                        'fixed bottom-6 left-1/2 isolate -translate-x-1/2 flex animate-in items-center gap-1 p-3 fade-in zoom-in-95 duration-300',
                        'md:sticky md:top-30 md:left-auto md:bottom-auto md:translate-x-0 md:flex-col md:gap-3',
                        'bg-sidebar text-sidebar-foreground border border-sidebar-border rounded-3xl',
                    )}
                    onMouseLeave={() => setIsNavHovered(false)}
                >
                    <span
                        aria-hidden="true"
                        className="pointer-events-none absolute left-3 top-3 z-10 size-10 rounded-2xl bg-sidebar-primary transition-transform duration-300 ease-out [transform:translateX(var(--nav-offset-x))] md:size-12 md:[transform:translateY(var(--nav-offset-y))]"
                        style={{
                            '--nav-offset-x': `${activeIndex * 2.75}rem`,
                            '--nav-offset-y': `${activeIndex * 3.75}rem`,
                        } as CSSProperties}
                    />
                    <span
                        ref={hoverIndicatorRef}
                        aria-hidden="true"
                        className={cn(
                            'pointer-events-none absolute left-3 top-3 z-0 size-10 [transform:translateX(var(--nav-offset-x))] md:size-12 md:[transform:translateY(var(--nav-offset-y))]',
                            isNavHovered ? 'transition-transform duration-300 ease-out' : 'transition-none',
                        )}
                        style={{
                            '--nav-offset-x': `${(hoveredIndex ?? activeIndex) * 2.75}rem`,
                            '--nav-offset-y': `${(hoveredIndex ?? activeIndex) * 3.75}rem`,
                        } as CSSProperties}
                    >
                        <span
                            className="absolute inset-0 rounded-2xl bg-sidebar-accent transition-opacity duration-350 ease-linear"
                            style={{ opacity: isNavHovered ? 1 : 0 }}
                        />
                    </span>
                    {NAV_ITEMS.map((route, index) => {
                        const isActive = currentPage === route.id;

                        return (
                            <button
                                key={route.id}
                                type="button"
                                aria-label={route.label}
                                aria-current={isActive ? 'page' : undefined}
                                onMouseEnter={() => {
                                    // 首次进入先在不可见状态下定位，避免背景从上一次位置移动过来。
                                    if (isNavHovered) {
                                        setHoveredIndex(index);
                                    } else {
                                        flushSync(() => setHoveredIndex(index));
                                        hoverIndicatorRef.current?.getBoundingClientRect();
                                        setIsNavHovered(true);
                                    }
                                    preloadPage(route.id);
                                }}
                                onFocus={() => preloadPage(route.id)}
                                onTouchStart={() => preloadPage(route.id)}
                                onClick={() => {
                                    preloadPage(route.id);
                                    setCurrentPage(route.id);
                                }}
                                className={cn(
                                    'relative z-20 flex size-10 items-center justify-center rounded-2xl p-2 transition-[color,scale] duration-150 ease-linear hover:z-30 hover:scale-110 active:scale-95 md:size-12 md:p-3',
                                    isActive ? 'text-sidebar-primary-foreground' : 'text-sidebar-foreground/60',
                                )}
                            >
                                <span className="relative z-10">
                                    <route.icon strokeWidth={2} />
                                </span>
                            </button>
                        );
                    })}
                </nav>
            </div>

            <header className="my-3 md:my-6 flex flex-none items-center gap-x-2 px-2">
                <Logo size={48} />
                <div className="min-w-0 flex-1 overflow-hidden">
                    <AnimatePresence mode="wait" custom={direction}>
                        <motion.div
                            key={currentPage}
                            custom={direction}
                            variants={{
                                initial: (value: number) => ({ y: 32 * value, opacity: 0 }),
                                animate: { y: 0, opacity: 1 },
                                exit: (value: number) => ({ y: -32 * value, opacity: 0 }),
                            }}
                            initial="initial"
                            animate="animate"
                            exit="exit"
                            transition={{ duration: 0.3 }}
                            className="flex items-center"
                        >
                            <span className="mt-1 truncate text-3xl font-bold">
                                {t(currentPage)}
                            </span>
                        </motion.div>
                    </AnimatePresence>
                </div>
                {actions && <div className="ml-auto">{actions}</div>}
            </header>

            <main className="relative flex min-h-0 w-full min-w-0 flex-1 flex-col overflow-hidden">
                {children}
            </main>
        </div>
    );
}
