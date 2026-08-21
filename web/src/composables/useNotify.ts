// useNotify.ts — blocked browser notifications + done-transition toasts/flash.
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

export function useNotify() {
  const justDonePanes = ref<Set<string>>(new Set())
  const statusMemory = new Map<string, string>()
  const seenBlocked = new Set<string>()

  // --- browser notification permission ---
  function ensurePermission() {
    if ('Notification' in window && Notification.permission === 'default') {
      Notification.requestPermission().catch(() => {})
    }
  }

  function notifyBlocked(pane: Pane) {
    const title = `🔒 ${pane.project || pane.agent || pane.pane_id}`
    const body = pane.prompt ? pane.prompt.slice(0, 120) : 'Waiting for approval'
    if ('Notification' in window && Notification.permission === 'granted') {
      try {
        const n = new Notification(title, { body, tag: pane.pane_id })
        n.onclick = () => window.focus()
      } catch {
        /* fall through to toast */
      }
    }
    vibrate(200)
    pushToast({ message: `${pane.project || pane.agent} blocked`, type: 'blocked', paneId: pane.pane_id })
  }

  function notifyDone(pane: Pane) {
    const label = pane.project || pane.agent || pane.pane_id
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
