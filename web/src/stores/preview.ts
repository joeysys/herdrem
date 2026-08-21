import { defineStore } from 'pinia'
import { ref } from 'vue'
import { ws } from '../api/ws'
import type { ReadPane } from '../api/types'
import { stripAnsi } from '../utils/ansi'

// previews store holds short text snapshots per pane for grid cards, refreshed
// periodically via read_pane with purpose="preview".
export const usePreviewsStore = defineStore('previews', () => {
  const previews = ref<Map<string, string>>(new Map())
  const pending = ref<Set<string>>(new Set())

  function setPreview(paneId: string, content: string) {
    previews.value.set(paneId, formatPreview(content))
    pending.value.delete(paneId)
  }

  function hasPreview(paneId: string): boolean {
    return previews.value.has(paneId)
  }

  function isPending(paneId: string): boolean {
    return pending.value.has(paneId)
  }

  /** request a preview read for a pane (dedupes in-flight requests) */
  function requestPreview(paneId: string, lines = 8) {
    if (pending.value.has(paneId)) return
    pending.value.add(paneId)
    const msg: ReadPane = { type: 'read_pane', pane_id: paneId, lines, purpose: 'preview' }
    ws.send(msg)
    // safety: clear pending if no response within a while
    setTimeout(() => pending.value.delete(paneId), 8000)
  }

  function clear() {
    previews.value.clear()
    pending.value.clear()
  }

  return { previews, setPreview, hasPreview, isPending, requestPreview, clear }
})

/** formatPreview strips ANSI and trims to the last few meaningful lines. */
export function formatPreview(raw: string, maxLines = 8): string {
  const text = stripAnsi(String(raw ?? ''))
  const lines = text
    .split('\n')
    .map((l) => l.replace(/\s+$/g, ''))
    .filter((l) => l.trim())
  return lines.slice(-maxLines).join('\n')
}
