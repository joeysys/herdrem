// Chrome skips, on system back, entries that a pushState left behind without
// user activation. The trap must keep enough gesture-made entries below the
// current one for every back the UI still needs, or back exits the PWA early.

import { afterEach, beforeEach, describe, expect, it } from 'vitest'
import { useHistoryTrap } from './useHistoryTrap'

const setStandalone = (v: boolean) =>
  (window as unknown as { __setStandalone: (v: boolean) => void }).__setStandalone(v)

let active = false
beforeEach(() => {
  setStandalone(true)
  active = false
  Object.defineProperty(navigator, 'userActivation', {
    configurable: true,
    get: () => ({ isActive: active }),
  })
})
afterEach(() => {
  setStandalone(false)
  delete (navigator as unknown as { userActivation?: unknown }).userActivation
})

async function back() {
  history.back()
  await new Promise((r) => setTimeout(r, 20))
}

describe('useHistoryTrap credit', () => {
  it('tops up gesture-made entries to the back depth, and only during activation', async () => {
    const trap = useHistoryTrap()
    let depth = 2
    const start = history.length

    // armed at mount, no activation: its entry does not count
    trap.arm(() => true, () => depth)
    expect(history.length).toBe(start + 1)

    // no activation → no top-up (those pushes would be skipped anyway)
    trap.topUp()
    expect(history.length).toBe(start + 1)

    // during a tap: depth 2 + 1 for the exit toast
    active = true
    trap.topUp()
    expect(history.length).toBe(start + 4)
    trap.topUp()
    expect(history.length).toBe(start + 4)

    // a system back (no activation) consumes one entry; the re-push doesn't count
    active = false
    await back()
    depth = 1
    active = true
    trap.topUp()
    // credit 3 → 2 after the back; depth 1 needs 2 → no extra push
    expect(history.length).toBe(start + 4)

    // two more backs without taps drain the credit (each pops one and
    // re-pushes one, so length holds); the next tap refills it with 2
    active = false
    await back()
    await back()
    expect(history.length).toBe(start + 4)
    active = true
    trap.topUp()
    expect(history.length).toBe(start + 6)

    trap.disarm()
  })
})
