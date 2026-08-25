import { describe, it, expect, vi, beforeEach } from 'vitest'
import { mount } from '@vue/test-utils'
import { createPinia, setActivePinia } from 'pinia'
import QuickChips from './QuickChips.vue'
import { usePrefsStore } from '../../stores/prefs'

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

describe('QuickChips', () => {
  it('disables skills when the list is empty', () => {
    const wrapper = mount(QuickChips, { props: { agentName: 'codex', mode: 'agent' } })
    expect(wrapper.get('[aria-label="Skills"]').attributes('disabled')).toBeDefined()
  })

  it('emits fill when a skill chip is chosen', async () => {
    const prefs = usePrefsStore()
    prefs.skills = [{ label: 'review', template: '/review' }]
    const wrapper = mount(QuickChips, { props: { agentName: 'codex', mode: 'agent' } })
    await wrapper.get('[aria-label="Skills"]').trigger('click')
    await wrapper.get('.qc-item').trigger('click')
    expect(wrapper.emitted('fill')?.[0]).toEqual(['/review'])
  })
})
