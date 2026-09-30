import { create } from 'zustand';
import { persist } from 'zustand/middleware';
import type { LucideIcon } from 'lucide-react';
import { Home, Radio, FolderTree, Settings, Logs, Activity } from 'lucide-react';

// Page 表示应用支持的固定页面集合。
export type Page = 'home' | 'channel' | 'group' | 'model' | 'probe' | 'log' | 'setting';

// NavItem 描述导航按钮使用的页面标识、文案和图标。
type NavItem = { id: Page; label: string; icon: LucideIcon };

// NAV_ITEMS 是桌面和移动导航共用的固定导航定义。
export const NAV_ITEMS: NavItem[] = [
    { id: 'home', label: 'Home', icon: Home },
    { id: 'channel', label: 'Channel', icon: Radio },
    { id: 'group', label: 'Group', icon: FolderTree },
    { id: 'probe', label: 'ModelMonitor', icon: Activity },
    { id: 'log', label: 'Log', icon: Logs },
    { id: 'setting', label: 'Setting', icon: Settings },
];

const NAV_ORDER: Page[] = NAV_ITEMS.map((item) => item.id); // NAV_ORDER 用于计算页面名称滚动方向。

interface AppState {
    currentPage: Page; // 当前选中的固定页面。
    direction: number; // 页面名称切换时的滚动方向。
    setCurrentPage: (page: Page) => void; // 切换当前页面。
}

// useAppStore 保存应用当前页面及页面名称切换方向。
export const useAppStore = create<AppState>()(
    persist(
        (set, get) => ({
            currentPage: 'home',
            direction: 0,
            setCurrentPage: (page) => {
                const currentIndex = NAV_ORDER.indexOf(get().currentPage);
                const nextIndex = NAV_ORDER.indexOf(page);
                set({ currentPage: page, direction: nextIndex > currentIndex ? 1 : -1 });
            },
        }),
        {
            name: 'nav-storage',
            merge: (persistedState, currentState) => {
                const persisted = persistedState as Partial<AppState>;
                return {
                    ...currentState,
                    ...persisted,
                    currentPage: persisted.currentPage === 'model' ? 'setting' : (persisted.currentPage ?? currentState.currentPage),
                };
            },
        }
    )
);
