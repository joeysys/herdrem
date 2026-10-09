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
import { useRelaysStore } from './stores/relays';
import { ws } from './api/ws';

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

import { safeStorage } from './utils/storage';

let fetchMock: MockInstance<typeof fetch>;

beforeEach(() => {
  setActivePinia(createPinia());
  safeStorage.clear();
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
    wrapper.unmount();
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
    wrapper.unmount();
  });

  it('can switch to another relay from the token page', async () => {
    const relays = useRelaysStore();
    const blocked = relays.add('blocked', 'wss://blocked.example');
    const working = relays.add('working', 'wss://working.example', 'working-token');
    relays.setCurrent(blocked.id);
    const wrapper = mount(App);
    await nextTick();

    expect(wrapper.find('.relay-auth-form').exists()).toBe(true);
    await wrapper.find(`[data-relay-id="${working.id}"]`).trigger('click');
    expect(relays.currentId).toBe(working.id);
    expect(ws.connect).toHaveBeenCalledWith(working.url, 'working-token');
    expect(wrapper.find('.relay-auth-form').exists()).toBe(false);
    wrapper.unmount();
  });

  it('does not carry a typed token to a different relay', async () => {
    const relays = useRelaysStore();
    const first = relays.add('first', 'wss://first.example');
    const second = relays.add('second', 'wss://second.example');
    relays.setCurrent(first.id);
    const wrapper = mount(App);
    await wrapper.find('#boot-relay-token').setValue('first-secret');
    await wrapper.find(`[data-relay-id="${second.id}"]`).trigger('click');

    expect((wrapper.find('#boot-relay-token').element as HTMLInputElement).value).toBe('');
    expect(wrapper.find('.relay-auth-url').text()).toBe(second.url);
    wrapper.unmount();
  });

  it('uses a token saved in settings without showing the token page', async () => {
    const relays = useRelaysStore();
    const saved = relays.add('saved', 'wss://saved.example', 'saved-token');
    relays.setCurrent(saved.id);
    setActivePinia(createPinia());
    const wrapper = mount(App);
    await nextTick();

    expect(wrapper.find('.relay-auth-form').exists()).toBe(false);
    expect(ws.connect).toHaveBeenCalledWith(saved.url, 'saved-token');
    wrapper.unmount();
  });

  it('can reveal and hide the token on the authentication page', async () => {
    const relays = useRelaysStore();
    relays.setCurrent(relays.add('remote', 'wss://remote.example').id);
    const wrapper = mount(App);
    await nextTick();
    expect(wrapper.find('#boot-relay-token').attributes('type')).toBe('password');
    await wrapper.find('.relay-token-toggle').trigger('click');
    expect(wrapper.find('#boot-relay-token').attributes('type')).toBe('text');
    wrapper.unmount();
  });

  it('can save a token from settings while the token page is open', async () => {
    const relays = useRelaysStore();
    const remote = relays.add('remote', 'wss://remote.example');
    relays.setCurrent(remote.id);
    const wrapper = mount(App);
    await wrapper.find('.boot-relay-list button:last-child').trigger('click');
    expect(wrapper.find('.settings-panel').exists()).toBe(true);
    const edit = wrapper.findAll('button[aria-label="Edit"]').at(-1)!;
    await edit.trigger('click');
    await wrapper.find('input[aria-label="Relay token"]').setValue('new-token');
    await wrapper.find('.rm-form-actions .primary').trigger('click');

    expect(ws.connect).toHaveBeenCalledWith(remote.url, 'new-token');
    expect(wrapper.find('.relay-auth-form').exists()).toBe(false);
    wrapper.unmount();
  });
});
