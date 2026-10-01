import { useEffect, useState } from 'react';

// PROBE_CLOCK_INTERVAL_MS 是"多久以前"这几个字的重算节拍。
//
// 取 10 秒而不是 1 秒: 这个标签最细只说到「分钟」, 秒级重算只会让整页白白重渲染十次。
// 也不能干脆不重算: 「刚刚」翻成「1 分钟前」只由时间本身决定, 没有任何数据变化可以依赖。
const PROBE_CLOCK_INTERVAL_MS = 10_000;

// useProbeClock 提供一个按固定节拍前进的当前时刻, 供卡片算出结论已经过去多久。
//
// 必须是状态而不是在格式化函数里直接读 Date.now(): React 看不见 Date.now()。
// 卡片渲染一次之后, React Compiler 会把那次 describeProbedAt 调用的结果按它的入参缓存下来,
// 结论没变就不再重算, 于是「刚刚」会一直挂着 —— 页面看着像没有自动刷新, 手动刷新才跳到真实时长。
// 把时刻放进状态, 它才成为缓存的入参之一, 时间推进才会真的走到界面上。
//
// 由页面调用一次再往下传, 而不是每张卡片各起一个定时器: 同一页上所有卡片读的是同一个时刻,
// 各自计时既多出几十个无谓的唤醒, 也会让相邻两行显示出互相矛盾的"几分钟前"。
export function useProbeClock() {
    const [now, setNow] = useState(() => Date.now());

    useEffect(() => {
        const timer = window.setInterval(() => setNow(Date.now()), PROBE_CLOCK_INTERVAL_MS);
        return () => window.clearInterval(timer);
    }, []);

    return now;
}