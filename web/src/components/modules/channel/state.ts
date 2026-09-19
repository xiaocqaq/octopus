import type { ChannelDetail } from '@/api/channel';

// ChannelFormState 是渠道表单的全部可编辑内容。
// 全按名称组织而不存主键: 后端凭据与模型都按名称匹配增删改, 而新建渠道和新加模型时主键尚不存在。
export type ChannelFormState = {
    name: string;
    dialect: ChannelDetail['dialect'];
    base_url: string;
    enabled: boolean;
    proxy: boolean;
    openai_chat_completion_path: string;
    openai_response_path: string;
    anthropic_message_path: string;
    openai_image_generation_path: string;
    openai_image_edit_path: string;
    keys: { name: string; key: string; enabled: boolean }[];
    models: string[];
    grants: Map<string, number>; // 键为 grantKey(模型名, 凭据名), 值为 Protocol 位掩码。
    custom_header: ChannelDetail['custom_header'];
    channel_proxy: string;
    param_override: string;
    match_regex: string;
};

// grantKey 生成授权在状态里的键; 分隔符取 \0, 模型名与凭据名都不会含它。
export function grantKey(modelName: string, keyName: string) {
    return `${modelName}\0${keyName}`;
}

export const emptyFormState: ChannelFormState = {
    name: '',
    dialect: 'generic',
    base_url: '',
    enabled: true,
    proxy: false,
    openai_chat_completion_path: '/v1/chat/completions',
    openai_response_path: '/v1/responses',
    anthropic_message_path: '/v1/messages',
    openai_image_generation_path: '/v1/images/generations',
    openai_image_edit_path: '/v1/images/edits',
    keys: [],
    models: [],
    grants: new Map(),
    custom_header: [],
    channel_proxy: '',
    param_override: '',
    match_regex: '',
};

// fromChannel 把渠道完整配置还原为表单状态; 授权读写都按名称, 直接建索引即可。
export function fromChannel(channel: ChannelDetail): ChannelFormState {
    return {
        name: channel.name,
        dialect: channel.dialect,
        base_url: channel.base_url,
        enabled: channel.enabled,
        proxy: channel.proxy,
        openai_chat_completion_path: channel.openai_chat_completion_path,
        openai_response_path: channel.openai_response_path,
        anthropic_message_path: channel.anthropic_message_path,
        openai_image_generation_path: channel.openai_image_generation_path,
        openai_image_edit_path: channel.openai_image_edit_path,
        keys: channel.keys.map(({ name, key, enabled }) => ({ name, key, enabled })),
        models: [...channel.models],
        grants: new Map(channel.grants.map((g) => [grantKey(g.model_name, g.key_name), g.protocols])),
        custom_header: channel.custom_header,
        channel_proxy: channel.channel_proxy,
        param_override: channel.param_override,
        match_regex: channel.match_regex,
    };
}

// toChannelConfig 生成渠道自身的配置字段, 提交与探测共用。
// 探测只用得上其中的地址, 路径, 代理与过滤表达式, 但必须与保存后生效的完全一致, 故由同一处给出。
export function toChannelConfig(state: ChannelFormState) {
    return {
        name: state.name.trim(),
        dialect: state.dialect,
        enabled: state.enabled,
        base_url: state.base_url.trim(),
        openai_chat_completion_path: state.openai_chat_completion_path.trim(),
        openai_response_path: state.openai_response_path.trim(),
        anthropic_message_path: state.anthropic_message_path.trim(),
        openai_image_generation_path: state.openai_image_generation_path.trim(),
        openai_image_edit_path: state.openai_image_edit_path.trim(),
        proxy: state.proxy,
        custom_header: state.custom_header.filter((h) => h.header_key.trim() && h.header_value !== ''),
        channel_proxy: state.channel_proxy.trim(),
        param_override: state.param_override.trim(),
        match_regex: state.match_regex.trim(),
    };
}

// toChannelDetail 把表单状态还原为提交用的完整配置; 创建时 id 取 0, 由后端分配。
// 读写同构, 提交即全量: 无需与原渠道逐字段比对, 表单本就一次给出完整配置。
// 协议位为空的条目不是授权, 在此丢弃。
export function toChannelDetail(state: ChannelFormState, id: number): ChannelDetail {
    return {
        ...toChannelConfig(state),
        id,
        keys: state.keys.map(({ name, key, enabled }) => ({ name: name.trim(), key: key.trim(), enabled })),
        models: [...state.models],
        grants: [...state.grants]
            .filter(([, protocols]) => protocols !== 0)
            .map(([mapKey, protocols]) => {
                const [model_name, key_name] = mapKey.split('\0');
                return { model_name, key_name, protocols };
            }),
    };
}
