import { afterEach, beforeEach, describe, expect, it } from 'vitest'
import { autoFocus } from './softKeyboard'

let coarse = false
const realMatchMedia = window.matchMedia

beforeEach(() => {
  coarse = false
  window.matchMedia = ((q: string) => ({ ...realMatchMedia(q), matches: q === '(pointer: coarse)' ? coarse : realMatchMedia(q).matches })) as typeof window.matchMedia
  document.body.innerHTML = '<input id="a" /><input id="b" /><button id="btn">x</button>'
  ;(document.activeElement as HTMLElement | null)?.blur()
})
afterEach(() => {
  window.matchMedia = realMatchMedia
  document.body.innerHTML = ''
})

const el = (id: string) => document.getElementById(id) as HTMLInputElement

describe('autoFocus', () => {
  it('focuses on desktop', () => {
    expect(autoFocus(el('a'))).toBe(true)
    expect(document.activeElement).toBe(el('a'))
  })

  it('on touch, does not raise the keyboard from nothing', () => {
    coarse = true
    expect(autoFocus(el('a'))).toBe(false)
    expect(document.activeElement).toBe(document.body)
  })

  it('on touch, does not raise the keyboard from a button', () => {
    coarse = true
    el('btn').focus()
    expect(autoFocus(el('a'))).toBe(false)
    expect(document.activeElement).toBe(el('btn'))
  })

  it('on touch, moves focus between inputs while the keyboard is already up', () => {
    coarse = true
    el('b').focus()
    expect(autoFocus(el('a'))).toBe(true)
    expect(document.activeElement).toBe(el('a'))
  })

  it('ignores a missing element', () => {
    expect(autoFocus(null)).toBe(false)
  })
})
