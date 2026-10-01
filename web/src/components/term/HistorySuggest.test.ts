import { describe, it, expect } from 'vitest'
import { mount } from '@vue/test-utils'
import HistorySuggest from './HistorySuggest.vue'

describe('HistorySuggest', () => {
  it('renders nothing when there are no suggestions', () => {
    const w = mount(HistorySuggest, { props: { items: [], typed: 'g', active: 0 } })
    expect(w.find('[role="listbox"]').exists()).toBe(false)
  })

  it('renders an accessible listbox with the active option selected', () => {
    const w = mount(HistorySuggest, { props: { items: ['git status', 'git stash'], typed: 'git st', active: 1 } })
    expect(w.get('[role="listbox"]').attributes('aria-label')).toBe('Command suggestions')
    const opts = w.findAll('[role="option"]')
    expect(opts.map((o) => o.attributes('aria-selected'))).toEqual(['false', 'true'])
  })

  it('dims the typed prefix and emphasises the completion', () => {
    const w = mount(HistorySuggest, { props: { items: ['git status'], typed: 'git st', active: 0 } })
    expect(w.get('.typed').text()).toBe('git st')
    expect(w.get('.rest').text()).toBe('atus')
  })

  it('does not dim anything for a non-prefix (substring) match', () => {
    const w = mount(HistorySuggest, { props: { items: ['echo git st'], typed: 'git st', active: 0 } })
    expect(w.get('.typed').text()).toBe('')
    expect(w.get('.rest').text()).toBe('echo git st')
  })

  it('emits pick with the command on click', async () => {
    const w = mount(HistorySuggest, { props: { items: ['ls -la'], typed: 'l', active: 0 } })
    await w.get('[role="option"]').trigger('click')
    expect(w.emitted('pick')![0]).toEqual(['ls -la'])
  })
})
