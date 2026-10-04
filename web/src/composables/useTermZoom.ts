// useTermZoom.ts — pinch / ctrl-wheel zoom for the terminal, persisted.
import { computed, ref, type Ref } from 'vue'
import { safeStorage } from '../utils/storage'

const KEY = 'herdrem_term_zoom'
const MIN = 0.6
const MAX = 2.5

function clamp(z: number): number {
  return Math.min(MAX, Math.max(MIN, z))
}

// Wheel and pinch steps are multiplicative (1.1 × 0.9 = 0.99), so they never
// land back on exactly 100%; anything this close to it counts as 100%.
const SNAP = 0.03

export function useTermZoom(scrollEl: Ref<HTMLElement | null>) {
  const zoom = ref(clamp(parseFloat(safeStorage.getItem(KEY) || '1') || 1))

  /** the zoom as a whole percentage, for display */
  const percent = computed(() => Math.round(zoom.value * 100))

  function setZoom(z: number) {
    zoom.value = Math.abs(z - 1) < SNAP ? 1 : clamp(z)
    safeStorage.setItem(KEY, String(zoom.value))
    apply()
  }

  // The zoom is a factor the terminal's own font sizes multiply (TermView:
  // calc(<size> * var(--term-zoom))). Setting font-size on the scroll box did
  // nothing: .term-body pins its own size, so the change never reached the text.
  function apply() {
    const el = scrollEl.value
    if (el) el.style.setProperty('--term-zoom', zoom.value.toFixed(3))
  }

  // --- pinch zoom (touch) ---
  // Scale from where the gesture started, not step by step: small steps from
  // 100% would each snap back to it, and a slow pinch could never leave 100%.
  let pinchDist = 0
  let pinchZoom = 1
  let lastDist = 0
  function onTouchStart(e: TouchEvent) {
    if (e.touches.length !== 2) return
    pinchDist = lastDist = dist(e.touches[0], e.touches[1])
    pinchZoom = zoom.value
  }
  function onTouchMove(e: TouchEvent) {
    if (e.touches.length === 2 && pinchDist > 0) {
      const d = dist(e.touches[0], e.touches[1])
      if (Math.abs(d - lastDist) > 4) {
        setZoom(pinchZoom * (d / pinchDist))
        lastDist = d
      }
      e.preventDefault()
    }
  }
  function onTouchEnd() {
    pinchDist = 0
  }

  // --- ctrl + wheel zoom (desktop) ---
  function onWheel(e: WheelEvent) {
    if (!e.ctrlKey) return
    e.preventDefault()
    setZoom(zoom.value * (e.deltaY < 0 ? 1.1 : 0.9))
  }

  function dist(a: Touch, b: Touch): number {
    return Math.hypot(a.clientX - b.clientX, a.clientY - b.clientY)
  }

  function bind(el: HTMLElement) {
    el.addEventListener('touchstart', onTouchStart, { passive: true })
    el.addEventListener('touchmove', onTouchMove, { passive: false })
    el.addEventListener('touchend', onTouchEnd, { passive: true })
    el.addEventListener('wheel', onWheel, { passive: false })
    apply()
  }
  function unbind(el: HTMLElement) {
    el.removeEventListener('touchstart', onTouchStart)
    el.removeEventListener('touchmove', onTouchMove)
    el.removeEventListener('touchend', onTouchEnd)
    el.removeEventListener('wheel', onWheel)
  }

  return { zoom, percent, setZoom, apply, bind, unbind }
}
