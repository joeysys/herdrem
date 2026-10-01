import { describe, it, expect, vi, beforeEach } from 'vitest'
import { mount } from '@vue/test-utils'
import { createPinia, setActivePinia } from 'pinia'
import HistorySection from './HistorySection.vue'
import { useCmdHistoryStore } from '../../stores/cmdHistory'
import { useConnectionStore } from '../../stores/connection'
import { safeStorage } from '../../utils/storage'

vi.mock('../../api/ws', () => ({
  ws: { send: vi.fn(() => true), connect: vi.fn(), disconnect: vi.fn(), onMessage: vi.fn(() => () => {}), onStatus: vi.fn(() => () => {}), status: 'connected' },
}))

beforeEach(() => {
  safeStorage.removeItem('herdrem_cmd_history')
  safeStorage.removeItem('herdrem_history_agents')
  safeStorage.removeItem('herdrem_history_pending')
  safeStorage.removeItem('herdrem_history_seeded')
  setActivePinia(createPinia())
})

describe('HistorySection', () => {
  it('shows how much is saved for this relay', () => {
    const h = useCmdHistoryStore()
    h.record('ls', { kind: 'sh' })
    h.record('fix it', { kind: 'ag' })
    h.record('fix more', { kind: 'ag' })
    const w = mount(HistorySection)
    expect(w.get('[role="status"]').text()).toContain('1 commands · 2 prompts')
  })

  it('toggles recording of agent prompts', async () => {
    const h = useCmdHistoryStore()
    const w = mount(HistorySection)
    const box = w.get('input[type="checkbox"]')
    expect((box.element as HTMLInputElement).checked).toBe(true)
    await box.setValue(false)
    expect(h.recordAgents).toBe(false)
    h.record('private prompt', { kind: 'ag' })
    expect(h.entries).toEqual([])
  })

  it('clears this relay only after a second tap', async () => {
    const h = useCmdHistoryStore()
    h.record('ls', { kind: 'sh' })
    h.record('fix it', { kind: 'ag' })
    const w = mount(HistorySection)
    const btn = w.get('.hs-clear')
    await btn.trigger('click')
    expect(h.entries).toHaveLength(2)
    expect(btn.text()).toBe('tap again to clear')
    await btn.trigger('click')
    expect(h.entries).toEqual([])
  })

  it('disables clear when there is nothing to clear', () => {
    expect(mount(HistorySection).get('.hs-clear').attributes('disabled')).toBeDefined()
  })

  it('says the history is stored in this browser only on a relay that cannot keep it', () => {
    const w = mount(HistorySection)
    expect(w.text()).toContain('in this browser only')
    expect(w.text()).toContain('too old to keep history')
    expect(w.get('.hs-clear').text()).toBe('clear history for this relay')
  })

  it('says it is stored on the relay, shared by all devices, when the relay keeps it', () => {
    useConnectionStore().setHello('test', ['input-history'])
    const h = useCmdHistoryStore()
    h.record('ls', { kind: 'sh' })
    const w = mount(HistorySection)
    expect(w.text()).toContain('on the relay, shared by all devices')
    expect(w.text()).toContain('Clearing it clears it for all of them')
    expect(w.get('.hs-clear').text()).toBe('clear history on the relay')
  })
})
