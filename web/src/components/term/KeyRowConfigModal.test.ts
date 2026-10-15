import { describe, it, expect, beforeEach } from 'vitest'
import { mount } from '@vue/test-utils'
import KeyRowConfigModal from './KeyRowConfigModal.vue'
import { safeStorage } from '../../utils/storage'
import { resetKeyRowState } from '../../composables/useKeyRowConfig'
import { overlayNow, closeTopRegisteredLayer, resetShortcutsForTests } from '../../composables/useShortcuts'

beforeEach(() => {
  resetShortcutsForTests()
  safeStorage.removeItem('herdrem_key_row_agent')
  safeStorage.removeItem('herdrem_key_row_shell')
  resetKeyRowState('agent')
  resetKeyRowState('shell')
})

describe('KeyRowConfigModal', () => {
  it('renders agent pane keys by default and allows switching tabs', async () => {
    const wrapper = mount(KeyRowConfigModal, { props: { type: 'agent' } })
    expect(wrapper.find('.krc-modal').exists()).toBe(true)
    expect(wrapper.findAll('.krc-item').length).toBe(9) // agent items, without the history button

    // Switch to shell pane tab
    const tabs = wrapper.findAll('.krc-tab')
    await tabs[1].trigger('click')
    expect(wrapper.findAll('.krc-item').length).toBe(15) // 15 shell items
  })

  it('can toggle button visibility via checkbox', async () => {
    const wrapper = mount(KeyRowConfigModal, { props: { type: 'agent' } })
    // Find Tab checkbox
    const tabCheckbox = wrapper.find('input[aria-label="Show Tab"]')
    expect(tabCheckbox.exists()).toBe(true)
    expect((tabCheckbox.element as HTMLInputElement).checked).toBe(true)

    await tabCheckbox.setValue(false)
    const stored = JSON.parse(safeStorage.getItem('herdrem_key_row_agent') || '[]')
    expect(stored.find((s: { id: string }) => s.id === 'Tab')?.visible).toBe(false)
  })

  it('can reorder buttons up and down', async () => {
    const wrapper = mount(KeyRowConfigModal, { props: { type: 'agent' } })
    const downButtons = wrapper.findAll('button[title="Move down"]')
    expect(downButtons.length).toBeGreaterThan(0)

    await downButtons[0].trigger('click')
    const stored = JSON.parse(safeStorage.getItem('herdrem_key_row_agent') || '[]')
    expect(stored[0].id).toBe('agentCmds')
    expect(stored[1].id).toBe('skills')
  })

  it('emits close on close button and Escape key', async () => {
    const wrapper = mount(KeyRowConfigModal, { props: { type: 'agent' } })
    await wrapper.find('.krc-close-btn').trigger('click')
    expect(wrapper.emitted('close')).toBeTruthy()

    window.dispatchEvent(new KeyboardEvent('keydown', { key: 'Escape' }))
    expect(wrapper.emitted('close')?.length).toBe(2)
  })

  it('resets defaults when reset button is clicked', async () => {
    const wrapper = mount(KeyRowConfigModal, { props: { type: 'agent' } })
    const downButtons = wrapper.findAll('button[title="Move down"]')
    await downButtons[0].trigger('click')
    expect(safeStorage.getItem('herdrem_key_row_agent')).not.toBeNull()

    await wrapper.find('.krc-reset-btn').trigger('click')
    expect(safeStorage.getItem('herdrem_key_row_agent')).toBeNull()
  })

  it('registers in overlay and top layer stack, closing via closeTopRegisteredLayer', async () => {
    expect(overlayNow()).toBe(false)
    const wrapper = mount(KeyRowConfigModal, { props: { type: 'agent' } })
    expect(overlayNow()).toBe(true)

    // Modal root has tabindex="-1"
    expect(wrapper.find('.krc-modal').attributes('tabindex')).toBe('-1')

    // closeTopRegisteredLayer intercepts and closes modal
    const closed = closeTopRegisteredLayer()
    expect(closed).toBe(true)
    expect(wrapper.emitted('close')?.length).toBe(1)

    wrapper.unmount()
    expect(overlayNow()).toBe(false)
  })
})
