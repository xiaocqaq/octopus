import { test } from 'node:test';
import assert from 'node:assert/strict';
import { distinct, expandTargets, sameTargetSet } from './targets';

// 三个渠道的模型集合：2 与 7 共有 deepseek-chat，grok-fail 只有 3 有。
const modelsOf = new Map<number, string[]>([
    [2, ['grok-4.7', 'deepseek-chat']],
    [3, ['grok-fail']],
    [7, ['deepseek-chat', 'deepseek-reasoner']],
]);

// 预览实例的真实形状：渠道 3 同时有 deepseek-chat 与 grok-fail。
const previewModels = new Map<number, string[]>([
    [2, ['deepseek-chat', 'grok-4.7']],
    [3, ['deepseek-chat', 'grok-fail']],
]);

test('一个渠道配多个模型时，每个模型各成一个目标', () => {
    assert.deepEqual(expandTargets([2], ['grok-4.7', 'deepseek-chat'], modelsOf), [
        { channel_id: 2, model_name: 'grok-4.7' },
        { channel_id: 2, model_name: 'deepseek-chat' },
    ]);
});

test('多选渠道时，模型只与真正提供它的渠道配对', () => {
    // grok-fail 只有渠道 3 有：即便同时勾了渠道 2，也不该给渠道 2 造出一条 grok-fail。
    assert.deepEqual(expandTargets([2, 3], ['grok-fail'], modelsOf), [{ channel_id: 3, model_name: 'grok-fail' }]);
});

test('同名模型被多个渠道提供时各算一个目标', () => {
    assert.deepEqual(expandTargets([2, 7], ['deepseek-chat'], modelsOf), [
        { channel_id: 2, model_name: 'deepseek-chat' },
        { channel_id: 7, model_name: 'deepseek-chat' },
    ]);
});

test('渠道或模型为空、模型无处可挂时不产生目标', () => {
    assert.deepEqual(expandTargets([], ['grok-4.7'], modelsOf), []);
    assert.deepEqual(expandTargets([2], [], modelsOf), []);
    assert.deepEqual(expandTargets([2], ['deepseek-reasoner'], modelsOf), []);
});

test('渠道的模型列表尚未取回时，不凭空造出目标', () => {
    const partial = new Map<number, string[]>([[2, ['grok-4.7']]]);
    assert.deepEqual(expandTargets([2, 3], ['grok-4.7'], partial), [{ channel_id: 2, model_name: 'grok-4.7' }]);
});

test('重复的渠道或模型不会产生重复目标', () => {
    assert.deepEqual(expandTargets([2, 2], ['deepseek-chat', 'deepseek-chat'], modelsOf), [
        { channel_id: 2, model_name: 'deepseek-chat' },
    ]);
});

test('distinct 按首次出现的顺序去重', () => {
    assert.deepEqual(distinct([3, 2, 3, 2, 5]), [3, 2, 5]);
    assert.deepEqual(distinct(['b', 'a', 'b']), ['b', 'a']);
});

test('sameTargetSet 只问集合不问顺序', () => {
    const stored = [
        { channel_id: 2, model_name: 'grok-4.7' },
        { channel_id: 3, model_name: 'deepseek-chat' },
    ];
    assert.equal(sameTargetSet(stored, [...stored].reverse()), true);
    assert.equal(sameTargetSet(stored, [{ channel_id: 2, model_name: 'grok-4.7' }]), false);
});

test('sameTargetSet 认得出旧任务被展开撑大', () => {
    // 预览里真实存在的旧任务：存了 2 条，按「渠道 × 模型」重开会变成 3 条（补上 2/deepseek-chat）。
    // 这正是编辑表单必须提示"目标会变"的场景，认不出来用户就会在点开又保存之后莫名多出一条。
    const stored = [
        { channel_id: 2, model_name: 'grok-4.7' },
        { channel_id: 3, model_name: 'deepseek-chat' },
    ];
    const expanded = expandTargets([2, 3], ['grok-4.7', 'deepseek-chat'], previewModels);
    assert.deepEqual(expanded, [
        { channel_id: 2, model_name: 'deepseek-chat' },
        { channel_id: 2, model_name: 'grok-4.7' },
        { channel_id: 3, model_name: 'deepseek-chat' },
    ]);
    assert.equal(sameTargetSet(stored, expanded), false);
});

test('sameTargetSet 认得出条数相同但目标被换掉', () => {
    assert.equal(
        sameTargetSet([{ channel_id: 2, model_name: 'grok-4.7' }], [{ channel_id: 2, model_name: 'deepseek-chat' }]),
        false
    );
});
