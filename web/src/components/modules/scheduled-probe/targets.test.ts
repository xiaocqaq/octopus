import { test } from 'node:test';
import assert from 'node:assert/strict';
import { targetFromKey, targetKey } from './targets';

// 多选值必须保留真实的渠道/模型组合，不产生笛卡尔积。
test('targetKey 编码渠道与模型组合', () => {
    assert.equal(targetKey({ channel_id: 2, model_name: 'grok-4.7' }), '2\u0000grok-4.7');
});

test('targetFromKey 解码合法多选值', () => {
    assert.deepEqual(targetFromKey('7\u0000deepseek-chat'), {
        channel_id: 7,
        model_name: 'deepseek-chat',
    });
});

test('targetFromKey 拒绝无效值', () => {
    assert.equal(targetFromKey(''), null);
    assert.equal(targetFromKey('0\u0000model'), null);
    assert.equal(targetFromKey('abc\u0000model'), null);
    assert.equal(targetFromKey('2\u0000'), null);
    assert.equal(targetFromKey('2\u0000   '), null);
});

test('多选值往返不合并同名模型', () => {
    const values = [
        targetKey({ channel_id: 2, model_name: 'same-model' }),
        targetKey({ channel_id: 3, model_name: 'same-model' }),
    ];
    assert.deepEqual(values.map((value) => targetFromKey(value)), [
        { channel_id: 2, model_name: 'same-model' },
        { channel_id: 3, model_name: 'same-model' },
    ]);
});
