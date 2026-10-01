import { defineStore } from 'pinia'
import { computed, ref } from 'vue'
import { useConnectionStore } from './connection'
import { useFilesStore } from './files'
import { relayHttpBase, relayMatchesPageOrigin } from '../utils/relay'
import { MAX_UPLOAD_BYTES, startUpload, uniqueName, type UploadHandle } from '../utils/upload'
import { uid } from '../utils/id'

export type UploadStatus = 'queued' | 'uploading' | 'done' | 'conflict' | 'error' | 'cancelled'

export interface UploadItem {
  id: string
  file: File
  /** name it will be stored as (may change with "keep both") */
  name: string
  host: string
  /** absolute destination directory */
  dir: string
  /** sandbox root of the browser at the time, for "fill path" */
  root: string
  status: UploadStatus
  loaded: number
  total: number
  overwrite: boolean
  error?: string
  path?: string
  replaced?: boolean
}

function describe(status: number, message: string): string {
  switch (status) {
    case 0: return message === 'cancelled' ? 'cancelled' : 'network error'
    case 401: return 'not signed in to the relay'
    case 403: return 'not allowed here'
    case 404: return 'folder no longer exists'
    case 413: return 'too large (max 100 MB)'
    default: return message || `failed (${status})`
  }
}

// Upload queue for the file browser. One file at a time, in order; the queue
// lives in a store so uploads keep going while the browser panel is closed.
export const useUploadsStore = defineStore('uploads', () => {
  const conn = useConnectionStore()
  const files = useFilesStore()
  const items = ref<UploadItem[]>([])
  let current: { id: string; handle: UploadHandle } | null = null

  /**
   * The relay can take uploads from this page. On the relay's own origin the
   * session cookie is enough; from any other origin the upload goes out with
   * the relay token as a Bearer header (the relay never accepts a cookie
   * cross-origin), so a token is needed there.
   */
  const supported = computed(() => conn.hasFeature('file-upload'))
  const sameOrigin = computed(() => relayMatchesPageOrigin(conn.url))
  const canUpload = computed(() => supported.value && (sameOrigin.value || !!conn.token))

  const active = computed(() => items.value.filter((i) => i.status === 'queued' || i.status === 'uploading').length)
  const finished = computed(() => items.value.filter((i) => i.status === 'done' || i.status === 'cancelled').length)

  function find(id: string) {
    return items.value.find((i) => i.id === id)
  }

  /** Queue files for the given directory. */
  function add(list: Iterable<File>, target: { host: string; dir: string; root: string }) {
    for (const file of list) {
      const item: UploadItem = {
        id: uid(), file, name: file.name, host: target.host, dir: target.dir, root: target.root,
        status: 'queued', loaded: 0, total: file.size, overwrite: false,
      }
      if (file.size > MAX_UPLOAD_BYTES) {
        item.status = 'error'
        item.error = 'too large (max 100 MB)'
      }
      items.value.push(item)
    }
    pump()
  }

  function pump() {
    if (current) return
    const next = items.value.find((i) => i.status === 'queued')
    if (!next) return
    next.status = 'uploading'
    next.loaded = 0
    next.error = undefined
    const handle = startUpload({
      base: relayHttpBase(conn.url),
      token: conn.token || undefined,
      host: next.host,
      dir: next.dir,
      name: next.name,
      file: next.file,
      overwrite: next.overwrite,
      onProgress: (loaded, total) => {
        const it = find(next.id)
        if (it) { it.loaded = loaded; it.total = total }
      },
    })
    current = { id: next.id, handle }
    void handle.promise.then((r) => {
      current = null
      const it = find(next.id)
      if (it && it.status === 'uploading') {
        if (r.ok) {
          it.status = 'done'
          it.loaded = it.total
          it.path = r.path
          it.replaced = r.replaced
          refreshIfShowing(it)
        } else if (r.aborted) {
          it.status = 'cancelled'
        } else if (r.status === 409) {
          it.status = 'conflict'
        } else {
          it.status = 'error'
          it.error = describe(r.status, r.message)
        }
      }
      pump()
    })
  }

  // Show the new file if the browser is looking at that folder.
  function refreshIfShowing(it: UploadItem) {
    const norm = (p: string) => p.replace(/\/+$/, '') || '/'
    if (files.host === it.host && norm(files.path) === norm(it.dir)) {
      files.navigate(files.path)
      files.scrollToFile = it.name
    }
  }

  /** Conflict: replace the existing file. */
  function replace(id: string) {
    const it = find(id)
    if (!it || it.status !== 'conflict') return
    it.overwrite = true
    it.status = 'queued'
    pump()
  }

  /** Conflict: store under the next free "name (n).ext". */
  function keepBoth(id: string) {
    const it = find(id)
    if (!it || it.status !== 'conflict') return
    const taken = new Set<string>([it.name])
    const norm = (p: string) => p.replace(/\/+$/, '') || '/'
    if (files.host === it.host && norm(files.path) === norm(it.dir)) {
      for (const e of files.entries) taken.add(e.name)
    }
    for (const other of items.value) {
      if (other.id !== it.id && other.dir === it.dir && other.host === it.host && other.status !== 'cancelled') taken.add(other.name)
    }
    it.name = uniqueName(it.name, taken)
    it.status = 'queued'
    pump()
  }

  /** Conflict / queued / uploading: give up on this one. */
  function cancel(id: string) {
    const it = find(id)
    if (!it) return
    if (it.status === 'uploading' && current?.id === id) {
      current.handle.abort() // resolves as aborted → cancelled
      return
    }
    if (it.status === 'queued' || it.status === 'conflict') it.status = 'cancelled'
  }

  function retry(id: string) {
    const it = find(id)
    if (!it || it.status !== 'error' || it.file.size > MAX_UPLOAD_BYTES) return
    it.status = 'queued'
    pump()
  }

  function dismiss(id: string) {
    const it = find(id)
    if (!it || it.status === 'uploading' || it.status === 'queued') return
    items.value = items.value.filter((i) => i.id !== id)
  }

  /** Drop finished rows (uploaded, skipped, cancelled). */
  function clearFinished() {
    items.value = items.value.filter((i) => i.status !== 'done' && i.status !== 'cancelled')
  }

  return {
    items, supported, sameOrigin, canUpload, active, finished,
    add, replace, keepBoth, cancel, retry, dismiss, clearFinished,
  }
})
