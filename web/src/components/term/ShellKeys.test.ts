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

import { safeStorage } from '../../utils/storage'
import { resetKeyRowState } from '../../composables/useKeyRowConfig'

beforeEach(() => {
  setActivePinia(createPinia())
  localStorage.clear()
  safeStorage.removeItem('herdrem_key_row_shell')
  resetKeyRowState('shell')
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

  it('sends Enter from the key row', async () => {
    const term = useTerminalStore()
    term.activePane = 'p1'
    const sendKey = vi.spyOn(term, 'sendKey').mockReturnValue(true)
    const wrapper = mount(ShellKeys, { props: { pane: { pane_id: 'p1', status: 'idle' } } })
    await wrapper.get('[aria-label="Enter"]').trigger('pointerdown')
    expect(sendKey).toHaveBeenCalledWith('Enter')
  })

  it('keeps commands and ctrl controls keyboard-accessible', () => {
    const wrapper = mount(ShellKeys, { props: { pane: { pane_id: 'p1', status: 'idle' } } })
    expect(wrapper.find('[aria-label="Shell commands"]').exists()).toBe(true)
    expect(wrapper.find('[aria-label="Ctrl modifier"]').exists()).toBe(true)
  })

  it('toggles the input box and remembers the choice', async () => {
    const wrapper = mount(ShellKeys, { props: { pane: { pane_id: 'p1', status: 'idle' } } })
    expect(wrapper.find('.input-row').exists()).toBe(false)
    const toggle = wrapper.get('[aria-label="Toggle input box"]')
    expect(toggle.attributes('aria-pressed')).toBe('false')

    await toggle.trigger('click')
    expect(wrapper.find('.input-row').exists()).toBe(true)
    expect(toggle.attributes('aria-pressed')).toBe('true')
    expect(localStorage.getItem('herdrem_shell_input')).toBe('true')

    // remount restores the persisted choice
    const remounted = mount(ShellKeys, { props: { pane: { pane_id: 'p1', status: 'idle' } } })
    expect(remounted.find('.input-row').exists()).toBe(true)

    await toggle.trigger('click')
    expect(wrapper.find('.input-row').exists()).toBe(false)
    expect(localStorage.getItem('herdrem_shell_input')).toBe('false')
  })

  it('submits the box text with enter and clears the draft', async () => {
    const term = useTerminalStore()
    term.activePane = 'p1'
    const sendText = vi.spyOn(term, 'sendText').mockReturnValue(true)
    localStorage.setItem('herdrem_shell_input', 'true')
    const wrapper = mount(ShellKeys, {
      props: { pane: { pane_id: 'p1', status: 'idle' }, modelValue: 'ls -la' },
    })

    await wrapper.get('form.input-row').trigger('submit')
    expect(sendText).toHaveBeenCalledWith('ls -la', true)
    expect(wrapper.emitted('update:modelValue')?.at(-1)).toEqual([''])
  })

  it('the box is a wrapping textarea: Enter sends, pasted line breaks are dropped', async () => {
    const term = useTerminalStore()
    term.activePane = 'p1'
    const sendText = vi.spyOn(term, 'sendText').mockReturnValue(true)
    localStorage.setItem('herdrem_shell_input', 'true')
    const wrapper = mount(ShellKeys, {
      props: {
        pane: { pane_id: 'p1', status: 'idle' },
        modelValue: '',
        'onUpdate:modelValue': (v: string) => wrapper.setProps({ modelValue: v }),
      },
    })
    const box = wrapper.get('textarea[aria-label="Command to send to shell"]')
    expect(box.attributes('rows')).toBe('1')
    await box.setValue('echo a\necho b')
    expect(wrapper.props('modelValue')).toBe('echo aecho b')

    const enter = new InputEvent('beforeinput', { inputType: 'insertLineBreak', cancelable: true })
    box.element.dispatchEvent(enter)
    expect(enter.defaultPrevented).toBe(true)
    expect(sendText).toHaveBeenCalledWith('echo aecho b', true)
  })

  it('opens KeyRowConfigModal when customize button is clicked', async () => {
    const wrapper = mount(ShellKeys, { props: { pane: { pane_id: 'p1', status: 'idle' } } })
    const configBtn = wrapper.find('button.config-btn')
    expect(configBtn.exists()).toBe(true)
    expect(wrapper.findComponent({ name: 'KeyRowConfigModal' }).exists()).toBe(false)

    await configBtn.trigger('click')
    expect(wrapper.findComponent({ name: 'KeyRowConfigModal' }).exists()).toBe(true)
  })

  it('closes arrows when customize button is clicked', async () => {
    const wrapper = mount(ShellKeys, { props: { pane: { pane_id: 'p1', status: 'idle' } } })
    const arrowBtn = wrapper.find('button[aria-label="Arrow pad"]')
    expect(arrowBtn.exists()).toBe(true)
    await arrowBtn.trigger('click')
    expect(document.body.querySelector('.arrow-pop')).not.toBeNull()

    const configBtn = wrapper.find('button.config-btn')
    await configBtn.trigger('click')
    expect(document.body.querySelector('.arrow-pop')).toBeNull()
    expect(wrapper.findComponent({ name: 'KeyRowConfigModal' }).exists()).toBe(true)
  })

  it('respects customized key row order and hidden keys', async () => {
    const customConfig = [
      { id: 'Tab', visible: true },
      { id: 'Enter', visible: true },
      { id: 'Escape', visible: false },
    ]
    safeStorage.setItem('herdrem_key_row_shell', JSON.stringify(customConfig))
    resetKeyRowState('shell')

    const wrapper = mount(ShellKeys, { props: { pane: { pane_id: 'p1', status: 'idle' } } })
    const buttons = wrapper.findAll('.key-row .key')
    const labels = buttons.map((b) => b.text())

    // Tab is first
    expect(labels[0]).toBe('Tab')
    expect(labels[1]).toBe('Enter')
    // Esc is hidden
    expect(labels.includes('Esc')).toBe(false)
  })
})
