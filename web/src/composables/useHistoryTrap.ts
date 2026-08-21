// useHistoryTrap.ts — intercept the PWA/standalone back button so it closes
// the topmost UI layer instead of exiting the app.
//
// Only active in standalone (installed PWA) mode. When armed, a history entry
// is pushed; on popstate the handler is asked to close its topmost layer. If it
// handled the back, a new sentinel is pushed; otherwise the trap disarms and
// the next back exits normally.

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

  function onPop() {
    if (!armed || !handler) return
    if (handler()) {
      // closed a layer — stay armed with a fresh sentinel
      window.history.pushState({ herdremTrap: true }, '')
    } else {
      disarm()
    }
  }

  /** start intercepting back; cb returns true if it closed a layer. */
  function arm(cb: () => boolean) {
    handler = cb
    if (armed || !isStandalone()) return
    armed = true
    window.addEventListener('popstate', onPop)
    window.history.pushState({ herdremTrap: true }, '')
  }

  function disarm() {
    if (!armed) return
    armed = false
    handler = null
    window.removeEventListener('popstate', onPop)
  }

  function isActive() {
    return armed
  }

  return { arm, disarm, isActive }
}
