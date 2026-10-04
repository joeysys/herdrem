// Chrome treats all of a page's history entries as one: a single pushState
// made without fresh user activation makes system back skip them all and
// leave the app. The trap must therefore push only right after a tap or
// keypress — never inside popstate — and push enough ahead for the backs the
// UI still needs.

import { afterEach, beforeEach, describe, expect, it } from 'vitest'
import { useHistoryTrap } from './useHistoryTrap'
import { simulateChromeHistory, type ChromeHistory } from '../test-chromeHistory'

const setStandalone = (v: boolean) =>
  (window as unknown as { __setStandalone: (v: boolean) => void }).__setStandalone(v)
const flush = () => new Promise<void>((r) => setTimeout(r, 20))

let chrome: ChromeHistory
let trap: ReturnType<typeof useHistoryTrap>
beforeEach(() => {
  setStandalone(true)
  chrome = simulateChromeHistory(flush)
  trap = useHistoryTrap()
})
afterEach(() => {
  trap.disarm()
  chrome.restore()
  setStandalone(false)
})

describe('useHistoryTrap', () => {
  it('pushes nothing until a tap, then enough for the depth plus the exit toast', () => {
    const start = history.length
    trap.arm(() => true, () => 2)
    expect(history.length).toBe(start) // no activation at load
    chrome.tap(document.body)
    expect(chrome.pushes).toBe(3)
    chrome.tap(document.body) // already enough
    expect(chrome.pushes).toBe(3)
  })

  it('handled backs push nothing, so back after back keeps working', async () => {
    let depth = 2
    const closed: number[] = []
    trap.arm(() => {
      if (depth === 0) return true // the exit toast
      closed.push(depth--)
      return true
    }, () => depth)
    chrome.tap(document.body)
    const pushed = chrome.pushes

    await chrome.systemBack()
    await chrome.systemBack()
    await chrome.systemBack() // toast
    expect(chrome.exited).toBe(false)
    expect(closed).toEqual([2, 1])
    expect(chrome.pushes).toBe(pushed) // nothing pushed while popping
    await chrome.systemBack()
    expect(chrome.exited).toBe(true)
  })

  it('a depth change right after a back pushes nothing; the next tap does', async () => {
    let depth = 1
    trap.arm(() => true, () => depth)
    chrome.tap(document.body)
    await chrome.systemBack()
    const pushed = chrome.pushes
    // a layer opening with no tap since the back (the old activation is still
    // transient-active, but Chrome no longer honours it)
    depth = 3
    trap.topUp()
    expect(chrome.pushes).toBe(pushed)
    chrome.tap(document.body)
    expect(chrome.pushes).toBe(pushed + 3) // credit 1 → depth 3 + toast
    expect(chrome.exited).toBe(false)
  })

  it('Escape is not an activation', async () => {
    trap.arm(() => true, () => 1)
    chrome.tap(document.body)
    await chrome.systemBack()
    const pushed = chrome.pushes
    window.dispatchEvent(new KeyboardEvent('keydown', { key: 'Escape' }))
    expect(chrome.pushes).toBe(pushed)
    window.dispatchEvent(new KeyboardEvent('keydown', { key: 'a' }))
    expect(chrome.pushes).toBe(pushed + 1)
  })
})
