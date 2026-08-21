import { describe, it, expect } from 'vitest'
import { mount } from '@vue/test-utils'
import AnsiStream from './AnsiStream.vue'

describe('AnsiStream', () => {
  it('renders ansi color as styled span', () => {
    const w = mount(AnsiStream, { props: { raw: '\x1b[31mred\x1b[0m plain' } })
    expect(w.text()).toContain('red')
    expect(w.text()).toContain('plain')
    expect(w.html()).toContain('color:')
  })

  it('renders a file link and emits openFile with path+line', async () => {
    const w = mount(AnsiStream, { props: { raw: 'error in src/app.ts:42 now' } })
    const link = w.find('a.term-file-link')
    expect(link.exists()).toBe(true)
    await link.trigger('click')
    const ev = w.emitted('openFile')
    expect(ev).toBeTruthy()
    expect(ev![0][0]).toEqual({ path: 'src/app.ts', line: 42 })
  })

  it('emits null line for paths without a line number', async () => {
    const w = mount(AnsiStream, { props: { raw: 'open README.md' } })
    const link = w.find('a.term-file-link')
    expect(link.exists()).toBe(true)
    await link.trigger('click')
    expect(w.emitted('openFile')![0][0]).toEqual({ path: 'README.md', line: null })
  })

  it('links the first ANSI segment of a path wrapped before its extension', async () => {
    const w = mount(AnsiStream, {
      props: {
        raw: '\x1b[33msrc/store/menus.store\x1b[0m\n\x1b[33m.ts\x1b[0m',
        validPaths: ['src/store/menus.store.ts'],
      },
    })
    const link = w.find('a.term-file-link')
    expect(link.exists()).toBe(true)
    await link.trigger('click')
    expect(w.emitted('openFile')![0][0]).toEqual({ path: 'src/store/menus.store.ts', line: null })
  })
})
