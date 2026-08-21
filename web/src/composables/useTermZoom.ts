// useTermZoom.ts — pinch / ctrl-wheel zoom for the terminal, persisted.
import { ref, type Ref } from 'vue'
import { safeStorage } from '../utils/storage'

const KEY = 'herdrem_term_zoom'
const MIN = 0.6
const MAX = 2.5

function clamp(z: number): number {
  return Math.min(MAX, Math.max(MIN, z))
}

export function useTermZoom(scrollEl: Ref<HTMLElement | null>) {
  const zoom = ref(clamp(parseFloat(safeStorage.getItem(KEY) || '1') || 1))

  function setZoom(z: number) {
    zoom.value = clamp(z)
    safeStorage.setItem(KEY, String(zoom.value))
    apply()
  }

  function apply() {
    const el = scrollEl.value
    if (el) el.style.fontSize = `${(zoom.value * 12.5).toFixed(1)}px`
  }

  // --- pinch zoom (touch) ---
  let pinchDist = 0
  function onTouchStart(e: TouchEvent) {
    if (e.touches.length === 2) pinchDist = dist(e.touches[0], e.touches[1])
  }
  function onTouchMove(e: TouchEvent) {
    if (e.touches.length === 2 && pinchDist > 0) {
      const d = dist(e.touches[0], e.touches[1])
      if (Math.abs(d - pinchDist) > 4) {
        setZoom(zoom.value * (d / pinchDist))
        pinchDist = d
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

  return { zoom, setZoom, apply, bind, unbind }
}
