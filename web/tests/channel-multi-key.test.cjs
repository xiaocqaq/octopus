// node --test tests/channel-multi-key.test.cjs
// 回归: 「全部凭据」模式下, 刷新/新增模型必须覆盖全部凭据。
// 曾经的缺陷: 刷新只探第一个凭据、新增只给第一个凭据写授权, 导致同一个模型挂多个 key 的渠道
// 在分组时只带进 1 个 key。这里直接执行真实 state 模块的纯函数, 不依赖浏览器。
const assert = require('node:assert/strict');
const fs = require('node:fs');
const path = require('node:path');
const vm = require('node:vm');
const { test } = require('node:test');
const ts = require('typescript');

const source = fs.readFileSync(path.join(__dirname, '../src/components/modules/channel/state.ts'), 'utf8');
const { outputText } = ts.transpileModule(source, {
    compilerOptions: { module: ts.ModuleKind.CommonJS, target: ts.ScriptTarget.ES2022 },
});
const context = { exports: {}, require: (name) => {
    // state.ts 只 import 类型, 运行时不需要任何依赖。
    throw new Error(`unexpected require: ${name}`);
} };
vm.runInNewContext(outputText, context);
const { probeTargetKeys, addModelGrantKeys, grantKey } = context.exports;

// vm 里造出来的数组不是本 realm 的 Array, deepStrictEqual 会因原型不同而失败; 统一转成宿主数组再比。
const list = (value) => Array.from(value);

const keys = [
    { name: 'key-a', key: 'sk-aaa' },
    { name: 'key-b', key: 'sk-bbb' },
    { name: 'key-c', key: '' }, // 还没填 Key
];

test('全部凭据模式: 刷新要探测所有已填 Key 的凭据', () => {
    assert.deepEqual(list(probeTargetKeys(keys, '')), ['key-a', 'key-b']);
});

test('全部凭据模式: 单个凭据也没被漏掉(多于两个凭据)', () => {
    const many = [
        { name: 'k1', key: 'a' },
        { name: 'k2', key: 'b' },
        { name: 'k3', key: 'c' },
        { name: 'k4', key: 'd' },
    ];
    assert.deepEqual(list(probeTargetKeys(many, '')), ['k1', 'k2', 'k3', 'k4']);
});

test('单选凭据模式: 只探测选中的那个', () => {
    assert.deepEqual(list(probeTargetKeys(keys, 'key-b')), ['key-b']);
});

test('没有填任何 Key 时不发请求', () => {
    assert.deepEqual(list(probeTargetKeys([{ name: 'k', key: '  ' }], '')), []);
});

test('全部凭据模式: 新增模型要覆盖全部凭据', () => {
    assert.deepEqual(list(addModelGrantKeys(['key-a', 'key-b'], true, 'key-a')), ['key-a', 'key-b']);
});

test('单选凭据模式: 新增模型只给选中的凭据', () => {
    assert.deepEqual(list(addModelGrantKeys(['key-a', 'key-b'], false, 'key-b')), ['key-b']);
});

test('全部凭据模式新增的模型, 每个凭据都有可引用的授权键', () => {
    const model = 'shared-model';
    const grantKeys = list(addModelGrantKeys(['key-a', 'key-b'], true, 'key-a')).map((k) => grantKey(model, k));
    assert.deepEqual(grantKeys, [grantKey(model, 'key-a'), grantKey(model, 'key-b')]);
    // 分组时前端就是按这些键挑出 grants 的, 两个都在才不会被只带进一个 key。
    assert.equal(grantKeys.length, 2);
});
