// TunnelPanel req-echo UI: a submitted create keeps the PIN while its echo
// is outstanding — the typed PIN is the receipt's snapshot, so a failure can
// hand it straight back instead of forcing a retype.

import { describe, it, expect, vi, beforeEach } from 'vitest'
import { mount } from '@vue/test-utils'
import { setActivePinia, createPinia } from 'pinia'
import { nextTick } from 'vue'
import TunnelPanel from './TunnelPanel.vue'
import { useTunnelsStore, portScope } from '../../stores/tunnels'
import { useConnectionStore } from '../../stores/connection'
import { ws } from '../../api/ws'

vi.mock('../../api/ws', () => ({
  ws: {
    send: vi.fn(() => true),
    connect: vi.fn(),
    disconnect: vi.fn(),
    onMessage: vi.fn(() => () => {}),
    onStatus: vi.fn(() => () => {}),
    status: 'connected',
  },
}))

const pinInput = (wrapper: ReturnType<typeof mount>) => wrapper.find('input[placeholder="Auto-generated if empty"]')

beforeEach(() => {
  setActivePinia(createPinia())
  vi.mocked(ws.send).mockClear().mockReturnValue(true)
  useConnectionStore().setHello('0.4.0', ['req-echo', 'tunnels'])
})

async function submitTunnel(wrapper: ReturnType<typeof mount>, pin: string) {
  await pinInput(wrapper).setValue(pin)
  await wrapper.find('.tp-submit-btn').trigger('click')
  await nextTick()
}

describe('TunnelPanel req-echo UI', () => {
  it('keeps the PIN while the create is pending and disables resubmits', async () => {
    const wrapper = mount(TunnelPanel)
    await submitTunnel(wrapper, 'my-pin')

    expect(wrapper.find('.tp-pending').text()).toContain('Requesting tunnel…')
    expect((pinInput(wrapper).element as HTMLInputElement).value).toBe('my-pin')
    expect(pinInput(wrapper).attributes('readonly')).toBeDefined()
    expect(wrapper.find('.tp-submit-btn').attributes('disabled')).toBeDefined()
  })

  it('a confirmed echo clears the PIN', async () => {
    const tunnels = useTunnelsStore()
    const wrapper = mount(TunnelPanel)
    await submitTunnel(wrapper, 'my-pin')

    tunnels.confirm(tunnels.pendingFor(portScope)!.reqId)
    await nextTick()

    expect(wrapper.find('.tp-pending').exists()).toBe(false)
    expect((pinInput(wrapper).element as HTMLInputElement).value).toBe('')
  })

  it('a failed echo shows the message and hands the PIN back', async () => {
    const tunnels = useTunnelsStore()
    const wrapper = mount(TunnelPanel)
    await submitTunnel(wrapper, 'my-pin')

    tunnels.fail(tunnels.pendingFor(portScope)!.reqId, 'port not allowed')
    await nextTick()

    expect(wrapper.find('.tp-pending').text()).toContain('port not allowed')
    expect((pinInput(wrapper).element as HTMLInputElement).value).toBe('my-pin')
  })

  it('clears the PIN immediately when the relay has no req-echo', async () => {
    useConnectionStore().setHello('0.4.0', ['tunnels'])
    const wrapper = mount(TunnelPanel)
    await submitTunnel(wrapper, 'my-pin')

    expect(wrapper.find('.tp-pending').exists()).toBe(false)
    expect((pinInput(wrapper).element as HTMLInputElement).value).toBe('')
  })

  it('shows not connected on a failed send and keeps the PIN', async () => {
    vi.mocked(ws.send).mockReturnValue(false)
    const wrapper = mount(TunnelPanel)
    await submitTunnel(wrapper, 'my-pin')

    expect(wrapper.find('.tp-pending').text()).toContain('not connected')
    expect((pinInput(wrapper).element as HTMLInputElement).value).toBe('my-pin')
  })

  it('a late echo after unconfirmed still clears the PIN and message', async () => {
    vi.useFakeTimers()
    try {
      const tunnels = useTunnelsStore()
      const wrapper = mount(TunnelPanel)
      await submitTunnel(wrapper, 'my-pin')
      const op = tunnels.pendingFor(portScope)!

      vi.advanceTimersByTime(10_000)
      await nextTick()
      expect(wrapper.find('.tp-pending').text()).toContain('Unconfirmed')

      tunnels.confirm(op.reqId)
      await nextTick()
      expect(wrapper.find('.tp-pending').exists()).toBe(false)
      expect((pinInput(wrapper).element as HTMLInputElement).value).toBe('')
    } finally {
      vi.useRealTimers()
    }
  })
})
