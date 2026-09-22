import { defineStore } from 'pinia'
import { ref, computed } from 'vue'
import { ws } from '../api/ws'
import type { FileRead, FileList, FileEntry, FileContent } from '../api/types'

export interface PreviewState {
  path: string
  root: string
  host: string
  reqId: string
  content: string
  encoding: 'utf-8' | 'base64'
  mimeType: string
  isText: boolean
  isImage: boolean
  truncated: boolean
  loaded: boolean
  size: number
  targetLine: number | null
  error?: string
}

// Directory and preview state live together because they share the file protocol,
// but opening either UI never mutates or opens the other one. Each panel tracks
// its own request id so a stale response (different host or an earlier request
// to the same path) cannot overwrite the current content.
export const useFilesStore = defineStore('files', () => {
  const browserOpen = ref(false)
  const browserDocked = ref(true)
  const host = ref('local')
  const root = ref('/') // sandbox root (pane cwd)
  const path = ref('/') // current dir
  const entries = ref<FileEntry[]>([])
  const loading = ref(false)
  const listReqId = ref('') // request id of the in-flight file_list
  const preview = ref<PreviewState | null>(null)
  const scrollToFile = ref<string | null>(null) // file name to scroll to in tree

  const breadcrumbs = computed(() => {
    // Show relative path from root; strip the root prefix
    const rel = makeRelative(path.value, root.value)
    const parts = rel.replace(/\/+$/, '').split('/').filter(Boolean)
    const crumbs: Array<{ name: string; path: string }> = []
    let acc = root.value.replace(/\/+$/, '')
    for (const p of parts) {
      acc += '/' + p
      crumbs.push({ name: p, path: acc })
    }
    return crumbs
  })

  const canGoUp = computed(() => normalize(path.value) !== normalize(root.value))

  function openBrowser(paneCwd: string, paneHost: string) {
    const newRoot = paneCwd || '/'
    const newHost = paneHost || 'local'

    // Check if re-opening within the same root & host, and path is still valid under root
    const sameRootHost = host.value === newHost && normalize(root.value) === normalize(newRoot)
    const validPathUnderRoot = sameRootHost && normalize(path.value).startsWith(normalize(newRoot))

    root.value = newRoot
    host.value = newHost

    if (validPathUnderRoot) {
      // Preserve the previously visited directory.
      browserOpen.value = true
      list(path.value)
    } else {
      // Initial open or root/host changed — reset only the directory.
      path.value = newRoot
      browserOpen.value = true
      list(path.value)
    }
  }

  function openBrowserAt(dirPath: string, paneRoot: string, paneHost: string) {
    const base = paneRoot || '/'
    const target = dirPath || base
    host.value = paneHost || 'local'
    // The dir is listable whenever it sits inside any pane-cwd sandbox root —
    // not necessarily this pane's cwd. Up-navigation is bounded by root, so a
    // target outside it must self-root: otherwise ".." no-ops and system back
    // consumes presses without ever reaching a closable state.
    root.value = isWithin(target, base) ? base : target
    path.value = target
    browserOpen.value = true
    list(path.value)
  }

  function closeBrowser() {
    browserOpen.value = false
  }

  /** Drop all browser/preview state — used when switching relays. */
  function reset() {
    browserOpen.value = false
    host.value = 'local'
    root.value = '/'
    path.value = '/'
    entries.value = []
    loading.value = false
    listReqId.value = ''
    preview.value = null
    scrollToFile.value = null
  }

  /** Close only the preview, back to the file tree */
  function closePreview() {
    preview.value = null
  }

  function newReqId(): string {
    return crypto.randomUUID()
  }

  function list(dirPath: string) {
    loading.value = true
    const reqId = newReqId()
    listReqId.value = reqId
    const msg: FileList = { type: 'file_list', path: dirPath, host: host.value, req_id: reqId }
    ws.send(msg)
  }

  function navigate(dirPath: string) {
    path.value = dirPath
    list(dirPath)
  }

  function goUp() {
    if (!canGoUp.value) return
    const parent = path.value.replace(/\/+$/, '').split('/').slice(0, -1).join('/') || '/'
    // never navigate above the sandbox root
    if (normalize(parent).length < normalize(root.value).length) return
    navigate(parent)
  }

  function openFile(filePath: string, line: number | null = null) {
    openPath(filePath, root.value, host.value, line)
  }

  /** Open a preview directly, without changing or opening the directory panel. */
  function openPath(filePath: string, paneRoot: string, paneHost: string, line: number | null) {
    const previewHost = paneHost || 'local'
    const reqId = newReqId()
    const msg: FileRead = { type: 'file_read', path: filePath, host: previewHost, req_id: reqId }
    ws.send(msg)
    preview.value = {
      path: filePath, root: paneRoot || '/', host: previewHost, reqId,
      content: '', encoding: 'utf-8', mimeType: '',
      isText: true, isImage: false, truncated: false, loaded: false, size: 0, targetLine: line,
    }
  }

  function setListing(listPath: string, list: FileEntry[], reqId?: string) {
    if (reqId && listReqId.value && reqId !== listReqId.value) return
    if (normalize(listPath) !== normalize(path.value)) return
    entries.value = [...list].sort((a, b) => {
      if (a.type === 'dir' && b.type !== 'dir') return -1
      if (a.type !== 'dir' && b.type === 'dir') return 1
      return a.name.localeCompare(b.name)
    })
    loading.value = false
  }

  function setContent(msg: FileContent) {
    if (!preview.value || preview.value.path !== msg.path) return
    if (msg.req_id && preview.value.reqId && msg.req_id !== preview.value.reqId) return
    preview.value = {
      ...preview.value,
      content: msg.content,
      encoding: msg.encoding || 'utf-8',
      mimeType: msg.mime_type || '',
      isText: msg.is_text ?? true,
      isImage: msg.is_image ?? false,
      truncated: msg.truncated ?? false,
      loaded: true,
      size: msg.size ?? 0,
    }
  }

  /** A file_list request failed — only the directory panel cares. */
  function setListError(reqId?: string) {
    if (reqId && listReqId.value && reqId !== listReqId.value) return
    loading.value = false
  }

  /** A file_read request failed — only the preview panel cares. */
  function setPreviewError(reqId?: string) {
    if (!preview.value) return
    if (reqId && preview.value.reqId && reqId !== preview.value.reqId) return
    preview.value = { ...preview.value, content: '', error: 'file not found or unreadable' }
  }

  /** The read target turned out to be a directory (relayed as "is_dir"):
   *  swap the pending preview for the directory panel at that path. */
  function previewIsDir(reqId?: string) {
    const p = preview.value
    if (!p) return
    if (reqId && p.reqId && reqId !== p.reqId) return
    preview.value = null
    openBrowserAt(p.path, p.root, p.host)
  }

  function toggleDocked() {
    browserDocked.value = !browserDocked.value
  }

  function clearScrollTarget() {
    scrollToFile.value = null
  }

  return {
    browserOpen, browserDocked, host, root, path, entries, loading, listReqId, preview,
    scrollToFile,
    breadcrumbs, canGoUp,
    openBrowser, openBrowserAt, closeBrowser, closePreview, reset, navigate, goUp, openFile, openPath,
    setListing, setContent, setListError, setPreviewError, previewIsDir, toggleDocked,
    clearScrollTarget,
  }
})

function normalize(p: string): string {
  return p.replace(/\/+$/, '') || '/'
}

/** isWithin reports whether p equals root or sits underneath it (path-segment boundary). */
function isWithin(p: string, root: string): boolean {
  const np = normalize(p)
  const nr = normalize(root)
  return np === nr || np.startsWith(nr === '/' ? '/' : nr + '/')
}

/** makeRelative strips the root prefix from a path, returning a relative path. */
function makeRelative(p: string, root: string): string {
  const np = normalize(p)
  const nr = normalize(root)
  if (np === nr) return ''
  if (np.startsWith(nr + '/')) return np.slice(nr.length + 1)
  return np
}
