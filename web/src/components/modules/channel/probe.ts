import { useState } from 'react';
import { toast } from 'sonner';
import { useTranslations } from 'use-intl';
import { useFetchModel } from '@/api/channel';
import { grantKey, probeTargetKeys, toChannelConfig, type ChannelFormState } from './state';

// useModelProbe 按凭据探测上游模型列表, 供模型页与凭据页共用。
// 两侧并发探测与协议位判定都在后端完成, 此处只负责转圈状态, 结果并入表单和结果提示。
export function useModelProbe() {
    const t = useTranslations('channel.form');
    const fetchModel = useFetchModel();
    const [pendingKey, setPendingKey] = useState<string | null>(null); // 正在探测的凭据名称, 只转动该行的图标。

    // probe 探测指定凭据可用的模型, 结果并入模型集合与授权表。
    // 上游未返回但本地已有的模型保留: 静默删除会打断正在使用该模型的路由。
    // 返回回写后的 state, 供调用处在多凭据探测时链式读取最新状态。
    const probeOne = async (
        state: ChannelFormState,
        setState: (next: ChannelFormState) => void,
        keyName: string,
    ): Promise<ChannelFormState> => {
        // 模型页的刷新按钮不按凭据禁用, 选中的凭据可能还没填 Key, 在此挡掉空请求。
        const channelKey = state.keys.find((k) => k.name === keyName);
        if (!channelKey || channelKey.key.trim() === '') return state;

        // 探测用的地址, 路径, 代理和过滤表达式必须和保存后生效的完全一致, 否则这里探到的模型
        // 与实际转发时能用的模型会不一样; 故与提交共用同一份配置, 探测用不上的字段由后端忽略。
        const fetched = await fetchModel.mutateAsync({
            channel: toChannelConfig(state),
            key: channelKey.key.trim(),
        });
        if (fetched.length === 0) {
            toast.warning(t('modelRefreshEmpty'));
            return state;
        }
        const models = [...state.models];
        const grants = new Map(state.grants);
        for (const { name, protocols } of fetched) {
            if (!models.includes(name)) models.push(name);
            const mapKey = grantKey(name, channelKey.name);
            grants.set(mapKey, (grants.get(mapKey) ?? 0) | protocols);
        }
        const next: ChannelFormState = { ...state, models, grants };
        setState(next);
        toast.success(t('modelRefreshSuccess', { count: fetched.length }));
        return next;
    };

    // probe 探测上游模型。keyName 为空表示探测全部凭据(「全部凭据」模式),
    // 此时逐凭据探测, 同一模型在多个凭据上的授权都回写, 否则新模型只落在第一个凭据上,
    // 后续「分组」只会带进一个 key。单选模式下 keyName 必填, 探测该凭据即可。
    const probe = async (
        state: ChannelFormState,
        setState: (next: ChannelFormState) => void,
        keyName: string,
    ) => {
        const targets = probeTargetKeys(state.keys, keyName);
        if (targets.length === 0) return;
        let current = state;
        setPendingKey(targets[0]);
        try {
            for (const name of targets) {
                setPendingKey(name);
                current = await probeOne(current, setState, name); // probeOne 返回最新 state 供下轮回写
            }
        } catch (error) {
            toast.error(t('modelRefreshFailed'), { description: String(error) });
        } finally {
            setPendingKey(null);
        }
    };

    return { probe, pendingKey };
}
