// useKeyRepeat.ts — repeat a key while a button is held (after a delay).
import { onUnmounted } from 'vue'

export function useKeyRepeat() {
  let delayTimer: ReturnType<typeof setTimeout> | null = null
  let repeatTimer: ReturnType<typeof setInterval> | null = null

  function start(fn: () => void, initialDelay = 400, interval = 120) {
    stop()
    fn() // fire once immediately
    delayTimer = setTimeout(() => {
      repeatTimer = setInterval(fn, interval)
    }, initialDelay)
  }

  function stop() {
    if (delayTimer) { clearTimeout(delayTimer); delayTimer = null }
    if (repeatTimer) { clearInterval(repeatTimer); repeatTimer = null }
  }

  onUnmounted(stop)
  return { start, stop }
}
