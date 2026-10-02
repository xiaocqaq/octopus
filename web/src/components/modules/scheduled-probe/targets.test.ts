import { test } from 'node:test';
import assert from 'node:assert/strict';
import type { ChannelGrantCandidate } from '@/api/channel';
import { buildTargetOptions, targetFromKey, targetKey, targetValueForKey, targetsOf } from './targets';

const SEP = '\u0000';

// 多选值的三段必须能原样往返：渠道、凭据、模型。
test('targetKey 编码渠道/凭据/模型三段', () => {
    assert.equal(targetKey(2, 'key-1', 'grok-4.7'), `2${SEP}key-1${SEP}grok-4.7`);
});

test('targetFromKey 解码合法多选值', () => {
    assert.deepEqual(targetFromKey(`7${SEP}default${SEP}deepseek-chat`), {
        channelId: 7,
        keyName: 'default',
        modelName: 'deepseek-chat',
        target: { channel_id: 7, model_name: 'deepseek-chat' },
    });
});

// 缺段或多段一律当作无效：宁可什么都不选，也不要猜出一个错的渠道号。
test('targetFromKey 拒绝无效值', () => {
    assert.equal(targetFromKey(''), null);
    assert.equal(targetFromKey(`0${SEP}key${SEP}model`), null);
    assert.equal(targetFromKey(`abc${SEP}key${SEP}model`), null);
    assert.equal(targetFromKey(`2${SEP}key${SEP}`), null);
    assert.equal(targetFromKey(`2${SEP}key${SEP}   `), null);
    // 只有两段（旧版「渠道/模型」的值）必须拒绝，不能把模型名当凭据名吞下去。
    assert.equal(targetFromKey(`2${SEP}grok-4.7`), null);
    assert.equal(targetFromKey(`2${SEP}key${SEP}model${SEP}extra`), null);
});

test('多选值往返不合并同名模型', () => {
    const values = [targetKey(2, 'a', 'same-model'), targetKey(3, 'b', 'same-model')];
    assert.deepEqual(values.map((value) => targetFromKey(value)?.target), [
        { channel_id: 2, model_name: 'same-model' },
        { channel_id: 3, model_name: 'same-model' },
    ]);
});

// 同一个 (渠道, 模型) 下勾了几条凭据，就折成一条目标，没勾到的进 excluded_keys。
test('targetsOf 把同一目标的多个凭据折成一条并排除未选的', () => {
    const candidates: ChannelGrantCandidate[] = [
        { id: 1, channel_id: 66, channel_name: '仙人', model_name: 'gpt-6-astra', key_name: '0001', protocols: 1, available: true },
        { id: 2, channel_id: 66, channel_name: '仙人', model_name: 'gpt-6-astra', key_name: '0069', protocols: 1, available: true },
        { id: 3, channel_id: 66, channel_name: '仙人', model_name: 'gpt-6-astra', key_name: '0089', protocols: 1, available: true },
    ];
    assert.deepEqual(targetsOf([targetKey(66, '0001', 'gpt-6-astra')], candidates), [
        { channel_id: 66, model_name: 'gpt-6-astra', excluded_keys: ['0069', '0089'] },
    ]);
});

// 全选该 (渠道, 模型) 下的凭据时不留排除项：空表示"没有排除"，
// 将来渠道新加的凭据也会自动纳入，而不是被一份列全的名单挡在门外。
test('targetsOf 全选凭据时不产生排除项', () => {
    const candidates: ChannelGrantCandidate[] = [
        { id: 1, channel_id: 8, channel_name: '林夕', model_name: 'claude-opus-5', key_name: 'default', protocols: 1, available: true },
        { id: 2, channel_id: 8, channel_name: '林夕', model_name: 'claude-opus-5', key_name: 'free', protocols: 1, available: true },
    ];
    assert.deepEqual(targetsOf([targetKey(8, 'default', 'claude-opus-5'), targetKey(8, 'free', 'claude-opus-5')], candidates), [
        { channel_id: 8, model_name: 'claude-opus-5', excluded_keys: [] },
    ]);
});

// 候选里查不到这一对（渠道刚被停用）时不给排除项：宁可不排除，也不要凭空缩小监控范围。
test('targetsOf 候选缺失时保持空排除项', () => {
    assert.deepEqual(targetsOf([targetKey(999, 'ghost', 'model-x')], []), [
        { channel_id: 999, model_name: 'model-x', excluded_keys: [] },
    ]);
});

// 回填要挑该目标下真实存在、且没有被排除的那条凭据。
test('targetValueForKey 优先挑未被排除的凭据', () => {
    const candidates: ChannelGrantCandidate[] = [
        { id: 1, channel_id: 66, channel_name: '仙人', model_name: 'gpt-6-astra', key_name: '0001', protocols: 1, available: true },
        { id: 2, channel_id: 66, channel_name: '仙人', model_name: 'gpt-6-astra', key_name: '0069', protocols: 1, available: true },
    ];
    assert.equal(
        targetValueForKey({ channel_id: 66, model_name: 'gpt-6-astra', excluded_keys: ['0001'] }, candidates),
        targetKey(66, '0069', 'gpt-6-astra'),
    );
});

// 候选还没到（首次渲染）或该目标下一条凭据都不剩时，回填"整条目标"的空凭据值，
// 至少让用户看见目标还在，而不是整条消失。
test('targetValueForKey 在候选缺失时回填空凭据值', () => {
    assert.equal(
        targetValueForKey({ channel_id: 7, model_name: 'deepseek-chat' }, []),
        targetKey(7, '', 'deepseek-chat'),
    );
});

// 同名凭据只出一条选项：后端按名字定位凭据，渲染两遍只会让人以为漏勾了其中一条。
test('buildTargetOptions 去重同名凭据并保留三段标签', () => {
    const candidates: ChannelGrantCandidate[] = [
        { id: 1, channel_id: 13, channel_name: '百倍', model_name: 'claude-opus-5', key_name: 'default', protocols: 1, available: true },
        { id: 2, channel_id: 13, channel_name: '百倍', model_name: 'claude-opus-5', key_name: 'default', protocols: 1, available: true },
        { id: 3, channel_id: 13, channel_name: '百倍', model_name: 'claude-opus-5', key_name: 'free', protocols: 1, available: true },
    ];
    const options = buildTargetOptions(candidates);
    assert.equal(options.length, 2);
    assert.deepEqual(options.map((option) => option.label), ['百倍 default claude-opus-5', '百倍 free claude-opus-5']);
});
