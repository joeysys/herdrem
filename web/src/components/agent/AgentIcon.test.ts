import { describe, it, expect } from 'vitest'
import { mount } from '@vue/test-utils'
import AgentIcon from './AgentIcon.vue'
import type { Pane } from '../../api/types'

function pane(partial: Partial<Pane>): Pane {
  return { pane_id: 'x:0.1', ...partial }
}

describe('AgentIcon', () => {
  it('detects claude', () => {
    const w = mount(AgentIcon, { props: { pane: pane({ agent: 'claude', kind: 'agent' }) } })
    expect(w.find('img').attributes('src')).toContain('claude.svg')
  })
  it('detects cursor', () => {
    const w = mount(AgentIcon, { props: { pane: pane({ agent: 'cursor-agent', kind: 'agent' }) } })
    expect(w.find('img').attributes('src')).toContain('cursor.svg')
  })
  it('uses shell icon for shell panes', () => {
    const w = mount(AgentIcon, { props: { pane: pane({ kind: 'shell' }) } })
    expect(w.find('img').attributes('src')).toContain('shell.svg')
  })
  it('prefers display_agent over agent', () => {
    const w = mount(AgentIcon, { props: { pane: pane({ agent: 'unknown', display_agent: 'grok', kind: 'agent' }) } })
    expect(w.find('img').attributes('src')).toContain('grok.svg')
  })
  it('falls back to initials when the svg fails to load', async () => {
    const w = mount(AgentIcon, { props: { pane: pane({ agent: 'mystery', kind: 'agent' }) } })
    await w.find('img').trigger('error')
    expect(w.find('.fallback').exists()).toBe(true)
    expect(w.find('.fallback').text()).toBe('MY')
  })
})
