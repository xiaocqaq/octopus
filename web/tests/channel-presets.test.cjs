// node --test tests/channel-presets.test.cjs
// 执行真实预设模块；仅替换与 URL 无关的图标导入，不发送上游请求。
const assert = require('node:assert/strict');
const fs = require('node:fs');
const path = require('node:path');
const vm = require('node:vm');
const { test } = require('node:test');
const ts = require('typescript');
const source = fs.readFileSync(path.join(__dirname, '../src/lib/channel-presets.tsx'), 'utf8');
const { outputText } = ts.transpileModule(source, {
    compilerOptions: { module: ts.ModuleKind.CommonJS, target: ts.ScriptTarget.ES2022 },
});
const context = { exports: {}, require: (name) => {
    assert.match(name, /^@thesvg\/react\//);
    return { default: () => null };
} };
vm.runInNewContext(outputText, context);
const { CHANNEL_PRESETS, IMG_GEN, IMG_EDIT } = context.exports;
const presets = new Map(CHANNEL_PRESETS.map((p) => [p.id, p]));

const expected = {
    volcengine: ['https://ark.cn-beijing.volces.com', '/api/v3/chat/completions', '/api/v3/responses', '/api/compatible/v1/messages'],
    deepseek: ['https://api.deepseek.com', '/chat/completions', '/responses', '/anthropic/v1/messages'],
    dashscope: ['https://dashscope.aliyuncs.com', '/compatible-mode/v1/chat/completions', '/compatible-mode/v1/responses', '/apps/anthropic/v1/messages'],
    moonshot: ['https://api.moonshot.cn', '/v1/chat/completions', '/v1/responses', '/anthropic/v1/messages'],
    zhipu: ['https://open.bigmodel.cn', '/api/paas/v4/chat/completions', '/api/v1/responses', '/api/anthropic/v1/messages'],
    azure: ['', '/openai/v1/chat/completions', '/openai/v1/responses', '/v1/messages'],
};
for (const [id, values] of Object.entries(expected)) {
    test(`${id}: 上游预设路径一致`, () => {
        const p = presets.get(id);
        assert.deepEqual([p.base_url, p.openai_chat_completion_path, p.openai_response_path, p.anthropic_message_path], values);
    });
}
for (const [id, prefix] of Object.entries({
    volcengine: 'https://ark.cn-beijing.volces.com/api/v3',
    zhipu: 'https://open.bigmodel.cn/api/paas/v4',
    dashscope: 'https://dashscope.aliyuncs.com/compatible-mode/v1',
})) {
    test(`${id}: 保留原有完整图片 URL`, () => {
        const p = presets.get(id);
        assert.equal(p.base_url + (p.openai_image_generation_path ?? IMG_GEN), prefix + '/images/generations');
        assert.equal(p.base_url + (p.openai_image_edit_path ?? IMG_EDIT), prefix + '/images/edits');
    });
}
test('所有预设 ID 唯一且接口路径为绝对路径', () => {
    assert.equal(presets.size, CHANNEL_PRESETS.length);
    for (const p of CHANNEL_PRESETS) {
        for (const field of ['openai_chat_completion_path', 'openai_response_path', 'anthropic_message_path']) {
            assert.ok(p[field].startsWith('/'), `${p.id}: ${field}`);
        }
    }
});
