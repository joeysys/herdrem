import { defineStore } from 'pinia'
import { ref, computed } from 'vue'
import { ws } from '../api/ws'
import type { FileRead, FileList, FileEntry, FileContent } from '../api/types'

export interface PreviewState {
  path: string
  content: string
  encoding: 'utf-8' | 'base64'
  mimeType: string
  isText: boolean
  isImage: boolean
  size: number
  targetLine: number | null
  error?: string
}

// files store drives the file browser + preview for the active pane.
export const useFilesStore = defineStore('files', () => {
  const browserOpen = ref(false)
  const host = ref('local')
  const root = ref('/') // sandbox root (pane cwd)
  const path = ref('/') // current dir
  const entries = ref<FileEntry[]>([])
  const loading = ref(false)
  const preview = ref<PreviewState | null>(null)
  const previewMax = ref(false) // hide file tree, show only preview
  const fullMax = ref(false) // full-screen overlay, no width limit
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
      // Preserve previously visited directory path & open preview file (if any)
      browserOpen.value = true
      list(path.value)
      if (preview.value?.path) {
        openFile(preview.value.path, preview.value.targetLine)
      }
    } else {
      // Initial open or root/host changed — reset to root directory
      path.value = newRoot
      preview.value = null
      previewMax.value = false
      fullMax.value = false
      browserOpen.value = true
      list(path.value)
    }
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
    preview.value = null
    previewMax.value = false
    fullMax.value = false
    scrollToFile.value = null
  }

  /** Close only the preview, back to the file tree */
  function closePreview() {
    preview.value = null
    previewMax.value = false
  }

  function list(dirPath: string) {
    loading.value = true
    const msg: FileList = { type: 'file_list', path: dirPath, host: host.value }
    ws.send(msg)
  }

  function navigate(dirPath: string) {
    path.value = dirPath
    preview.value = null
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
    const msg: FileRead = { type: 'file_read', path: filePath, host: host.value }
    ws.send(msg)
    preview.value = {
      path: filePath, content: '', encoding: 'utf-8', mimeType: '',
      isText: true, isImage: false, size: 0, targetLine: line,
    }
    // In full mode, keep the tree visible side-by-side; otherwise hide it
    if (!fullMax.value) previewMax.value = true
  }

  /** open a file directly in preview (from a terminal link), entering its dir */
  function openPath(filePath: string, paneCwd: string, paneHost: string, line: number | null) {
    root.value = paneCwd || '/'
    host.value = paneHost || 'local'
    const dir = filePath.replace(/\/[^/]*$/, '') || '/'
    path.value = dir
    browserOpen.value = true
    previewMax.value = true // hide tree by default when opening from term
    fullMax.value = false
    list(dir)
    openFile(filePath, line)
  }

  function setListing(listPath: string, list: FileEntry[]) {
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
    preview.value = {
      ...preview.value,
      content: msg.content,
      encoding: msg.encoding || 'utf-8',
      mimeType: msg.mime_type || '',
      isText: msg.is_text ?? true,
      isImage: msg.is_image ?? false,
      size: msg.size ?? 0,
    }
  }

  function setError() {
    loading.value = false
    if (preview.value) {
      preview.value = { ...preview.value, content: '', error: 'file not found or unreadable' }
    }
  }

  function toggleMax() {
    previewMax.value = !previewMax.value
  }

  /** Show the file tree (restore from preview-only mode), scrolling to current file */
  function showTree() {
    previewMax.value = false
    // Set scroll target so the tree can scroll to the file after listing
    if (preview.value) {
      const fileName = preview.value.path.split('/').pop() || ''
      scrollToFile.value = fileName
      // Re-list the current dir to ensure entries are loaded
      list(path.value)
    }
  }

  function toggleFullMax() {
    fullMax.value = !fullMax.value
  }

  function clearScrollTarget() {
    scrollToFile.value = null
  }

  return {
    browserOpen, host, root, path, entries, loading, preview, previewMax,
    fullMax, scrollToFile,
    breadcrumbs, canGoUp,
    openBrowser, closeBrowser, closePreview, reset, navigate, goUp, openFile, openPath,
    setListing, setContent, setError, toggleMax, showTree, toggleFullMax,
    clearScrollTarget,
  }
})

function normalize(p: string): string {
  return p.replace(/\/+$/, '') || '/'
}

/** makeRelative strips the root prefix from a path, returning a relative path. */
function makeRelative(p: string, root: string): string {
  const np = normalize(p)
  const nr = normalize(root)
  if (np === nr) return ''
  if (np.startsWith(nr + '/')) return np.slice(nr.length + 1)
  return np
}
