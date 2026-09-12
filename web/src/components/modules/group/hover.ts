import { create } from 'zustand';

// 分组卡片当前展开的那一个, 全页面共享。
// 必须放在模块级而不是组件内: 卡片由 VirtualizedGrid 逐张渲染, 组件内状态无法约束彼此,
// 只有这样"移到另一个分组时上一个立刻收起"才成立。
interface GroupHoverState {
    activeGroupID: number | null; // 正在展开的分组主键, null 表示没有。
    setActiveGroup: (id: number | null) => void;
}

export const useGroupHoverStore = create<GroupHoverState>((set) => ({
    activeGroupID: null,
    setActiveGroup: (id) => set({ activeGroupID: id }),
}));
