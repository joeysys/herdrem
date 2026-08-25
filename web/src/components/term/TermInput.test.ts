import { describe, it, expect, vi, beforeEach } from 'vitest'
import { mount } from '@vue/test-utils'
import { createPinia, setActivePinia } from 'pinia'
import TermInput from './TermInput.vue'
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

const pane = { pane_id: 'p1', status: 'idle', agent: 'codex' }

beforeEach(() => {
  setActivePinia(createPinia())
})

describe('TermInput', () => {
  it('disables fill when the input is empty', () => {
    const wrapper = mount(TermInput, { props: { pane } })
    const send = wrapper.get('button.send-btn')
    expect(send.attributes('disabled')).toBeDefined()
    expect(send.attributes('aria-label')).toBe('Fill command into terminal')
  })

  it('fills then sends on two-phase submit', async () => {
    const term = useTerminalStore()
    term.activePane = 'p1'
    const sendText = vi.spyOn(term, 'sendText').mockReturnValue(true)
    const sendKey = vi.spyOn(term, 'sendKey').mockReturnValue(true)

    const wrapper = mount(TermInput, { props: { pane } })
    await wrapper.get('input.cmd').setValue('echo hi')
    await wrapper.get('form.input-row').trigger('submit')
    expect(sendText).toHaveBeenCalledWith('echo hi', false)
    expect(wrapper.get('button.send-btn').attributes('aria-label')).toBe('Send command to pane')

    await wrapper.get('form.input-row').trigger('submit')
    expect(sendKey).toHaveBeenCalledWith('Enter')
  })

  it('quick-sends on Ctrl+Enter', async () => {
    const term = useTerminalStore()
    term.activePane = 'p1'
    const sendText = vi.spyOn(term, 'sendText').mockReturnValue(true)

    const wrapper = mount(TermInput, { props: { pane } })
    await wrapper.get('input.cmd').setValue('pwd')
    await wrapper.get('input.cmd').trigger('keydown', { key: 'Enter', ctrlKey: true })
    expect(sendText).toHaveBeenCalledWith('pwd', true)
  })

  it('exposes keyboard-accessible special keys', () => {
    const wrapper = mount(TermInput, { props: { pane } })
    expect(wrapper.get('input.cmd').attributes('aria-label')).toBe('Command to send to pane')
    expect(wrapper.find('[aria-label="Enter"]').exists()).toBe(true)
    expect(wrapper.find('[aria-label="Ctrl modifier"]').exists()).toBe(true)
  })
})
