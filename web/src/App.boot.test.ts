// Boot-splash stability: while the first WS hello is outstanding the app
// must keep the splash through WS reconnect cycles instead of flashing the
// empty app shell on every failed dial — a restarting relay rejecting the
// session cookie used to oscillate splash ↔ app until the 5s boot timeout
// finally surfaced the token page.

import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest';
import type { MockInstance } from 'vitest';
import { mount } from '@vue/test-utils';
import { setActivePinia, createPinia } from 'pinia';
import { nextTick } from 'vue';
import App from './App.vue';
import { useConnectionStore } from './stores/connection';

vi.mock('./api/ws', () => ({
  ws: {
    send: vi.fn(() => true),
    connect: vi.fn(),
    disconnect: vi.fn(),
    onMessage: vi.fn(() => () => {}),
    onStatus: vi.fn(() => () => {}),
    status: 'disconnected',
  },
}));

let fetchMock: MockInstance<typeof fetch>;

beforeEach(() => {
  setActivePinia(createPinia());
  localStorage.clear();
  sessionStorage.clear();
  fetchMock = vi.spyOn(globalThis, 'fetch').mockImplementation(() => new Promise<Response>(() => {}));
});

afterEach(() => {
  fetchMock.mockRestore();
});

async function flush() {
  await new Promise((r) => setTimeout(r, 20));
  await nextTick();
  await nextTick();
}

describe('App boot splash', () => {
  it('keeps the splash through reconnect cycles until the first hello', async () => {
    const wrapper = mount(App);
    const conn = useConnectionStore();
    await nextTick();

    // Tokenless same-origin boot (jsdom origin) — splash, no app shell.
    expect(wrapper.find('.boot-splash').exists()).toBe(true);
    expect(wrapper.find('.app').exists()).toBe(false);

    conn.status = 'connecting';
    await nextTick();
    expect(wrapper.find('.boot-splash').exists()).toBe(true);
    expect(wrapper.find('.app').exists()).toBe(false);

    // A failed dial between retries must not reveal the empty app.
    conn.status = 'disconnected';
    await nextTick();
    expect(wrapper.find('.boot-splash').exists()).toBe(true);
    expect(wrapper.find('.app').exists()).toBe(false);

    // First hello ends the boot gate and renders the real UI.
    conn.setHello('0.4.0', []);
    await nextTick();
    expect(wrapper.find('.boot-splash').exists()).toBe(false);
    expect(wrapper.find('.app').exists()).toBe(true);
  });

  it('goes splash → token page when the session cookie is dead', async () => {
    // Stale same-origin session hint + every /session-auth probe answering
    // 401: the relay restarted and dropped the in-memory session.
    localStorage.setItem('herdrem_session', '1');
    fetchMock.mockResolvedValue({ status: 401 } as Response);
    const wrapper = mount(App);
    await flush();

    // Store probe → failBootToAuth → watcher re-probe → serverNeedsToken.
    expect(wrapper.find('.relay-auth-form').exists()).toBe(true);
    expect(wrapper.find('.app').exists()).toBe(false);
    // The prompt names the relay being authenticated — same-origin here.
    expect(wrapper.find('.relay-auth-url').text()).toContain(location.host);
    expect(localStorage.getItem('herdrem_session')).toBeNull();
  });
});
