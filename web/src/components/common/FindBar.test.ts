import { describe, it, expect } from 'vitest'
import { mount } from '@vue/test-utils'
import FindBar from './FindBar.vue'

const base = { modelValue: '', current: 0, total: 0 }

describe('FindBar', () => {
  it('names itself after what is searched', () => {
    const w = mount(FindBar, { props: { ...base, label: 'file' } })
    expect(w.get('[role="search"]').attributes('aria-label')).toBe('Search file')
    expect(w.get('input').attributes('aria-label')).toBe('Search file')
    expect(mount(FindBar, { props: base }).get('input').attributes('aria-label')).toBe('Search output')
  })

  it('shows nothing, "no match", or index/total (with + when capped)', async () => {
    const w = mount(FindBar, { props: base })
    expect(w.get('.count').text()).toBe('')
    await w.setProps({ modelValue: 'x' })
    expect(w.get('.count').text()).toBe('no match')
    await w.setProps({ total: 12, current: 3 })
    expect(w.get('.count').text()).toBe('3/12')
    await w.setProps({ capped: true })
    expect(w.get('.count').text()).toBe('3/12+')
  })

  it('disables navigation without hits and emits next / prev / close', async () => {
    const w = mount(FindBar, { props: { ...base, modelValue: 'x' } })
    expect(w.get('[aria-label="Next match"]').attributes('disabled')).toBeDefined()
    await w.setProps({ total: 2, current: 1 })
    await w.get('[aria-label="Next match"]').trigger('click')
    await w.get('[aria-label="Previous match"]').trigger('click')
    await w.get('[aria-label="Close search"]').trigger('click')
    expect(w.emitted('next')).toHaveLength(1)
    expect(w.emitted('prev')).toHaveLength(1)
    expect(w.emitted('close')).toHaveLength(1)
  })

  it('Enter / Shift+Enter / Escape on the input', async () => {
    const w = mount(FindBar, { props: { ...base, modelValue: 'x', total: 2, current: 1 } })
    const input = w.get('input')
    await input.trigger('keydown', { key: 'Enter' })
    await input.trigger('keydown', { key: 'Enter', shiftKey: true })
    await input.trigger('keydown', { key: 'Escape' })
    expect(w.emitted('next')).toHaveLength(1)
    expect(w.emitted('prev')).toHaveLength(1)
    expect(w.emitted('close')).toHaveLength(1)
  })

  it('shows a caveat badge only when a note is given', async () => {
    const w = mount(FindBar, { props: base })
    expect(w.find('.note').exists()).toBe(false)
    await w.setProps({ note: 'only part of the file is searched' })
    expect(w.get('.note').attributes('title')).toBe('only part of the file is searched')
  })

  it('v-model updates as you type', async () => {
    const w = mount(FindBar, { props: base })
    await w.get('input').setValue('hello')
    expect(w.emitted('update:modelValue')![0]).toEqual(['hello'])
  })
})
