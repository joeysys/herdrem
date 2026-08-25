import { describe, it, expect, vi, beforeEach } from 'vitest'
import { mount } from '@vue/test-utils'
import { createPinia, setActivePinia } from 'pinia'
import ShellKeys from './ShellKeys.vue'
import { useTerminalStore } from '../../stores/terminal'

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

beforeEach(() => {
  setActivePinia(createPinia())
})

describe('ShellKeys', () => {
  it('sends a special key to the active pane', async () => {
    const term = useTerminalStore()
    term.activePane = 'p1'
    const sendKey = vi.spyOn(term, 'sendKey').mockReturnValue(true)
    const wrapper = mount(ShellKeys, { props: { pane: { pane_id: 'p1', status: 'idle' } } })
    await wrapper.get('[aria-label="Escape"]').trigger('pointerdown')
    expect(sendKey).toHaveBeenCalledWith('Escape')
  })

  it('keeps commands and ctrl controls keyboard-accessible', () => {
    const wrapper = mount(ShellKeys, { props: { pane: { pane_id: 'p1', status: 'idle' } } })
    expect(wrapper.find('[aria-label="Shell commands"]').exists()).toBe(true)
    expect(wrapper.find('[aria-label="Ctrl modifier"]').exists()).toBe(true)
  })
})
