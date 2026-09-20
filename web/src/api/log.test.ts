import { test } from 'node:test';
import assert from 'node:assert/strict';
import { getRetryErrors, type RelayLogOverview, type RelayRetryError } from './log';

const failure: RelayRetryError = {
    round: 1,
    target_channel: 'selected-provider',
    target_model: 'upstream-model',
    error: '401: invalid upstream key',
};

function snapshot(overrides: Partial<RelayLogOverview> = {}): RelayLogOverview {
    return {
        id: 42, status: 'running', started_at: '2026-01-01T00:00:00Z',
        duration: 0, first_token_duration: 0, stream_duration: 0, response_duration: 0,
        model: 'requested-model', protocol: 1, group_id: 1, api_key_name: 'test',
        usage: { prompt_tokens: 0, completion_tokens: 0, total_tokens: 0, prompt_tokens_details: null },
        cost: 0, output_chars: 0, round: 2, round_started_at: '2026-01-01T00:00:01Z',
        target_channel: 'next-provider', target_model: 'next-model', target_protocol: 1, sending: true,
        ...overrides,
    };
}

test('server failure remains visible after the next provider clears the current error', () => {
    const log = snapshot({ retry_errors: [failure], error: '' });
    assert.deepEqual(getRetryErrors(log), [failure]);
    assert.equal(log.target_channel, 'next-provider');
});

test('opening or reopening details while waiting for manual selection preserves the failure', () => {
    const log = snapshot({ sending: false, retry_errors: [failure] });
    assert.deepEqual(getRetryErrors(log), [failure]);
    assert.deepEqual(getRetryErrors(structuredClone(log)), [failure]);
});

test('history survives streaming, success, final failure and cancellation without absorbing final errors', () => {
    for (const status of ['committed', 'success', 'failed', 'canceled'] as const) {
        const log = snapshot({ status, retry_errors: [failure], error: 'final request outcome' });
        assert.deepEqual(getRetryErrors(log), [failure]);
        assert.equal(log.error, 'final request outcome');
    }
});

test('current running failure merges with history in newest-first order without mutating snapshots', () => {
    const log = snapshot({ retry_errors: [failure], error: '503: unavailable' });
    const original = structuredClone(log);
    assert.deepEqual(getRetryErrors(log), [
        { round: 2, target_channel: 'next-provider', target_model: 'next-model', error: '503: unavailable' },
        failure,
    ]);
    assert.deepEqual(log, original);
});

test('authoritative server error wins when the current round is already in history', () => {
    const log = snapshot({ round: 1, retry_errors: [failure], error: 'transient snapshot' });
    assert.deepEqual(getRetryErrors(log), [failure]);
});

test('omitted history and a request canceled before its first attempt produce no invented failures', () => {
    assert.deepEqual(getRetryErrors(snapshot()), []);
    assert.deepEqual(getRetryErrors(snapshot({ status: 'canceled', round: 0, error: 'client canceled' })), []);
});
