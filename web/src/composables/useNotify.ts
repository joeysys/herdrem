// useNotify.ts — system notifications for blocked panes (and done panes while
// the app is in the background), plus done-transition toasts/flash.
import { ref } from 'vue'
import type { Pane } from '../api/types'
import { bucket } from '../stores/connection'

export interface Toast {
  id: number
  message: string
  type: 'blocked' | 'done' | 'info'
  paneId?: string
}

let toastId = 0

// Module-level toast state — any component can push toasts.
const toasts = ref<Toast[]>([])

function pushToast(t: Omit<Toast, 'id'>, ms = 3200) {
  const id = ++toastId
  toasts.value.push({ ...t, id })
  setTimeout(() => {
    toasts.value = toasts.value.filter((x) => x.id !== id)
  }, ms)
}

function dismissToast(id: number) {
  toasts.value = toasts.value.filter((x) => x.id !== id)
}

/**
 * showSystemNotification goes through the service worker when there is one:
 * Android Chrome (browser and installed PWA) throws "Illegal constructor" on
 * `new Notification()`, so the constructor is only the fallback for pages
 * without a service worker (dev server, desktop without SW).
 */
export async function showSystemNotification(title: string, opts: NotificationOptions & { renotify?: boolean }): Promise<boolean> {
  if (!('Notification' in window) || Notification.permission !== 'granted') return false
  try {
    const reg = 'serviceWorker' in navigator ? await navigator.serviceWorker.getRegistration() : undefined
    if (reg) {
      await reg.showNotification(title, opts)
      return true
    }
    const n = new Notification(title, opts)
    n.onclick = () => window.focus()
    return true
  } catch {
    return false
  }
}

export function useNotify() {
  const justDonePanes = ref<Set<string>>(new Set())
  const statusMemory = new Map<string, string>()
  const seenBlocked = new Set<string>()

  // --- browser notification permission ---
  // Asked on the first tap/key press: mobile browsers and installed PWAs
  // ignore (or quietly block) a permission request made without a gesture.
  function ensurePermission() {
    if (!('Notification' in window) || Notification.permission !== 'default') return
    const ask = () => {
      document.removeEventListener('pointerdown', ask, true)
      document.removeEventListener('keydown', ask, true)
      if (Notification.permission === 'default') Notification.requestPermission().catch(() => {})
    }
    document.addEventListener('pointerdown', ask, true)
    document.addEventListener('keydown', ask, true)
  }

  function notifyBlocked(pane: Pane) {
    const title = `🔒 ${pane.project || pane.agent || pane.pane_id}`
    const body = pane.prompt ? pane.prompt.slice(0, 120) : 'Waiting for approval'
    void showSystemNotification(title, { body, tag: pane.pane_id, renotify: true, data: { paneId: pane.pane_id } })
    vibrate(200)
    pushToast({ message: `${pane.project || pane.agent} blocked`, type: 'blocked', paneId: pane.pane_id })
  }

  function notifyDone(pane: Pane) {
    const label = pane.project || pane.agent || pane.pane_id
    // The toast covers a visible app; a system notification only when it is
    // in the background (another tab/app, or the phone's screen is off).
    if (document.hidden) {
      void showSystemNotification(`✅ ${label}`, { body: 'Finished', tag: pane.pane_id, renotify: true, data: { paneId: pane.pane_id } })
    }
    pushToast({ message: `✅ ${label} done`, type: 'done', paneId: pane.pane_id })
    vibrate(100)
    justDonePanes.value.add(pane.pane_id)
    setTimeout(() => justDonePanes.value.delete(pane.pane_id), 2000)
  }

  function vibrate(pattern: number | number[]) {
    if ('vibrate' in navigator) {
      try { navigator.vibrate(pattern) } catch { /* ignore */ }
    }
  }

  /**
   * processSnapshot diffes the incoming agents against memory and fires
   * blocked/done notifications. Call on every agents snapshot.
   */
  function processSnapshot(agents: Pane[]) {
    const seen = new Set<string>()
    for (const pane of agents) {
      const id = pane.pane_id
      seen.add(id)
      const b = bucket(pane.status)
      const prev = statusMemory.get(id)

      // newly blocked
      if (b === 'blocked' && prev !== 'blocked' && !seenBlocked.has(id)) {
        seenBlocked.add(id)
        if (prev !== undefined) notifyBlocked(pane) // skip initial load burst
      }
      if (b !== 'blocked') seenBlocked.delete(id)

      // done transition: working/blocked → done/idle
      if (prev && (prev === 'working' || prev === 'blocked') && (b === 'done' || b === 'idle')) {
        notifyDone(pane)
      }
      statusMemory.set(id, b)
    }
    // prune memory for vanished panes
    for (const id of [...statusMemory.keys()]) {
      if (!seen.has(id)) statusMemory.delete(id)
    }
  }

  return {
    toasts,
    justDonePanes,
    ensurePermission,
    processSnapshot,
    dismissToast,
    notifyBlocked,
    push: pushToast,
  }
}
