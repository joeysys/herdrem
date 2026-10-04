// Test helper: Chrome's history manipulation intervention, on top of jsdom's
// history (which never skips anything, so it cannot show the bug).
//
// Rules modelled (Chromium docs/history_manipulation_intervention.md):
//   - every same-document entry shares one skippable state;
//   - a pushState without user activation makes the page skippable;
//   - after a back, earlier activation no longer counts for new entries;
//   - a user activation makes the page unskippable again.
// A system back on a skippable page — or with no entry below — leaves the app.

export interface ChromeHistory {
  /** a tap on el: activation, pointerup (where the trap listens), click */
  tap(el: Element): void
  /** the system back button; resolves after popstate has been handled */
  systemBack(): Promise<void>
  /** whether a system back left the app */
  readonly exited: boolean
  /** pushState calls so far (history.length hides a push that replaced a
   *  forward entry) */
  readonly pushes: number
  restore(): void
}

export function simulateChromeHistory(flush: () => Promise<void>): ChromeHistory {
  let index = 0 // jsdom does not expose the current entry's position
  let activation = false // transient activation (navigator.userActivation)
  let honored = false // activation still counts for new entries
  let skippable = false
  let exited = false
  let pushes = 0

  Object.defineProperty(navigator, 'userActivation', {
    configurable: true,
    get: () => ({ isActive: activation }),
  })
  const realPush = history.pushState.bind(history)
  const realBack = history.back.bind(history)
  history.pushState = (data: unknown, unused: string, url?: string | URL | null) => {
    if (!(activation && honored)) skippable = true
    pushes++
    index++
    realPush(data, unused, url)
  }
  // history.back() from page code is not skipped by the intervention
  history.back = () => {
    if (index > 0) index--
    realBack()
  }

  return {
    tap(el: Element) {
      activation = true
      honored = true
      skippable = false
      el.dispatchEvent(new Event('pointerup', { bubbles: true }))
      ;(el as HTMLElement).click()
    },
    async systemBack() {
      honored = false // the old (still transient) activation stops counting
      if (skippable || index === 0) {
        exited = true
        return
      }
      history.back()
      await flush()
    },
    get exited() {
      return exited
    },
    get pushes() {
      return pushes
    },
    restore() {
      history.pushState = realPush
      history.back = realBack
      delete (navigator as unknown as { userActivation?: unknown }).userActivation
    },
  }
}
