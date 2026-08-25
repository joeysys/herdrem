import { describe, it, expect, vi, beforeEach } from 'vitest'
import { mount } from '@vue/test-utils'
import { createPinia, setActivePinia } from 'pinia'
import TermView from './TermView.vue'
import { useAgentsStore } from '../../stores/connection'
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

describe('TermView', () => {
  it('shows a polite empty/loading state and labelled terminal region', () => {
    const agents = useAgentsStore()
    agents.setAgents([{ pane_id: 'p1', project: 'demo', cwd: '/tmp', host: 'local', status: 'idle', kind: 'agent' }])
    const term = useTerminalStore()
    term.loading = true
    term.content = ''

    const wrapper = mount(TermView, { props: { paneId: 'p1' } })
    const region = wrapper.get('[aria-label="Terminal"]')
    expect(region.attributes('aria-busy')).toBe('true')
    expect(wrapper.get('.term-loading').attributes('aria-live')).toBe('polite')
    expect(wrapper.find('[aria-label="Back to agent list"]').exists()).toBe(true)
  })

  it('emits back from the labelled back control', async () => {
    const agents = useAgentsStore()
    agents.setAgents([{ pane_id: 'p1', project: 'demo', cwd: '/tmp', host: 'local', status: 'idle', kind: 'agent' }])
    const wrapper = mount(TermView, { props: { paneId: 'p1' } })
    await wrapper.get('[aria-label="Back to agent list"]').trigger('click')
    expect(wrapper.emitted('back')).toHaveLength(1)
  })
})
