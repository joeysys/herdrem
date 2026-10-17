// Tests for the service-worker update banner (`registerType: 'prompt'`):
// a waiting SW must surface as a visible bar, ✕ dismisses it until the next
// needRefresh event, and the update action is the only path that triggers
// SKIP_WAITING via updateServiceWorker().

import { describe, it, expect, vi, beforeEach } from 'vitest';
import { mount } from '@vue/test-utils';
import { setActivePinia, createPinia } from 'pinia';
import { nextTick } from 'vue';
import App from './App.vue';
import { useConnectionStore } from './stores/connection';
import {
  needRefreshRef,
  updateServiceWorker,
  lastOptions,
  resetRegisterSwMock,
} from './__mocks__/virtual-pwa-register-vue';

vi.mock('./api/ws', () => ({
  ws: {
    send: vi.fn(() => true),
    connect: vi.fn(),
    disconnect: vi.fn(),
    onMessage: vi.fn(() => () => {}),
    onStatus: vi.fn(() => () => {}),
    status: 'connected',
  },
}));

function bootedApp() {
  setActivePinia(createPinia());
  const wrapper = mount(App);
  useConnectionStore().booted = true;
  return wrapper;
}

describe('SW update banner', () => {
  beforeEach(() => {
    resetRegisterSwMock();
  });

  it('does not render while no update is waiting', async () => {
    const wrapper = bootedApp();
    await nextTick();
    expect(wrapper.find('.sw-update').exists()).toBe(false);
    wrapper.unmount();
  });

  it('renders the bar when needRefresh turns true', async () => {
    const wrapper = bootedApp();
    await nextTick();
    needRefreshRef.value = true;
    await nextTick();
    const bar = wrapper.find('.sw-update');
    expect(bar.exists()).toBe(true);
    expect(bar.text()).toContain('new version available');
    wrapper.unmount();
  });

  it('✕ dismisses the bar and a new needRefresh event shows it again', async () => {
    const wrapper = bootedApp();
    needRefreshRef.value = true;
    await nextTick();
    await wrapper.find('.sw-update-x').trigger('click');
    expect(wrapper.find('.sw-update').exists()).toBe(false);

    // The next update arriving re-arms the banner (reset of updateDismissed).
    lastOptions?.onNeedRefresh?.();
    await nextTick();
    expect(wrapper.find('.sw-update').exists()).toBe(true);
    wrapper.unmount();
  });

  it('update & reload calls updateServiceWorker()', async () => {
    const wrapper = bootedApp();
    needRefreshRef.value = true;
    await nextTick();
    await wrapper.find('.sw-update-go').trigger('click');
    expect(updateServiceWorker).toHaveBeenCalledTimes(1);
    wrapper.unmount();
  });
});
