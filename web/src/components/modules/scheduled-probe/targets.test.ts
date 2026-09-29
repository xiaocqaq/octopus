import { test } from 'node:test';
import assert from 'node:assert/strict';
import { distinct, expandTargets } from './targets';

// 三个渠道的模型集合：2 与 7 共有 deepseek-chat，grok-fail 只有 3 有。
const modelsOf = new Map<number, string[]>([
    [2, ['grok-4.7', 'deepseek-chat']],
    [3, ['grok-fail']],
    [7, ['deepseek-chat', 'deepseek-reasoner']],
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
