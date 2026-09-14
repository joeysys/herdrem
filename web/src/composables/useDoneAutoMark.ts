// useDoneAutoMark.ts — after a finished (done) pane's terminal has been open
// for `delay` ms, auto-send mark_pane_read so it flips to idle without the
// user having to click the done badge. The timer pauses while the page is
// hidden (the user isn't viewing the term then) and is cancelled if the pane
// leaves done or the view unmounts. `paneId` is the pane identity: when the
// host view is reused across panes (mobile layout keeps one TermView), a
// done→done switch must restart the delay for the new pane instead of firing
// a timer armed for the old one. mark() itself re-checks the done state.
import { onScopeDispose, watch, type Ref } from 'vue'

export function useDoneAutoMark(done: Ref<boolean>, delay: Ref<number>, paneId: Ref<string>, mark: () => void) {
  let timer: ReturnType<typeof setTimeout> | null = null

  function clear() {
    if (timer) {
      clearTimeout(timer)
      timer = null
    }
  }

  function arm() {
    clear()
    if (!done.value || delay.value <= 0 || document.hidden) return
    timer = setTimeout(() => {
      timer = null
      if (done.value) mark()
    }, delay.value)
  }

  function onVisibility() {
    if (document.hidden) clear()
    else arm()
  }

  // Re-arm on every fresh done transition, pane switch (done flips or the
  // identity itself changes under a reused view), and on live pref changes
  // to the delay.
  watch([done, delay, paneId], arm, { immediate: true, flush: 'sync' })
  document.addEventListener('visibilitychange', onVisibility)
  onScopeDispose(() => {
    clear()
    document.removeEventListener('visibilitychange', onVisibility)
  })
}
