// useHistoryTrap.ts — intercept the PWA/standalone back button so it closes
// the topmost UI layer instead of exiting the app.
//
// Only active in standalone (installed PWA) mode. While armed, popstate asks
// the handler to close its topmost layer; when it reports nothing left to
// close, the trap disarms and the next back exits normally.
//
// Chrome's history manipulation intervention decides what system back skips
// (docs/history_manipulation_intervention.md in Chromium):
//   - all same-document entries share one skippable state, so one pushState
//     made without user activation makes EVERY entry of this page skippable,
//     and the next system back leaves the app;
//   - after a back/forward, earlier activation no longer counts for new
//     entries — only a fresh one does.
// So the trap never pushes inside popstate (that push would poison the whole
// page: back once, then back again exits). Instead, whenever the user has
// just tapped or typed, it pushes ahead enough entries for every back the UI
// still needs plus one for the exit toast; each handled back spends one.

/** hasActivation reports transient user activation; assumed true where the
 *  API is missing (those browsers don't skip entries anyway). */
function hasActivation(): boolean {
  const ua = (navigator as unknown as { userActivation?: { isActive: boolean } }).userActivation
  return ua ? ua.isActive : true
}

export function isStandalone(): boolean {
  if (typeof window === 'undefined') return false
  return (
    window.matchMedia('(display-mode: standalone)').matches ||
    (navigator as unknown as { standalone?: boolean }).standalone === true
  )
}

/** lockPortrait tries to lock orientation to portrait (needs a user gesture). */
export function tryLockPortrait() {
  try {
    const so = screen.orientation as unknown as { lock?: (o: string) => Promise<void> }
    if (so && typeof so.lock === 'function') {
      so.lock('portrait').catch(() => {})
    }
  } catch {
    /* not supported */
  }
}

export function useHistoryTrap() {
  let armed = false
  let handler: (() => boolean) | null = null
  let depthOf: () => number = () => 0
  // entries the trap pushed below the current one, i.e. backs it can absorb
  let credit = 0
  // false from a back until the next tap / keypress: pushes made in between
  // would be taken as made without activation
  let fresh = true
  let onRefill: (() => void) | null = null

  function onPop() {
    if (!armed || !handler) return
    credit = Math.max(0, credit - 1)
    fresh = false
    // no re-push here — see the header
    if (!handler()) disarm()
  }

  /** push entries up to the UI's back depth plus one for the exit toast, but
   *  only while a fresh user activation makes them count. */
  function topUp() {
    if (!armed || !fresh || !hasActivation()) return
    const was = credit
    const want = depthOf() + 1
    while (credit < want) {
      window.history.pushState({ herdremTrap: true }, '')
      credit++
    }
    if (was === 0 && credit > 0) onRefill?.()
  }

  /** whether a back would still land on an entry the trap pushed; with none
   *  left, the next system back leaves the app without a popstate. */
  function hasCredit() {
    return armed && credit > 0
  }

  // A tap ends on pointerup (touch only activates there); a keypress other
  // than Escape activates too. Escape, like a back, does not.
  function onInput(e: Event) {
    if (e instanceof KeyboardEvent && e.key === 'Escape') return
    fresh = true
    topUp()
  }

  /** start intercepting back; cb returns true if it closed a layer, depth
   *  counts the backs the UI needs to reach its root (over-counting only
   *  costs spare history entries), refill runs when a tap pushes entries
   *  again after the last one was spent. */
  function arm(cb: () => boolean, depth?: () => number, refill?: () => void) {
    handler = cb
    if (depth) depthOf = depth
    if (refill) onRefill = refill
    if (armed || !isStandalone()) return
    armed = true
    fresh = true
    window.addEventListener('popstate', onPop)
    window.addEventListener('pointerup', onInput, true)
    window.addEventListener('keydown', onInput, true)
    topUp() // a no-op at load (no activation yet); the first tap fills it
  }

  function disarm() {
    if (!armed) return
    armed = false
    handler = null
    onRefill = null
    credit = 0
    window.removeEventListener('popstate', onPop)
    window.removeEventListener('pointerup', onInput, true)
    window.removeEventListener('keydown', onInput, true)
  }

  function isActive() {
    return armed
  }

  return { arm, disarm, isActive, topUp, hasCredit }
}
