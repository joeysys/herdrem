import { describe, it, expect, vi, beforeEach } from 'vitest'
import { mount } from '@vue/test-utils'
import { createPinia, setActivePinia } from 'pinia'
import PreferencesSection from './PreferencesSection.vue'
import { safeStorage } from '../../utils/storage'
import { resetKeyRowState } from '../../composables/useKeyRowConfig'

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
  safeStorage.removeItem('herdrem_key_row_agent')
  safeStorage.removeItem('herdrem_key_row_shell')
  resetKeyRowState('agent')
  resetKeyRowState('shell')
  setActivePinia(createPinia())
})

describe('PreferencesSection shortcut keys', () => {
  it('opens KeyRowConfigModal for agent and shell panes', async () => {
    const wrapper = mount(PreferencesSection)
    expect(wrapper.findComponent({ name: 'KeyRowConfigModal' }).exists()).toBe(false)

    // Click agent pane button
    const agentBtn = wrapper.findAll('.layout-toggle button').find((b) => b.text().includes('agent pane'))
    expect(agentBtn).toBeDefined()
    await agentBtn!.trigger('click')

    const modal = wrapper.findComponent({ name: 'KeyRowConfigModal' })
    expect(modal.exists()).toBe(true)
    expect(modal.props('type')).toBe('agent')

    // Close modal
    await modal.find('.krc-close-btn').trigger('click')
    expect(wrapper.findComponent({ name: 'KeyRowConfigModal' }).exists()).toBe(false)

    // Click shell pane button
    const shellBtn = wrapper.findAll('.layout-toggle button').find((b) => b.text().includes('shell pane'))
    expect(shellBtn).toBeDefined()
    await shellBtn!.trigger('click')

    const modalShell = wrapper.findComponent({ name: 'KeyRowConfigModal' })
    expect(modalShell.exists()).toBe(true)
    expect(modalShell.props('type')).toBe('shell')
  })
})
