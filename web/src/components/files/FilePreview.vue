<script setup lang="ts">
import { computed, nextTick, ref, watch } from 'vue'
import { useFilesStore } from '../../stores/files'
import { useConnectionStore } from '../../stores/connection'
import { useTerminalStore } from '../../stores/terminal'
import { useHaptic } from '../../composables/useHaptic'
import { renderMarkdown } from '../../utils/markdown'
import { highlight, langForPath } from '../../utils/highlight'

const files = useFilesStore()
const conn = useConnectionStore()
const term = useTerminalStore()
const { haptic } = useHaptic()

const preview = computed(() => files.preview)
const isMd = computed(() => /\.(md|markdown)$/i.test(preview.value?.path || ''))
const isHtml = computed(() => /\.(html|htm)$/i.test(preview.value?.path || ''))
const isSvg = computed(() => /\.svg$/i.test(preview.value?.path || ''))

const fileName = computed(() => {
  const full = preview.value?.path || ''
  const root = files.root
  // Show relative path if under root, otherwise just basename
  const rel = full.startsWith(root + '/') ? full.slice(root.length + 1) : full
  return rel.split('/').pop() || ''
})

const previewRelPath = computed(() => {
  const full = preview.value?.path || ''
  const root = files.root.replace(/\/+$/, '')
  if (!full) return ''
  if (full.startsWith(root + '/')) return full.slice(root.length + 1)
  return full
})

function fillPreviewPath() {
  haptic()
  if (previewRelPath.value) {
    term.fillPath(previewRelPath.value)
  }
}

const lang = computed(() => langForPath(preview.value?.path || ''))

// markdown & html preview modes
const mdRendered = ref(true)
const htmlRendered = ref(true)
const viewportWidth = ref<'100%' | '768px' | '375px'>('100%')
const authReady = ref(false)
const authError = ref('')

watch(preview, (p) => {
  mdRendered.value = !(p && p.targetLine != null && isMd.value)
  htmlRendered.value = true
})

const lines = computed(() => {
  if (!preview.value || (preview.value.isImage && !isSvg.value)) return []
  return highlight(preview.value.content, lang.value)
})

const renderedMd = computed(() => {
  if (!preview.value || !isMd.value || !mdRendered.value) return ''
  return renderMarkdown(preview.value.content)
})

const rawFileUrl = computed(() => {
  if (!preview.value?.path) return ''
  const base = conn.url.replace(/^ws/, 'http').replace(/\/ws$/, '').replace(/\/$/, '')
  const token = conn.token ? `&token=${encodeURIComponent(conn.token)}` : ''
  const host = files.host !== 'local' ? `&host=${encodeURIComponent(files.host)}` : ''
  return `${base}/file?path=${encodeURIComponent(preview.value.path)}${host}${token}`
})

// previewUrl uses a path-based URL (/preview/<path>) so that relative URLs in
// HTML documents (CSS, JS, images) resolve against the file's directory.
// No token in URL — auth is via /preview/ scoped cookie set by previewAuthUrl.
const previewUrl = computed(() => {
  if (!preview.value?.path) return ''
  const base = conn.url.replace(/^ws/, 'http').replace(/\/ws$/, '').replace(/\/$/, '')
  const encodedHost = btoa(files.host || 'local').replace(/\+/g, '-').replace(/\//g, '_').replace(/=+$/, '')
  const encodedPath = preview.value.path.split('/').map(encodeURIComponent).join('/')
  return `${base}/preview/${encodedHost}/${encodedPath}`
})

// previewAuthUrl sets the /preview/ auth cookie via a same-origin fetch.
// Called before loading the iframe so the cookie is available for relative
// resource requests, without putting the token in the iframe URL (F1).
const previewAuthUrl = computed(() => {
  const base = conn.url.replace(/^ws/, 'http').replace(/\/ws$/, '').replace(/\/$/, '')
  const token = conn.token ? `?token=${encodeURIComponent(conn.token)}` : ''
  return `${base}/preview-auth${token}`
})

// Set preview auth cookie before loading iframe (F1: keep token out of iframe URL).
// N1: Gate iframe rendering on authReady to avoid race with cookie landing.
// N2: Show visible error if cookie prefetch fails (cross-origin, HTTP LAN, dev mode).
// F4: The /preview/ cookie can only be set same-origin — the relay refuses
// cross-origin preview auth rather than reflecting credentialed CORS. Detect
// that up front so we can explain it instead of loading an iframe that is
// guaranteed to 401.
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

// Set preview auth cookie before loading iframe (F1: keep token out of iframe URL).
// N1: Gate iframe rendering on authReady to avoid race with cookie landing.
// F4: authReady stays false on any failure, so the iframe is never rendered
// into a known-broken state; the message below explains what to do instead.
watch(previewUrl, async (url) => {
  authReady.value = false
  authError.value = ''
  if (!url) return

  if (isCrossOrigin.value) {
    authError.value =
      `Rendered HTML preview needs a same-origin Relay. This Web App is on ${window.location.origin} ` +
      `but the Relay is ${relayOrigin.value}, so the preview cookie cannot be set. ` +
      'Open the Relay URL directly to use rendered preview — or use the "source" toggle above, which works anywhere.'
    return
  }

  try {
    const resp = await fetch(previewAuthUrl.value, { credentials: 'include' })
    if (!resp.ok && resp.status !== 204) {
      authError.value =
        `Preview auth failed (${resp.status}). The Relay rejected the preview cookie request. ` +
        'Use the "source" toggle above to view the file contents.'
      return
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

function fmtSize(bytes: number): string {
  if (!bytes) return ''
  if (bytes < 1024) return `${bytes}B`
  if (bytes < 1024 * 1024) return `${(bytes / 1024).toFixed(1)}K`
  return `${(bytes / 1024 / 1024).toFixed(1)}M`
}
</script>

<template>
  <div v-if="preview" class="file-preview" :class="{ max: files.previewMax }">
    <header class="fp-head">
      <span class="fp-name" :title="preview.path">{{ fileName }}</span>
      <span class="fp-size mono">{{ fmtSize(preview.size) }}</span>
      <button
        class="fp-btn fill-path"
        :title="`Fill file path (${previewRelPath}) into terminal input`"
        aria-label="Fill file path into terminal"
        @click="fillPreviewPath"
      >↙ term</button>

      <!-- Viewport controls for HTML preview -->
      <template v-if="isHtml && htmlRendered">
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
        class="fp-btn"
        :aria-label="files.previewMax ? 'Show file tree' : 'Hide file tree'"
        :title="files.previewMax ? 'Show file tree' : 'Hide file tree'"
        @click="files.previewMax ? files.showTree() : files.toggleMax()"
      >☰</button>
      <button
        v-if="!files.fullMax"
        class="fp-btn"
        aria-label="Maximize"
        title="Maximize (full width)"
        @click="files.toggleFullMax()"
      >⤢</button>
      <button
        v-else
        class="fp-btn"
        aria-label="Restore"
        title="Restore width"
        @click="files.toggleFullMax()"
      >⤡</button>
    </header>

    <div ref="scroller" class="fp-scroll" :class="{ 'fp-html-wrap': isHtml && htmlRendered }">
      <div v-if="preview.error" class="fp-error">
        <span class="fp-error-icon">⚠</span>
        <span class="fp-error-text">{{ preview.error }}</span>
        <span class="fp-error-path mono">{{ preview.path }}</span>
      </div>

      <div v-else-if="!preview.content && !preview.isImage && !isHtml" class="fp-loading">reading…</div>

      <!-- F4: rendered preview unavailable — explain and offer the fallback
           rather than loading an iframe that is guaranteed to 401. -->
      <div v-else-if="isHtml && htmlRendered && authError" class="fp-auth-error">
        <div class="fp-auth-error-text">{{ authError }}</div>
        <button class="fp-btn mode" @click="htmlRendered = false">Show source</button>
      </div>

      <!-- HTML Sandboxed Iframe Preview -->
      <div v-else-if="isHtml && htmlRendered" class="fp-iframe-container">
        <iframe
          v-if="authReady"
          ref="iframeRef"
          class="fp-iframe"
          :style="{ width: viewportWidth }"
          :src="previewUrl"
          sandbox="allow-scripts allow-forms allow-popups"
          title="HTML Preview"
        ></iframe>
        <div v-else class="fp-loading">preparing preview…</div>
      </div>

      <img v-else-if="preview.isImage && !isHtml" class="fp-image" :src="imageUrl" :alt="fileName" />

      <div v-else-if="isMd && mdRendered" class="fp-markdown" v-html="renderedMd"></div>

      <table v-else class="fp-code">
        <tbody>
          <tr
            v-for="(line, i) in lines"
            :key="i"
            :id="`L${i + 1}`"
            :class="{ target: preview.targetLine === i + 1 }"
          >
            <td class="ln">{{ i + 1 }}</td>
            <td class="lc" v-html="line || '&nbsp;'"></td>
          </tr>
        </tbody>
      </table>
    </div>
  </div>
</template>

<style scoped>
.mono { font-family: var(--font-mono); }
.file-preview { display: flex; flex-direction: column; flex: 1; min-width: 0; min-height: 0; }
.fp-head {
  display: flex; align-items: center; gap: 8px;
  padding: 9px 12px; border-bottom: 1px solid var(--border); background: var(--surface); flex-shrink: 0;
}
.fp-name { flex: 1; min-width: 0; font-family: var(--font-mono); font-size: var(--text-sm); font-weight: 600; color: var(--text); white-space: nowrap; overflow: hidden; text-overflow: ellipsis; }
.fp-size { font-size: var(--text-2xs); color: var(--faint); flex-shrink: 0; }
.fp-btn {
  font-family: var(--font-mono); font-size: var(--text-2xs); color: var(--muted);
  background: var(--surface-2); border: 1px solid var(--border); border-radius: var(--radius);
  padding: 4px 9px; flex-shrink: 0;
}
.fp-btn:hover { color: var(--text); }
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

.fp-scroll { flex: 1; min-height: 0; overflow: auto; background: var(--term-bg); }
.fp-scroll.fp-html-wrap { display: flex; flex-direction: column; overflow: hidden; background: #1e1e1e; }
.fp-iframe-container { flex: 1; min-height: 0; width: 100%; display: flex; flex-direction: column; justify-content: center; background: #121212; }
.fp-auth-error {
  padding: 16px; color: #e88; font-family: var(--font-mono); font-size: var(--text-xs);
  background: #2a1810; border-bottom: 1px solid #4a2820; line-height: 1.6;
  display: flex; flex-direction: column; align-items: flex-start; gap: 12px;
}
.fp-auth-error-text { max-width: 70ch; }
.fp-iframe { height: 100%; border: none; background: #ffffff; transition: width 0.2s ease; box-shadow: 0 0 20px rgba(0,0,0,0.5); }
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
