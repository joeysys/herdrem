// autoHideHeader.ts — state machine for the mobile auto-hide topbar.
// Hides chrome on sustained downward scroll, reveals on upward scroll.
//
// Why a state machine instead of a bare `st > lastScrollTop + 4` delta check:
// scrollTop moves without user input — scroll anchoring after ws-driven DOM
// churn (cards re-sort by last_active, quick-approve rows appear/disappear),
// clamping while the expand transition shrinks the scroller near the bottom,
// rubber-band settle. The old check read those deltas as intent and
// flip-flopped the header (mobile list jitter). Rules that break the loop:
//  - dirty window: DOM mutations inside the scroller precede anchoring
//    adjustments by ~15-30ms — scroll events in that window only resync the
//    baseline, they never count as direction.
//  - direction accumulator: toggling needs sustained same-direction travel;
//    alternating churn resets the accumulator on every reversal.
//  - cooldown: never flip faster than the CSS transition (250ms).
//  - lastSt resyncs on every event so the baseline can't go stale.
//  - room after hiding: the hidden chrome's height goes to the scroller, so
//    a list with little to spare would, once hidden, be "barely scrollable"
//    (or clamp to the top) and show again at once — hide, show, hide while
//    scrolling to the bottom. Only hide when the room left still counts.

export interface AutoHideScroll {
  hidden: boolean
  lastSt: number
  dir: 1 | -1 | 0
  acc: number
  lastToggleAt: number
}

export const AUTO_HIDE = {
  hideAcc: 48, // px of net downward travel required to hide
  showAcc: 24, // px of net upward travel required to show
  hideBelow: 80, // never hide within this distance of the top
  showAbove: 48, // always show when this close to the top
  minRoom: 120, // never hide when there is barely any scroll room
  dirtyWindow: 100, // ms after a DOM mutation to distrust scroll deltas
  cooldown: 350, // ms between toggles (> the 250ms CSS transition)
} as const

export function createAutoHideScroll(): AutoHideScroll {
  return { hidden: false, lastSt: 0, dir: 0, acc: 0, lastToggleAt: -Infinity }
}

export function autoHideStep(
  s: AutoHideScroll,
  st: number,
  maxScroll: number,
  now: number,
  dirty: boolean,
  /** maxScroll once the header is hidden (defaults to maxScroll) */
  roomHidden: number = maxScroll,
): void {
  const d = st - s.lastSt
  s.lastSt = st
  // Anchoring/clamp churn correlated with DOM mutations is not user scroll.
  if (dirty) return
  // Barely scrollable or pinned to the top: keep the header visible.
  if (maxScroll < AUTO_HIDE.minRoom || st <= 2) {
    s.dir = 0
    s.acc = 0
    if (s.hidden) {
      s.hidden = false
      s.lastToggleAt = now
    }
    return
  }
  // At the bottom edge don't toggle — clamping there produces fake deltas.
  if (st >= maxScroll - 2) {
    s.dir = 0
    s.acc = 0
    return
  }
  if (d === 0) return
  const dir: 1 | -1 = d > 0 ? 1 : -1
  if (dir !== s.dir) {
    s.dir = dir
    s.acc = 0
  }
  s.acc += d
  if (now - s.lastToggleAt < AUTO_HIDE.cooldown) return
  if (!s.hidden && s.acc >= AUTO_HIDE.hideAcc && st > AUTO_HIDE.hideBelow && roomHidden >= AUTO_HIDE.minRoom) {
    s.hidden = true
    s.lastToggleAt = now
    s.dir = 0
    s.acc = 0
  } else if (s.hidden && (s.acc <= -AUTO_HIDE.showAcc || st <= AUTO_HIDE.showAbove)) {
    s.hidden = false
    s.lastToggleAt = now
    s.dir = 0
    s.acc = 0
  }
}
