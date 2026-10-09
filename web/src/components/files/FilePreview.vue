<script setup lang="ts">
import { computed, nextTick, onBeforeUnmount, onMounted, ref, watch } from 'vue'
import { useFilesStore } from '../../stores/files'
import { useConnectionStore } from '../../stores/connection'
import { useTerminalStore } from '../../stores/terminal'
import { useTunnelsStore } from '../../stores/tunnels'
import { useHaptic } from '../../composables/useHaptic'
import { usePreviewWidth } from '../../composables/usePreviewWidth'
import { renderMarkdown } from '../../utils/markdown'
import { highlight, langForPath } from '../../utils/highlight'
import { relayFileUrl, relayHttpBase } from '../../utils/relay'
import { applyHighlights, clearHighlights, matchesQuery, setActiveGroup, MAX_HITS } from '../../utils/domHighlight'
import FindBar from '../common/FindBar.vue'
import { addOverlaySource, isShortcutActive, register, shortcutChord, shortcutVersion } from '../../composables/useShortcuts'

const files = useFilesStore()
const conn = useConnectionStore()
const term = useTerminalStore()
const tunnels = useTunnelsStore()
const { haptic } = useHaptic()
const props = defineProps<{ directoryOpen: boolean }>()
const emit = defineEmits<{
  close: []
  openDirectory: [payload: { path: string; root: string; host: string }]
  toggleDirectory: [payload: { path: string; root: string; host: string }]
}>()

const preview = computed(() => files.preview)
const isMd = computed(() => /\.(md|markdown)$/i.test(preview.value?.path || ''))
const isHtml = computed(() => /\.(html|htm)$/i.test(preview.value?.path || ''))
const isSvg = computed(() => /\.svg$/i.test(preview.value?.path || ''))
const hasScripts = computed(() => {
  if (!isHtml.value) return false
  if (preview.value?.truncated) return true
  const c = preview.value?.content
  if (!c) return false
  return /<script\b|\bon[a-z]{3,}\s*=|javascript\s*:/i.test(c)
})

const copyStatus = ref<'idle' | 'copied' | 'error'>('idle')
let copyTimer: ReturnType<typeof setTimeout> | null = null

const canCopy = computed(() => {
  const p = preview.value
  if (!p || !p.loaded || p.truncated || p.error) return false
  if (p.isImage && !isSvg.value) return false
  return p.isText || isSvg.value
})

const copyTitle = computed(() => {
  if (preview.value?.error) return 'File content unavailable'
  if (preview.value && !preview.value.loaded) return 'Reading file content'
  if (preview.value?.truncated) return 'File content truncated'
  if (copyStatus.value === 'copied') return 'Copied to clipboard'
  if (copyStatus.value === 'error') return 'Failed to copy'
  return 'Copy file content'
})

const copyLabel = computed(() => {
  if (copyStatus.value === 'copied') return '✓ copied'
  if (copyStatus.value === 'error') return '⚠ failed'
  return '⧉ copy'
})

function fallbackCopy(text: string): boolean {
  try {
    const textarea = document.createElement('textarea')
    textarea.value = text
    textarea.readOnly = true // select without raising the soft keyboard
    textarea.style.position = 'fixed'
    textarea.style.left = '-9999px'
    textarea.style.top = '-9999px'
    textarea.style.opacity = '0'
    document.body.appendChild(textarea)
    textarea.focus()
    textarea.select()
    const result = typeof document.execCommand === 'function' ? document.execCommand('copy') : false
    document.body.removeChild(textarea)
    return result
  } catch {
    return false
  }
}

async function copyContent() {
  haptic()
  if (!preview.value || !canCopy.value) return
  if (copyTimer) clearTimeout(copyTimer)

  const textToCopy = preview.value.content ?? ''
  let success = false

  try {
    if (navigator.clipboard?.writeText) {
      await navigator.clipboard.writeText(textToCopy)
      success = true
    } else {
      success = fallbackCopy(textToCopy)
    }
  } catch {
    success = fallbackCopy(textToCopy)
  }

  if (success) {
    copyStatus.value = 'copied'
  } else {
    copyStatus.value = 'error'
  }

  copyTimer = setTimeout(() => {
    copyStatus.value = 'idle'
  }, 2000)
}

const findTitle = computed(() => {
  void shortcutVersion.value
  return isShortcutActive('file.find') ? `Search file (${shortcutChord('file.find')})` : 'Search file'
})

let offFind: (() => void) | null = null
let offOverlay: (() => void) | null = null
onBeforeUnmount(() => {
  if (copyTimer) clearTimeout(copyTimer)
  if (findDebounce) clearTimeout(findDebounce)
  offFind?.()
  offFind = null
  offOverlay?.()
  offOverlay = null
})
onMounted(() => {
  offOverlay = addOverlaySource(() => !!preview.value)
  offFind = register({
    id: 'file.find',
    label: 'Search file',
    scope: 'overlay',
    key: 'f',
    mod: true,
    browserTab: 'blocked',
    blockedNote: 'In a normal browser tab, Ctrl/⌘+F is the browser find. Use the search button',
    when: () => {
      if (!preview.value || !searchable.value) return false
      const active = document.activeElement
      return !active || active === document.body || !!rootEl.value?.contains(active)
    },
    run: () => openFind(),
  })
})

const fileName = computed(() => {
  const full = preview.value?.path || ''
  const root = preview.value?.root || '/'
  // Show relative path if under root, otherwise just basename
  const rel = full.startsWith(root + '/') ? full.slice(root.length + 1) : full
  return rel.split('/').pop() || ''
})

const previewRelPath = computed(() => {
  const full = preview.value?.path || ''
  const root = (preview.value?.root || '/').replace(/\/+$/, '')
  if (!full) return ''
  if (full.startsWith(root + '/')) return full.slice(root.length + 1)
  return full
})

const previewDirs = computed(() => {
  const p = preview.value
  if (!p) return []
  const root = p.root.replace(/\/+$/, '') || '/'
  const parent = p.path.replace(/\/[^/]*$/, '') || '/'
  const rel = parent === root ? '' : parent.startsWith(root + '/') ? parent.slice(root.length + 1) : parent
  const dirs = [{ name: '⌂', path: root }]
  let current = root === '/' ? '' : root
  for (const name of rel.split('/').filter(Boolean)) {
    current += `/${name}`
    dirs.push({ name, path: current })
  }
  return dirs
})

function openDirectory(path: string) {
  const p = preview.value
  if (p) emit('openDirectory', { path, root: p.root, host: p.host })
}

function toggleDirectory() {
  const p = preview.value
  const dir = previewDirs.value[previewDirs.value.length - 1]
  if (p && dir) emit('toggleDirectory', { path: dir.path, root: p.root, host: p.host })
}

function fillPreviewPath() {
  haptic()
  if (previewRelPath.value) {
    term.fillPath(previewRelPath.value)
  }
}

const lang = computed(() => langForPath(preview.value?.path || ''))

// Code line width setting: 'screen' wraps at the panel edge; a number pins
// wrapping to that many characters; 'none' disables wrapping so one real line
// stays one visual line. The width constraint must live on an inner block
// inside the cell — in auto table layout a td's own width is only a floor,
// so a long line would stretch the column instead of wrapping at N chars.
const { previewWidth } = usePreviewWidth()
const codeWidthClass = computed(() => {
  const w = previewWidth.value
  if (w === 'none') return 'w-none'
  if (w === 'screen') return ''
  return 'w-num'
})
const codeCellStyle = computed(() =>
  codeWidthClass.value === 'w-num' ? { width: `${previewWidth.value}ch` } : undefined,
)

// markdown & html preview modes
const mdRendered = ref(true)
const htmlRendered = ref(true)
const viewportWidth = ref<'100%' | '768px' | '375px'>('100%')
const authReady = ref(false)
const authError = ref('')

watch(preview, (p) => {
  mdRendered.value = !(p && p.targetLine != null && isMd.value)
  htmlRendered.value = true
  copyStatus.value = 'idle'
  if (copyTimer) clearTimeout(copyTimer)
})

const lines = computed(() => {
  if (!preview.value || (preview.value.isImage && !isSvg.value)) return []
  return highlight(preview.value.content, lang.value)
})

const renderedMd = computed(() => {
  if (!preview.value || !isMd.value || !mdRendered.value) return ''
  return renderMarkdown(preview.value.content)
})

// previewUrl uses a path-based URL (/preview/<path>) so that relative URLs in
// HTML documents (CSS, JS, images) resolve against the file's directory.
// The URL carries only a short-lived preview capability, never the relay token.
const previewCapability = ref('')
const previewUrl = computed(() => {
  if (!preview.value?.path) return ''
  const base = relayHttpBase(conn.url)
  const encodedHost = btoa(preview.value.host || 'local').replace(/\+/g, '-').replace(/\//g, '_').replace(/=+$/, '')
  const encodedPath = preview.value.path.split('/').map(encodeURIComponent).join('/')
  const capability = previewCapability.value ? `${previewCapability.value}/` : ''
  return `${base}/preview/${capability}${encodedHost}/${encodedPath}`
})

// previewAuthUrl mints a scoped preview capability via a same-origin fetch.
// The relay token stays in this parent-page request's Authorization header,
// never in a URL or the iframe URL.
const previewAuthUrl = computed(() => {
  const base = relayHttpBase(conn.url)
  const params = new URLSearchParams({ host: preview.value?.host || 'local' })
  return `${base}/preview-auth?${params.toString()}`
})

const fileTunnel = computed(() => {
  if (!preview.value?.path) return null
  return tunnels.getFileTunnel(preview.value.path, preview.value.host || 'local')
})

const fullTunnelUrl = computed(() => {
  if (!fileTunnel.value?.public_url) return ''
  if (fileTunnel.value.auth_mode === 'token_gate' && fileTunnel.value.token) {
    const sep = fileTunnel.value.public_url.includes('?') ? '&' : '?'
    return `${fileTunnel.value.public_url}${sep}token=${encodeURIComponent(fileTunnel.value.token)}`
  }
  return fileTunnel.value.public_url
})

const tunnelStarting = ref(false)
const copiedTunnelUrl = ref(false)
const tunnelCreateError = ref<string | null>(null)
let tunnelStartingTimer: ReturnType<typeof setTimeout> | null = null

function clearTunnelStartingTimer() {
  if (tunnelStartingTimer) {
    clearTimeout(tunnelStartingTimer)
    tunnelStartingTimer = null
  }
}

function startTunnelPreview() {
  haptic()
  if (!preview.value?.path) return
  if (fileTunnel.value?.status === 'active') {
    if (fullTunnelUrl.value) {
      window.open(fullTunnelUrl.value, '_blank')
    }
    return
  }
  tunnelCreateError.value = null
  tunnelStarting.value = true
  clearTunnelStartingTimer()
  tunnelStartingTimer = setTimeout(() => {
    tunnelStarting.value = false
  }, 15000)
  tunnels.createFileTunnel(preview.value.path, preview.value.host || 'local', 'token_gate')
}

function dismissTunnelError() {
  tunnelCreateError.value = null
  if (fileTunnel.value?.id) {
    tunnels.dismissTunnel(fileTunnel.value.id)
  }
}

async function copyTunnelUrl() {
  haptic()
  if (!fullTunnelUrl.value) return
  try {
    await navigator.clipboard.writeText(fullTunnelUrl.value)
    copiedTunnelUrl.value = true
    setTimeout(() => {
      copiedTunnelUrl.value = false
    }, 2000)
  } catch {
    copiedTunnelUrl.value = false
  }
}

watch(
  () => fileTunnel.value?.status,
  (status) => {
    if (status === 'active' || status === 'error') {
      tunnelStarting.value = false
      clearTunnelStartingTimer()
    }
  },
)

watch(
  () => tunnels.lastCreateError,
  (err) => {
    if (err && tunnelStarting.value) {
      tunnelStarting.value = false
      clearTunnelStartingTimer()
      tunnelCreateError.value = err.message
    }
  },
)

watch(
  () => preview.value?.path,
  () => {
    tunnelStarting.value = false
    tunnelCreateError.value = null
    clearTunnelStartingTimer()
  },
)

// Mint the preview capability before loading the iframe.
// Gate iframe rendering on authReady to avoid a capability race.
// Show visible error if capability minting fails.
// Preview auth can only be minted same-origin — the relay refuses
// cross-origin requests rather than reflecting credentialed CORS.
const relayOrigin = computed(() => {
  const base = conn.url.replace(/^ws/, 'http').replace(/\/ws$/, '').replace(/\/$/, '')
  try {
    return new URL(base).origin
  } catch {
    return ''
  }
})
const isCrossOrigin = computed(() => {
  const o = relayOrigin.value
  return !!o && typeof window !== 'undefined' && o !== window.location.origin
})

// rawFileUrl never carries the relay token. Same-origin /file uses the
// HttpOnly session cookie; cross-origin image tags cannot send headers, so
// they only work when the relay is same-origin (or token-less local dev).
const rawFileUrl = computed(() => {
  if (!preview.value?.path) return ''
  return relayFileUrl(conn.url, preview.value.path, preview.value.host)
})

// authReady stays false on any failure, so the iframe is never rendered
// into a known-broken state; the message below explains what to do instead.
watch([() => preview.value?.path, () => preview.value?.host, () => conn.url], async ([path]) => {
  authReady.value = false
  authError.value = ''
  previewCapability.value = ''
  if (!path) return

  if (isCrossOrigin.value) {
    authError.value =
      `Rendered HTML preview needs a same-origin Relay. This Web App is on ${window.location.origin} ` +
      `but the Relay is ${relayOrigin.value}, so the preview capability cannot be minted. ` +
      'Open the Relay URL directly to use rendered preview — or use the "source" toggle above, which works anywhere.'
    return
  }

  try {
    const resp = await fetch(previewAuthUrl.value, {
      credentials: 'include',
      headers: conn.token ? { Authorization: `Bearer ${conn.token}` } : undefined,
    })
    if (!resp.ok && resp.status !== 204) {
      authError.value =
        `Preview auth failed (${resp.status}). The Relay rejected the capability request. ` +
        'Use the "source" toggle above to view the file contents.'
      return
    }
    if (resp.status !== 204) {
      const body = (await resp.json()) as { capability?: unknown }
      if (typeof body.capability !== 'string' || !body.capability) {
        authError.value = 'Preview auth returned no capability. Use the "source" toggle above to view the file contents.'
        return
      }
      previewCapability.value = body.capability
    }
  } catch {
    authError.value =
      'Preview auth unreachable — /preview-auth did not respond. If the Relay was rebuilt, restart it; ' +
      'in dev mode make sure vite proxies /preview and /preview-auth. Use the "source" toggle meanwhile.'
    return
  }
  authReady.value = true
}, { immediate: true })

const imageUrl = computed(() => {
  if (!preview.value?.isImage && !isSvg.value) return ''
  return rawFileUrl.value
})

const iframeRef = ref<HTMLIFrameElement | null>(null)
function reloadIframe() {
  haptic()
  if (iframeRef.value) {
    iframeRef.value.src = previewUrl.value
  }
}

const scroller = ref<HTMLElement | null>(null)
const pathNav = ref<HTMLElement | null>(null)

// Scroll the preview path bar to the right so the file name (the last element)
// is visible rather than the leading directory segments. Double rAF after
// nextTick ensures the browser has finished layout (scrollWidth is stable)
// before we set scrollLeft — a single rAF can fire mid-layout and leave the
// bar a few characters short of the right edge.
async function scrollPathToRight() {
  await nextTick()
  requestAnimationFrame(() => {
    requestAnimationFrame(() => {
      const el = pathNav.value
      if (el) el.scrollLeft = el.scrollWidth
    })
  })
}
// Re-scroll when the preview file changes or when the directory panel
// opens/closes (docked), since that changes the preview panel width.
watch(
  [() => preview.value?.path, () => props.directoryOpen],
  scrollPathToRight,
  { immediate: true },
)

async function scrollToTarget() {
  const p = preview.value
  if (!p || p.targetLine == null || !scroller.value) return
  await nextTick()
  const el = scroller.value.querySelector(`#L${p.targetLine}`) as HTMLElement | null
  if (el) {
    const container = scroller.value
    const containerRect = container.getBoundingClientRect()
    const elRect = el.getBoundingClientRect()
    const relativeTop = elRect.top - containerRect.top + container.scrollTop
    container.scrollTop = Math.max(0, relativeTop - container.clientHeight / 2 + elRect.height / 2)
  }
}
watch(
  [() => preview.value?.content, () => preview.value?.targetLine],
  scrollToTarget,
  { immediate: true },
)

// --- find in file ---
// Searches what is on screen: the syntax-highlighted source, or the rendered
// markdown. A rendered HTML preview lives in a sandboxed iframe we cannot look
// into, so searching it switches to the source view (and back on close).
// Only the loaded text is searched — file_read stops at 1 MiB.
const findOpen = ref(false)
const findQuery = ref('')
const appliedFind = ref('')
const findHits = ref(0)
const findIndex = ref(0)
const findRef = ref<InstanceType<typeof FindBar> | null>(null)
const rootEl = ref<HTMLElement | null>(null)
let findGroups: HTMLElement[][] = []
let findDebounce: ReturnType<typeof setTimeout> | null = null
let pendingJump = false
let htmlSwitched = false

const searchable = computed(() => {
  const p = preview.value
  return !!p && !p.error && !!p.content && !p.isImage
})
const showsMarkdown = computed(() => searchable.value && isMd.value && mdRendered.value)
const showsCode = computed(() => searchable.value && !showsMarkdown.value && !(isHtml.value && htmlRendered.value))

watch(findQuery, (q) => {
  if (findDebounce) clearTimeout(findDebounce)
  if (!q) {
    appliedFind.value = ''
    findHits.value = 0
    return
  }
  findDebounce = setTimeout(() => {
    pendingJump = true
    appliedFind.value = q
  }, 120)
})

function runFind() {
  const root = scroller.value
  if (!root) return
  clearHighlights(root)
  findGroups = []
  const q = findOpen.value ? appliedFind.value : ''
  if (q) {
    if (showsMarkdown.value) {
      const md = root.querySelector<HTMLElement>('.fp-markdown')
      if (md) findGroups = applyHighlights(md, q)
    } else if (showsCode.value) {
      // Only touch the lines that contain the query; the line-number gutter is
      // outside .lcw so it can never match.
      let budget = MAX_HITS
      for (const cell of root.querySelectorAll<HTMLElement>('.lcw')) {
        if (budget <= 0) break
        if (!matchesQuery(cell.textContent ?? '', q)) continue
        const g = applyHighlights(cell, q, budget)
        findGroups.push(...g)
        budget -= g.length
      }
    }
  }
  findHits.value = findGroups.length
  if (!findGroups.length) {
    findIndex.value = 0
    return
  }
  const jump = pendingJump
  pendingJump = false
  findIndex.value = jump ? 0 : Math.min(findIndex.value, findGroups.length - 1)
  const mark = setActiveGroup(findGroups, findIndex.value)
  if (jump) mark?.scrollIntoView?.({ block: 'center', inline: 'nearest' })
}
watch(
  [appliedFind, findOpen, () => preview.value?.content, mdRendered, htmlRendered, lines],
  runFind,
  { flush: 'post' },
)

function openFind() {
  if (!searchable.value) return
  if (findOpen.value) {
    findRef.value?.focus()
    return
  }
  if (isHtml.value && htmlRendered.value) {
    htmlRendered.value = false
    htmlSwitched = true
  }
  findOpen.value = true
  pendingJump = true
  nextTick(() => findRef.value?.focus())
}

function closeFind(restoreHtml = true) {
  if (findDebounce) clearTimeout(findDebounce)
  findOpen.value = false
  findQuery.value = ''
  appliedFind.value = ''
  findHits.value = 0
  findIndex.value = 0
  pendingJump = false
  if (htmlSwitched && restoreHtml) htmlRendered.value = true
  htmlSwitched = false
}

function stepFind(delta: number) {
  const n = findHits.value
  if (!n) return
  findIndex.value = (findIndex.value + delta + n) % n
  setActiveGroup(findGroups, findIndex.value)?.scrollIntoView?.({ block: 'center', inline: 'nearest' })
}

// Showing the rendered HTML again while searching leaves nothing to search.
watch(htmlRendered, (v) => { if (v && findOpen.value) closeFind(false) })
// A different file starts a fresh search.
watch(() => preview.value?.path, () => closeFind(false))



function fmtSize(bytes: number): string {
  if (!bytes) return ''
  if (bytes < 1024) return `${bytes}B`
  if (bytes < 1024 * 1024) return `${(bytes / 1024).toFixed(1)}K`
  return `${(bytes / 1024 / 1024).toFixed(1)}M`
}
</script>

<template>
  <section v-if="preview" ref="rootEl" class="file-preview" aria-label="File preview">
    <header class="fp-head">
      <button class="fp-btn fp-close" aria-label="Close preview" title="Close preview" @click="emit('close')">×</button>
      <nav ref="pathNav" class="fp-path" aria-label="Preview path">
        <template v-for="(dir, index) in previewDirs" :key="dir.path">
          <span v-if="index" class="fp-sep">/</span>
          <button class="fp-dir" :title="`Open ${dir.path} in file browser`" @click="openDirectory(dir.path)">{{ dir.name }}</button>
        </template>
        <span class="fp-sep">/</span>
        <span class="fp-name" :title="preview.path">{{ fileName }}</span>
      </nav>
      <span class="fp-size mono">{{ fmtSize(preview.size) }}</span>
      <button
        class="fp-btn fill-path"
        :title="`Fill file path (${previewRelPath}) into terminal input`"
        aria-label="Fill file path into terminal"
        @click="fillPreviewPath"
      >↙</button>

      <button
        v-if="!preview.isImage || isSvg"
        class="fp-btn copy-btn"
        :class="{ copied: copyStatus === 'copied', error: copyStatus === 'error' }"
        :title="copyTitle"
        :aria-label="copyTitle"
        :disabled="!canCopy"
        @click="copyContent"
      >{{ copyLabel }}</button>

      <button
        v-if="searchable"
        class="fp-btn"
        :class="{ active: findOpen }"
        aria-label="Search file"
        :title="findTitle"
        :aria-pressed="findOpen"
        @click="findOpen ? closeFind() : openFind()"
      >⌕</button>

      <!-- Viewport controls for HTML preview -->
      <template v-if="isHtml && htmlRendered">
        <a
          v-if="previewUrl"
          :href="previewUrl"
          target="_blank"
          rel="noopener noreferrer"
          class="fp-btn"
          title="Open HTML in a new tab"
          aria-label="Open HTML in a new tab"
        >↗</a>
        <button
          v-if="conn.hasFeature('tunnels')"
          class="fp-btn"
          :class="{ active: fileTunnel?.status === 'active' }"
          :title="fileTunnel?.status === 'active' ? 'Show tunnel preview' : 'Publish a public tunnel preview'"
          aria-label="Publish a public tunnel preview"
          @click="startTunnelPreview"
        >⚡</button>
        <button
          class="fp-btn vp-btn"
          :class="{ active: viewportWidth === '375px' }"
          title="Mobile Viewport (375px)"
          @click="viewportWidth = '375px'"
        >📱</button>
        <button
          class="fp-btn vp-btn"
          :class="{ active: viewportWidth === '768px' }"
          title="Tablet Viewport (768px)"
          @click="viewportWidth = '768px'"
        >📱 768</button>
        <button
          class="fp-btn vp-btn"
          :class="{ active: viewportWidth === '100%' }"
          title="Full Viewport (100%)"
          @click="viewportWidth = '100%'"
        >💻</button>
        <button
          class="fp-btn"
          title="Reload Preview"
          aria-label="Reload Preview"
          @click="reloadIframe"
        >↻</button>
      </template>

      <button
        v-if="isMd && !preview.isImage"
        class="fp-btn mode"
        :aria-label="mdRendered ? 'Show source' : 'Show rendered'"
        @click="mdRendered = !mdRendered"
      >{{ mdRendered ? 'text' : 'rendered' }}</button>

      <button
        v-if="isHtml"
        class="fp-btn mode"
        :aria-label="htmlRendered ? 'Show source' : 'Show preview'"
        @click="htmlRendered = !htmlRendered"
      >{{ htmlRendered ? 'source' : 'preview' }}</button>

      <button
        class="fp-btn directory-toggle"
        :class="{ active: props.directoryOpen }"
        :aria-label="props.directoryOpen ? 'Hide file browser' : 'Show file browser'"
        :title="props.directoryOpen ? 'Hide directory' : 'Show directory'"
        :aria-pressed="props.directoryOpen"
        @click="toggleDirectory"
      >🗂</button>

    </header>

    <div class="fp-main">
    <div ref="scroller" class="fp-scroll" :class="{ 'fp-html-wrap': isHtml && htmlRendered }">
      <div v-if="preview.error" class="fp-error">
        <span class="fp-error-icon">⚠</span>
        <span class="fp-error-text">{{ preview.error }}</span>
        <span class="fp-error-path mono">{{ preview.path }}</span>
      </div>

      <div v-else-if="!preview.content && !preview.isImage && !isHtml" class="fp-loading">reading…</div>

      <!-- Rendered preview unavailable — explain and offer the fallback
           rather than loading an iframe that is guaranteed to 401. -->
      <div v-else-if="isHtml && htmlRendered && authError" class="fp-auth-error">
        <div class="fp-auth-error-text">{{ authError }}</div>
        <button class="fp-btn mode" @click="htmlRendered = false">Show source</button>
      </div>

      <!-- HTML Sandboxed Iframe Preview -->
      <div v-else-if="isHtml && htmlRendered" class="fp-iframe-container">
        <!-- Sandbox tip & Tunnel banner -->
        <div class="fp-sandbox-tip" :class="{ 'has-scripts': hasScripts }">
          <div v-if="hasScripts" class="fp-tip-msg">
            <span class="fp-tip-icon">ℹ</span>
            <span class="fp-tip-text">
              Preview is sandboxed (JavaScript disabled). Start a tunnel preview to run scripts.
            </span>
          </div>
          <div class="fp-tip-actions">
            <a v-if="previewUrl" :href="previewUrl" target="_blank" rel="noopener noreferrer" class="fp-tip-btn">Open in new tab ↗</a>
            <template v-if="conn.hasFeature('tunnels')">
              <div v-if="tunnelCreateError" class="fp-tunnel-status">
                <span class="fp-tunnel-badge error" :title="tunnelCreateError">Failed to start: {{ tunnelCreateError }}</span>
                <button class="fp-tip-btn" @click="startTunnelPreview">Retry</button>
                <button class="fp-tip-btn danger" @click="dismissTunnelError">Dismiss</button>
              </div>
              <button
                v-else-if="!fileTunnel || fileTunnel.status === 'closed'"
                class="fp-tip-btn primary"
                :disabled="tunnelStarting"
                @click="startTunnelPreview"
              >
                {{ tunnelStarting ? 'Starting…' : '⚡ Public tunnel preview' }}
              </button>
              <div v-else class="fp-tunnel-status">
                <span v-if="fileTunnel.status === 'starting'" class="fp-tunnel-badge starting">Starting tunnel…</span>
                <template v-else-if="fileTunnel.status === 'error'">
                  <span class="fp-tunnel-badge error" :title="fileTunnel.error">Failed to start: {{ fileTunnel.error }}</span>
                  <button class="fp-tip-btn" @click="startTunnelPreview">Retry</button>
                  <button class="fp-tip-btn danger" @click="dismissTunnelError">Dismiss</button>
                </template>
                <template v-else-if="fileTunnel.status === 'active'">
                  <span class="fp-tunnel-badge active">Public URL ready</span>
                  <a :href="fullTunnelUrl" target="_blank" rel="noopener noreferrer" class="fp-tip-btn primary">Open public URL ↗</a>
                  <button class="fp-tip-btn" title="Copy public URL" @click="copyTunnelUrl">{{ copiedTunnelUrl ? 'Copied' : 'Copy link' }}</button>
                  <button class="fp-tip-btn danger" title="Close tunnel" @click="tunnels.closeTunnel(fileTunnel.id)">Close</button>
                </template>
              </div>
            </template>
          </div>
        </div>

        <iframe
          v-if="authReady"
          ref="iframeRef"
          class="fp-iframe"
          :style="{ width: viewportWidth }"
          :src="previewUrl"
          sandbox=""
          title="HTML Preview"
        ></iframe>
        <div v-else class="fp-loading">preparing preview…</div>
      </div>

      <img v-else-if="preview.isImage && !isHtml" class="fp-image" :src="imageUrl" :alt="fileName" />

      <div v-else-if="isMd && mdRendered" class="fp-markdown" v-html="renderedMd"></div>

      <table v-else class="fp-code" :class="codeWidthClass">
        <tbody>
          <tr
            v-for="(line, i) in lines"
            :key="i"
            :id="`L${i + 1}`"
            :class="{ target: preview.targetLine === i + 1 }"
          >
            <td class="ln">{{ i + 1 }}</td>
            <td class="lc"><div class="lcw" :style="codeCellStyle" v-html="line || '&nbsp;'"></div></td>
          </tr>
        </tbody>
      </table>
    </div>
    <FindBar
      v-if="findOpen"
      ref="findRef"
      v-model="findQuery"
      label="file"
      :current="findHits ? findIndex + 1 : 0"
      :total="findHits"
      :capped="findHits >= MAX_HITS"
      :note="preview.truncated ? 'File truncated: only the loaded part is searched' : ''"
      @next="stepFind(1)"
      @prev="stepFind(-1)"
      @close="closeFind()"
    />
    </div>
  </section>
</template>

<style scoped>
.mono { font-family: var(--font-mono); }
.file-preview { display: flex; flex-direction: column; flex: 1; min-width: 0; min-height: 0; }
.fp-head {
  display: flex; align-items: center; gap: 8px;
  padding: 10px 12px; border-bottom: 1px solid var(--border); background: var(--surface); flex-shrink: 0;
}
.fp-path { flex: 1; min-width: 0; display: flex; align-items: center; overflow-x: auto; overflow-y: hidden; overscroll-behavior-x: contain; font-family: var(--font-mono); font-size: var(--text-xs); white-space: nowrap; scrollbar-width: none; }
.fp-path::-webkit-scrollbar { display: none; }
.fp-name { flex-shrink: 0; font-size: var(--text-sm); font-weight: 600; color: var(--text); }
.fp-dir { flex-shrink: 0; color: var(--blue); background: none; border: 0; padding: 3px 4px; border-radius: 3px; }
.fp-dir:hover { background: var(--blue-soft); }
.fp-sep { flex-shrink: 0; color: var(--faint); }
.fp-size { font-size: var(--text-2xs); color: var(--faint); flex-shrink: 0; }
.fp-btn {
  font-family: var(--font-mono); font-size: var(--text-2xs); color: var(--muted);
  background: var(--surface-2); border: 1px solid var(--border); border-radius: var(--radius);
  padding: 4px 9px; flex-shrink: 0;
}
.fp-btn:hover { color: var(--text); }
.fp-btn:disabled { opacity: 0.45; cursor: not-allowed; }
.fp-btn.directory-toggle.active { color: var(--blue); border-color: var(--blue); background: var(--blue-soft); }
.fp-close { width: 30px; height: 30px; padding: 0; font-size: var(--text-md); }
.fp-btn.mode { color: var(--blue); }
.fp-btn.vp-btn { padding: 4px 7px; }
.fp-btn.vp-btn.active { color: var(--blue); border-color: var(--blue); background: var(--surface-3, var(--surface)); }
.fp-btn.fill-path {
  color: var(--blue);
  border-color: var(--border-strong, var(--border));
}
.fp-btn.fill-path:hover {
  background: var(--surface-3, var(--surface));
}
.fp-btn.copy-btn {
  color: var(--blue);
  border-color: var(--border-strong, var(--border));
}
.fp-btn.copy-btn:hover:not(:disabled) {
  background: var(--surface-3, var(--surface));
}
.fp-btn.copy-btn.copied {
  color: var(--green);
  border-color: var(--green);
  background: var(--green-soft);
}
.fp-btn.copy-btn.error {
  color: var(--orange);
  border-color: var(--orange);
  background: var(--orange-soft);
}

.fp-main { position: relative; flex: 1; min-height: 0; display: flex; flex-direction: column; }
.fp-btn.active { color: var(--blue); border-color: var(--blue); background: var(--blue-soft); }
.fp-scroll { flex: 1; min-height: 0; overflow: auto; background: var(--term-bg); }
.fp-scroll.fp-html-wrap { display: flex; flex-direction: column; overflow: hidden; background: #1e1e1e; }
.fp-iframe-container { flex: 1; min-height: 0; width: 100%; display: flex; flex-direction: column; background: #121212; }
.fp-sandbox-tip {
  padding: 8px 12px;
  background: var(--surface-2, #1c1d22);
  border-bottom: 1px solid var(--border, #2d3139);
  display: flex;
  flex-wrap: wrap;
  align-items: center;
  justify-content: space-between;
  gap: 8px;
  font-size: var(--text-xs, 12px);
  color: var(--text, #e2e8f0);
}
.fp-sandbox-tip:not(.has-scripts) {
  justify-content: flex-end;
  padding: 4px 12px;
}
.fp-tip-msg {
  display: flex;
  align-items: center;
  gap: 6px;
  flex: 1;
  min-width: 240px;
}
.fp-tip-icon {
  color: var(--amber, #f59e0b);
  font-weight: 700;
  flex-shrink: 0;
}
.fp-tip-text {
  color: var(--muted, #94a3b8);
  line-height: 1.4;
}
.fp-tip-actions {
  display: flex;
  align-items: center;
  gap: 6px;
  flex-wrap: wrap;
}
.fp-tip-btn {
  display: inline-flex;
  align-items: center;
  gap: 4px;
  padding: 3px 8px;
  border-radius: var(--radius, 4px);
  border: 1px solid var(--border-strong, #3b4252);
  background: var(--surface, #242933);
  color: var(--text, #e2e8f0);
  font-size: var(--text-xs, 11px);
  cursor: pointer;
  text-decoration: none;
  white-space: nowrap;
  transition: background-color 0.15s, border-color 0.15s, color 0.15s;
}
.fp-tip-btn:hover:not(:disabled) {
  background: var(--surface-2, #2e3440);
  border-color: var(--border-strong, #3b4252);
  color: var(--text, #e2e8f0);
}
.fp-tip-btn.primary {
  background: var(--blue-soft);
  border-color: color-mix(in srgb, var(--blue) 40%, transparent);
  color: var(--blue);
  font-weight: 600;
}
.fp-tip-btn.primary:hover:not(:disabled) {
  background: color-mix(in srgb, var(--blue) 22%, transparent);
  border-color: var(--blue);
  color: var(--blue);
}
.fp-tip-btn.danger {
  color: var(--red);
  background: var(--red-soft, rgba(239, 68, 68, 0.12));
  border-color: color-mix(in srgb, var(--red) 35%, transparent);
}
.fp-tip-btn.danger:hover:not(:disabled) {
  background: color-mix(in srgb, var(--red) 22%, transparent);
  border-color: var(--red);
  color: var(--red);
}
.fp-tunnel-status {
  display: inline-flex;
  align-items: center;
  gap: 6px;
  flex-wrap: wrap;
}
.fp-tunnel-badge {
  font-size: 10px;
  padding: 2px 6px;
  border-radius: 9999px;
  font-weight: 600;
  text-transform: uppercase;
}
.fp-tunnel-badge.active {
  background: rgba(34, 197, 94, 0.15);
  color: var(--green, #22c55e);
  border: 1px solid rgba(34, 197, 94, 0.3);
}
.fp-tunnel-badge.starting {
  background: rgba(234, 179, 8, 0.15);
  color: var(--amber, #eab308);
  border: 1px solid rgba(234, 179, 8, 0.3);
}
.fp-tunnel-badge.error {
  background: rgba(239, 68, 68, 0.15);
  color: var(--red, #ef4444);
  border: 1px solid rgba(239, 68, 68, 0.3);
}
.fp-iframe {
  flex: 1;
  min-height: 0;
  margin: 0 auto;
  border: none;
  background: #ffffff;
  transition: width 0.2s ease;
  box-shadow: 0 0 20px rgba(0,0,0,0.5);
}
.fp-auth-error {
  padding: 16px; color: #e88; font-family: var(--font-mono); font-size: var(--text-xs);
  background: #2a1810; border-bottom: 1px solid #4a2820; line-height: 1.6;
  display: flex; flex-direction: column; align-items: flex-start; gap: 12px;
}
.fp-auth-error-text { max-width: 70ch; }
.fp-loading { padding: 30px; text-align: center; color: var(--faint); font-family: var(--font-mono); font-size: var(--text-xs); }
.fp-error { padding: 40px 24px; text-align: center; display: flex; flex-direction: column; align-items: center; gap: 8px; }
.fp-error-icon { font-size: 28px; color: var(--orange); }
.fp-error-text { color: var(--muted); font-family: var(--font-mono); font-size: var(--text-sm); }
.fp-error-path { color: var(--faint); font-size: var(--text-2xs); word-break: break-all; max-width: 100%; }
.fp-image { display: block; max-width: 100%; margin: 16px auto; border-radius: var(--radius); }

/* code with line numbers */
.fp-code { width: 100%; border-collapse: collapse; font-family: var(--font-mono); font-size: 11.5px; line-height: 1.6; }
.fp-code td { padding: 0; vertical-align: top; }
.fp-code .ln {
  width: 1%; min-width: 42px; padding: 0 10px 0 14px; text-align: right;
  color: var(--faint); user-select: none; background: var(--term-bg);
  position: sticky; left: 0;
}
.fp-code .lc { padding: 0 14px 0 12px; white-space: pre-wrap; word-break: break-word; color: var(--term-fg); }
.fp-code tr.target .lc { background: rgba(251, 191, 36, 0.12); }
.fp-code tr.target .ln { color: var(--amber); }

/* Preview width setting: the fixed char column lives on .lcw, an inner block
   inside the cell — a td's own width is only a floor in auto table layout.
   The constrained block overflows the panel → .fp-scroll scrolls sideways.
   w-none keeps every real line on one visual line. */
.fp-code.w-none .lc { white-space: pre; overflow-wrap: normal; word-break: normal; }

/* syntax tokens — highlight.js theme (dark, VS Code-like) */
:deep(.hljs) { color: var(--term-fg); }
:deep(.hljs-comment), :deep(.hljs-quote) { color: #6a9955; font-style: italic; }
:deep(.hljs-keyword), :deep(.hljs-selector-tag), :deep(.hljs-literal), :deep(.hljs-section), :deep(.hljs-link) { color: #c586c0; }
:deep(.hljs-built_in), :deep(.hljs-type), :deep(.hljs-class .hljs-title) { color: #4ec9b0; }
:deep(.hljs-string), :deep(.hljs-attr), :deep(.hljs-symbol), :deep(.hljs-bullet), :deep(.hljs-addition) { color: #ce9178; }
:deep(.hljs-number), :deep(.hljs-meta), :deep(.hljs-link) { color: #b5cea8; }
:deep(.hljs-title), :deep(.hljs-name) { color: #569cd6; }
:deep(.hljs-attribute), :deep(.hljs-attr), :deep(.hljs-variable), :deep(.hljs-template-variable) { color: #9cdcfe; }
:deep(.hljs-regexp), :deep(.hljs-deletion) { color: #d16969; }
:deep(.hljs-emphasis) { font-style: italic; }
:deep(.hljs-strong) { font-weight: bold; }
:deep(.hljs-tag) { color: #569cd6; }
:deep(.hljs-params) { color: var(--term-fg); }

/* markdown */
.fp-markdown { padding: 18px 20px; color: var(--text); line-height: 1.7; font-size: var(--text-sm); }
.fp-markdown :deep(h1), .fp-markdown :deep(h2), .fp-markdown :deep(h3) { margin: 0.9em 0 0.4em; line-height: 1.3; }
.fp-markdown :deep(h1) { font-size: 1.5em; }
.fp-markdown :deep(h2) { font-size: 1.25em; }
.fp-markdown :deep(h3) { font-size: 1.1em; }
.fp-markdown :deep(p) { margin: 0.5em 0; }
.fp-markdown :deep(code) { font-family: var(--font-mono); font-size: 0.88em; background: var(--surface-2); border: 1px solid var(--border); border-radius: 4px; padding: 1px 5px; }
.fp-markdown :deep(.md-code) { background: var(--bg-deep); border: 1px solid var(--border); border-radius: var(--radius); padding: 12px 14px; overflow-x: auto; margin: 0.7em 0; }
.fp-markdown :deep(.md-code code) { background: none; border: none; padding: 0; }
.fp-markdown :deep(a) { color: var(--blue); }
.fp-markdown :deep(.md-table), .fp-markdown :deep(.md-frontmatter) { border-collapse: collapse; margin: 0.7em 0; width: 100%; }
.fp-markdown :deep(th), .fp-markdown :deep(td) { border: 1px solid var(--border); padding: 6px 10px; text-align: left; }
.fp-markdown :deep(th) { background: var(--surface-2); }
.fp-markdown :deep(.md-frontmatter .fm-k) { font-family: var(--font-mono); color: var(--muted); width: 30%; }
.fp-markdown :deep(.md-hr) { border: none; border-top: 1px solid var(--border); margin: 1em 0; }
.fp-markdown :deep(.md-task) { list-style: none; padding-left: 4px; }
.fp-markdown :deep(.md-task-item.done) { color: var(--muted); text-decoration: line-through; }
.fp-markdown :deep(.md-img) { max-width: 100%; border-radius: var(--radius); }
.fp-markdown :deep(ul), .fp-markdown :deep(ol) { padding-left: 1.4em; margin: 0.4em 0; }

/* Desktop: larger preview text */
@media (min-width: 900px) {
  .fp-code { font-size: 13px; }
  .fp-markdown { font-size: 14px; }
}
/* Wide screens: bump preview font +2px for readability */
@media (min-width: 1800px) {
  .fp-code { font-size: 15px; }
  .fp-markdown { font-size: 16px; }
}
</style>
