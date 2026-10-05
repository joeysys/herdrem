import { describe, it, expect } from 'vitest'
import { createAutoHideScroll, autoHideStep, AUTO_HIDE, type AutoHideScroll } from './autoHideHeader'

const MAX = 5000
let now = 0

/** feed scroll deltas; returns toggle count */
function run(s: AutoHideScroll, deltas: number[], opts: { dirty?: boolean; stepMs?: number } = {}): number {
  let st = s.lastSt
  let toggles = 0
  for (const d of deltas) {
    now += opts.stepMs ?? 16
    st += d
    const was = s.hidden
    autoHideStep(s, st, MAX, now, opts.dirty ?? false)
    if (s.hidden !== was) toggles++
  }
  return toggles
}

describe('autoHideStep', () => {
  it('hides on sustained downward scroll and shows on upward scroll', () => {
    const s = createAutoHideScroll()
    s.lastSt = 1000
    run(s, Array(10).fill(20)) // +200px down
    expect(s.hidden).toBe(true)
    now += 400 // let the cooldown lapse
    run(s, Array(6).fill(-10)) // -60px up
    expect(s.hidden).toBe(false)
  })

  it('does not flip-flop on alternating DOM-churn deltas (scroll anchoring)', () => {
    const s = createAutoHideScroll()
    s.lastSt = 1400
    run(s, Array(20).fill(30)) // hide first
    expect(s.hidden).toBe(true)
    now += 400
    run(s, [-600]) // user scrolls up -> shows
    expect(s.hidden).toBe(false)
    // ws updates: alternating ±45px anchoring bumps, mutation-correlated (dirty)
    const t = run(
      s,
      Array(16)
        .fill(0)
        .map((_, i) => (i % 2 ? -45 : 45)),
      { dirty: true },
    )
    expect(t).toBe(0)
    expect(s.hidden).toBe(false)
  })

  it('absorbs alternating churn even when the dirty window misses it', () => {
    const s = createAutoHideScroll()
    s.lastSt = 890
    // shown state: alternating ±45 never reaches the 48px hide threshold
    const t = run(
      s,
      Array(16)
        .fill(0)
        .map((_, i) => (i % 2 ? -45 : 45)),
    )
    expect(s.hidden).toBe(false)
    expect(t).toBe(0)
  })

  it('has no dead zone: upward scroll reveals within showAcc of travel', () => {
    const s = createAutoHideScroll()
    s.lastSt = 900
    run(s, [500]) // jump down -> hide
    now += 400
    expect(s.hidden).toBe(true)
    // upward scroll: old impl kept lastScrollTop stale at hide-time value and
    // required scrolling back past it; now ~24px of upward travel is enough
    run(s, [-8, -8, -8, -8])
    expect(s.hidden).toBe(false)
  })

  it('respects the toggle cooldown — cannot flip faster than the transition', () => {
    const s = createAutoHideScroll()
    s.lastSt = 1000
    run(s, Array(5).fill(15)) // hide
    expect(s.hidden).toBe(true)
    const t = run(s, Array(10).fill(-30), { stepMs: 20 }) // rapid up-scroll, still in cooldown
    expect(t).toBe(0)
    expect(s.hidden).toBe(true)
    now += 400 // cooldown lapses
    run(s, [-30])
    expect(s.hidden).toBe(false)
  })

  it('does not toggle at the bottom edge', () => {
    const s = createAutoHideScroll()
    s.lastSt = 1000
    run(s, [4000]) // jump to bottom
    expect(s.hidden).toBe(false)
    run(s, [-1, 1, -1, 1]) // clamp tremor at bottom
    expect(s.hidden).toBe(false)
  })

  // Bug: hiding hands the chrome's height to the scroller. With 185px of room
  // and 258px of chrome (as measured on a phone with 8 cards), the room left
  // was below minRoom, so the header showed again at once — and hid on the
  // next scroll, flickering while the user scrolled to the bottom.
  it('does not hide when the room left after hiding would show it again', () => {
    const CHROME = 258
    const layout = (shown: number, s: AutoHideScroll) => (s.hidden ? shown - CHROME : shown)
    for (const shown of [185, 353]) {
      const s = createAutoHideScroll()
      let st = 0
      let toggles = 0
      for (let i = 0; i < 40; i++) {
        now += 16
        st = Math.min(st + 20, layout(shown, s)) // drag down; the scroller clamps
        const was = s.hidden
        autoHideStep(s, st, layout(shown, s), now, false, shown - CHROME)
        if (s.hidden !== was) toggles++
      }
      expect(toggles).toBe(0)
    }
    // plenty of room once hidden: hides as before
    const s = createAutoHideScroll()
    s.lastSt = 200
    for (let i = 0; i < 10; i++) autoHideStep(s, 200 + 20 * (i + 1), 773, (now += 16), false, 773 - CHROME)
    expect(s.hidden).toBe(true)
  })

  it('always shows when barely scrollable or near the top', () => {
    const s = createAutoHideScroll()
    s.hidden = true
    s.lastSt = 200
    autoHideStep(s, 100, AUTO_HIDE.minRoom - 10, ++now, false) // maxScroll < minRoom
    expect(s.hidden).toBe(false)
    s.hidden = true
    s.lastSt = 100
    autoHideStep(s, 30, MAX, ++now + 400, false) // st <= showAbove while hidden
    expect(s.hidden).toBe(false)
  })
})
