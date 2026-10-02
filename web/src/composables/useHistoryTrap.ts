// useHistoryTrap.ts — intercept the PWA/standalone back button so it closes
// the topmost UI layer instead of exiting the app.
//
// Only active in standalone (installed PWA) mode. When armed, a history entry
// is pushed; on popstate the handler is asked to close its topmost layer. If it
// handled the back, a new sentinel is pushed; otherwise the trap disarms and
// the next back exits normally.
//
// Chrome (and installed PWAs on Android) skips, on system back, any history
// entry that a pushState left behind without user activation (the "history
// manipulation intervention"). The re-push after a handled back runs without
// activation, so every back that isn't preceded by a tap burns one entry that
// was pushed during a gesture. Once none are left, back leaves the app, e.g.
// from inside a space. To keep ahead of that, the trap counts the entries it
// knows back can still land on (credit). While the user is interacting it
// tops that count up to the UI's back depth.

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
  // entries below the current one that system back can still land on
  let credit = 0

  /** pushes a sentinel. With user activation, the entry it leaves behind
   *  stays reachable by back; without activation, Chrome skips that entry. */
  function pushSentinel() {
    window.history.pushState({ herdremTrap: true }, '')
    if (hasActivation()) credit++
  }

  function onPop() {
    if (!armed || !handler) return
    credit = Math.max(0, credit - 1)
    if (handler()) {
      // closed a layer — stay armed with a fresh sentinel
      pushSentinel()
    } else {
      disarm()
    }
  }

  /** while the user is interacting, push enough sentinels for every back
   *  the UI still needs, plus one for the exit toast. */
  function topUp() {
    if (!armed || !hasActivation()) return
    const want = depthOf() + 1
    for (let i = credit; i < want; i++) pushSentinel()
  }

  /** start intercepting back; cb returns true if it closed a layer, depth
   *  counts the backs the UI needs to reach its root (over-counting only
   *  costs spare history entries). */
  function arm(cb: () => boolean, depth?: () => number) {
    handler = cb
    if (depth) depthOf = depth
    if (armed || !isStandalone()) return
    armed = true
    window.addEventListener('popstate', onPop)
    window.addEventListener('pointerdown', topUp, true)
    pushSentinel()
  }

  function disarm() {
    if (!armed) return
    armed = false
    handler = null
    credit = 0
    window.removeEventListener('popstate', onPop)
    window.removeEventListener('pointerdown', topUp, true)
  }

  function isActive() {
    return armed
  }

  return { arm, disarm, isActive, topUp }
}
