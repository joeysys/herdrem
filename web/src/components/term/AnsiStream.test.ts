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

describe('AnsiStream search highlight', () => {
  it('wraps matches across colour spans and reports the hit count', async () => {
    const w = mount(AnsiStream, { props: { raw: 'run \x1b[31mfa\x1b[0mil then fail again' } })
    await w.setProps({ highlight: 'fail' })
    expect(w.findAll('mark.term-hit').length).toBe(3) // 2 pieces + 1 piece
    expect(w.emitted('hits')!.at(-1)).toEqual([2])
    expect(w.text()).toBe('run fail then fail again')
  })

  it('moves the active emphasis with setActiveHit', async () => {
    const w = mount(AnsiStream, { props: { raw: 'x one x two x', highlight: 'x' } })
    await w.vm.$nextTick()
    const vm = w.vm as unknown as { setActiveHit: (i: number, scroll?: boolean) => void }
    vm.setActiveHit(2, false)
    const marks = w.findAll('mark.term-hit')
    expect(marks.map((m) => m.classes().includes('active'))).toEqual([false, false, true])
  })

  it('re-applies highlights when the output changes and clears them with the query', async () => {
    const w = mount(AnsiStream, { props: { raw: 'one err', highlight: 'err' } })
    await w.vm.$nextTick()
    expect(w.findAll('mark.term-hit')).toHaveLength(1)
    await w.setProps({ raw: 'one err two err' })
    expect(w.findAll('mark.term-hit')).toHaveLength(2)
    expect(w.emitted('hits')!.at(-1)).toEqual([2])
    await w.setProps({ highlight: '' })
    expect(w.findAll('mark.term-hit')).toHaveLength(0)
    expect(w.emitted('hits')!.at(-1)).toEqual([0])
  })

  it('does not emit hits when no query was ever set', async () => {
    const w = mount(AnsiStream, { props: { raw: 'plain' } })
    await w.setProps({ raw: 'plain more' })
    expect(w.emitted('hits')).toBeUndefined()
  })
})
