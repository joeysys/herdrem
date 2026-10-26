// Keep the screen on while a terminal view is open. The switch is per device
// and defaults off. The browser Wake Lock API needs a secure context; a
// missing API or a rejected request is ignored.
import { onMounted, onUnmounted, ref, watch } from 'vue'
import { safeStorage } from '../utils/storage'

const STORAGE_KEY = 'herdrem_wake_lock'

const enabled = ref(safeStorage.getItem(STORAGE_KEY) === '1')

let sessions = 0
let sentinel: WakeLockSentinel | null = null
let pending: Promise<void> | null = null
let epoch = 0
let listening = false

export function useWakeLock() {
  function setEnabled(on: boolean) {
    enabled.value = on
    if (on) safeStorage.setItem(STORAGE_KEY, '1')
    else safeStorage.removeItem(STORAGE_KEY)
  }
  return { enabled, setEnabled }
}

function wantLock() {
  return sessions > 0 && enabled.value && typeof document !== 'undefined' && !document.hidden
}

function acquire() {
  if (!wantLock() || pending || sentinel) return
  const locks = navigator.wakeLock
  if (!locks || typeof locks.request !== 'function') return
  const my = ++epoch
  let granted = false
  pending = locks.request('screen').then((sent) => {
    granted = true
    if (my !== epoch || !wantLock()) {
      void sent.release().catch(() => {})
      return
    }
    sentinel = sent
    sent.addEventListener('release', () => {
      if (sentinel === sent) sentinel = null
    })
  }).catch(() => {
    // Unsupported, denied, or the page is not a secure context.
  }).finally(() => {
    pending = null
    if (granted && !sentinel && wantLock()) acquire()
  })
}

function releaseLock() {
  epoch++
  const sent = sentinel
  sentinel = null
  if (!sent) return
  void sent.release().catch(() => {})
}

function onVisibility() {
  if (document.hidden) releaseLock()
  else acquire()
}

function ensureListener() {
  if (listening || typeof document === 'undefined') return
  document.addEventListener('visibilitychange', onVisibility)
  listening = true
}

// useWakeLockSession holds a screen wake lock for as long as this component
// is mounted and the device switch is on. Hidden pages release it; becoming
// visible requests it again.
export function useWakeLockSession() {
  const { enabled: on } = useWakeLock()
  onMounted(() => {
    sessions++
    ensureListener()
    acquire()
  })
  onUnmounted(() => {
    sessions = Math.max(0, sessions - 1)
    if (sessions === 0) releaseLock()
  })
  watch(on, (value) => {
    if (sessions <= 0) return
    if (value) acquire()
    else releaseLock()
  })
}

export function resetWakeLockForTests() {
  sessions = 0
  sentinel = null
  pending = null
  epoch++
  enabled.value = safeStorage.getItem(STORAGE_KEY) === '1'
}
