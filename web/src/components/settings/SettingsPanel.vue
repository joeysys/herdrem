<script setup lang="ts">
import { ref } from 'vue'
import { useConnectionStore } from '../../stores/connection'
import { useTerminalStore } from '../../stores/terminal'
import RelayManager from './RelayManager.vue'
import PreferencesSection from './PreferencesSection.vue'
import { useBackground } from '../../composables/useBackground'
import { useNotify } from '../../composables/useNotify'
import { buildId } from 'virtual:build-id'

const conn = useConnectionStore()
const term = useTerminalStore()
const props = defineProps<{ focusSection?: 'shell_cmds' | 'skills' | 'agent_cmds' | null }>()
const emit = defineEmits<{ close: []; switched: [] }>()

const { bgImage, bgName, setBgUrl, setBgFile, clearBg } = useBackground()
const notify = useNotify()
const bgUrlInput = ref(bgImage.value.startsWith('data:') ? '' : bgImage.value)
const fileInput = ref<HTMLInputElement | null>(null)

function onSwitched() {
  emit('switched')
}

function applyBgUrl() {
  const url = bgUrlInput.value.trim()
  if (!url) return
  setBgUrl(url)
  notify.push({ message: 'background set', type: 'info' })
}

async function onFileChange(e: Event) {
  const target = e.target as HTMLInputElement
  const file = target.files?.[0]
  if (file) {
    await setBgFile(file)
    bgUrlInput.value = ''
    if (bgImage.value) {
      notify.push({ message: `background: ${file.name}`, type: 'info' })
    } else {
      notify.push({ message: 'image too large to store, try a smaller image', type: 'info' })
    }
  }
  target.value = ''
}

function doClear() {
  clearBg()
  bgUrlInput.value = ''
  notify.push({ message: 'background cleared', type: 'info' })
}

async function reloadPage() {
  // Clear the terminal content cache (all panes) so a stale cached snapshot
  // cannot survive the reload (global clear-cache action).
  term.clearAllCache()
  // Clear all SW caches and unregister, then hard reload
  try {
    if ('caches' in window) {
      const keys = await caches.keys()
      await Promise.all(keys.map((k) => caches.delete(k)))
    }
    if ('serviceWorker' in navigator) {
      const regs = await navigator.serviceWorker.getRegistrations()
      await Promise.all(regs.map((r) => r.unregister()))
    }
  } catch { /* noop */ }
  location.reload()
}
</script>

<template>
  <div class="settings-backdrop" @click.self="emit('close')">
    <aside class="settings-panel view-enter" role="dialog" aria-label="Settings">
      <header class="sp-head">
        <h2>settings</h2>
        <button class="sp-close" aria-label="Close settings" title="Close" @click="emit('close')">×</button>
      </header>

      <div class="sp-body">
        <section class="sp-section">
          <RelayManager @switched="onSwitched" />
        </section>

        <section class="sp-section">
          <PreferencesSection :focus-section="props.focusSection" />
        </section>

        <section class="sp-section">
          <h3>background</h3>
          <div class="bg-current" v-if="bgImage">
            <span class="bg-label" :title="bgName">{{ bgName }}</span>
            <button class="bg-clear" @click="doClear">clear</button>
          </div>
          <div class="bg-url-row">
            <input
              v-model="bgUrlInput"
              class="bg-url-input"
              type="text"
              placeholder="image URL (https://…)"
              @keydown.enter="applyBgUrl"
            />
            <button class="bg-apply" @click="applyBgUrl">set</button>
          </div>
          <div class="bg-file-row">
            <button class="bg-upload" @click="fileInput?.click()">upload file</button>
            <input
              ref="fileInput"
              type="file"
              accept="image/*"
              class="bg-file-hidden"
              @change="onFileChange"
            />
          </div>
          <p class="bg-hint">stored locally on this device only</p>
        </section>

        <section class="sp-section about">
          <h3>about</h3>
          <div class="about-row">
            <span class="k">server</span>
            <span class="v mono">{{ conn.serverVersion || '—' }}</span>
          </div>
          <div class="about-row">
            <span class="k">features</span>
            <span class="v mono">{{ conn.features.join(' ') || '—' }}</span>
          </div>
          <div class="about-row">
            <span class="k">status</span>
            <span class="v mono" :data-status="conn.status">{{ conn.status }}</span>
          </div>
          <div class="about-row">
            <span class="k">build</span>
            <span class="v mono">{{ buildId }}</span>
          </div>
        </section>

        <section class="sp-section">
          <button class="sp-reload" @click="reloadPage">⟳ clear cache & reload</button>
        </section>
      </div>
    </aside>
  </div>
</template>

<style scoped>
.settings-backdrop {
  position: fixed;
  inset: 0;
  z-index: 80;
  background: rgba(4, 6, 10, 0.55);
  backdrop-filter: blur(2px);
  display: flex;
  justify-content: flex-end;
}
.settings-panel {
  width: min(420px, 100%);
  height: 100%;
  background: var(--bg);
  border-left: 1px solid var(--border);
  box-shadow: var(--shadow-2);
  display: flex;
  flex-direction: column;
}
.sp-head {
  display: flex; align-items: center; gap: 8px;
  padding: 16px 18px; border-bottom: 1px solid var(--border); flex-shrink: 0;
}
.sp-head h2 {
  flex: 1;
  font-size: var(--text-md); font-weight: 700;
  text-transform: uppercase; letter-spacing: 0.14em; color: var(--text-dim);
}
.sp-close {
  width: 32px; height: 32px; display: grid; place-items: center;
  font-size: var(--text-xl); color: var(--muted);
  background: var(--surface-2); border: 1px solid var(--border); border-radius: var(--radius);
}
.sp-close:hover { color: var(--text); }
.sp-reload {
  width: 100%;
  font-family: var(--font-mono); font-size: var(--text-xs); font-weight: 700;
  color: var(--text-dim); background: var(--surface-2);
  border: 1px solid var(--border); border-radius: var(--radius);
  padding: 10px 16px; transition: color 0.15s, border-color 0.15s;
}
.sp-reload:hover { color: var(--text); border-color: var(--border-strong); }
.sp-body { flex: 1; overflow-y: auto; padding: 18px; }
.sp-section { margin-bottom: 26px; }
.sp-section.about h3 {
  font-size: var(--text-sm); font-weight: 700;
  text-transform: uppercase; letter-spacing: 0.12em; color: var(--muted); margin-bottom: 10px;
}
.about-row {
  display: flex; align-items: baseline; gap: 12px; padding: 6px 0;
  border-bottom: 1px solid var(--border);
}
.about-row .k { font-family: var(--font-mono); font-size: var(--text-2xs); text-transform: uppercase; letter-spacing: 0.1em; color: var(--faint); width: 72px; flex-shrink: 0; }
.about-row .v { font-size: var(--text-sm); color: var(--text-dim); word-break: break-all; }
.about-row .v[data-status="connected"] { color: var(--green); }
.about-row .v[data-status="connecting"] { color: var(--orange); }
.about-row .v[data-status="disconnected"] { color: var(--red); }
.mono { font-family: var(--font-mono); }

/* Background section */
.sp-section h3 {
  font-size: var(--text-sm); font-weight: 700;
  text-transform: uppercase; letter-spacing: 0.12em; color: var(--muted); margin-bottom: 10px;
}
.bg-current {
  display: flex; align-items: center; justify-content: space-between;
  margin-bottom: 10px; padding: 6px 10px;
  background: var(--surface-2); border: 1px solid var(--border); border-radius: var(--radius);
}
.bg-label {
  font-family: var(--font-mono); font-size: var(--text-2xs); color: var(--text-dim);
  overflow: hidden; text-overflow: ellipsis; white-space: nowrap; flex: 1; min-width: 0;
}
.bg-clear {
  font-family: var(--font-mono); font-size: var(--text-2xs); font-weight: 600;
  color: var(--red); background: var(--red-soft);
  border: 1px solid rgba(248,113,113,.3); border-radius: var(--radius);
  padding: 3px 10px; cursor: pointer;
}
.bg-clear:hover { filter: brightness(1.2); }
.bg-url-row { display: flex; gap: 6px; margin-bottom: 8px; }
.bg-url-input {
  flex: 1; min-width: 0;
  font-family: var(--font-mono); font-size: var(--text-xs);
  color: var(--text); background: var(--surface); border: 1px solid var(--border); border-radius: var(--radius);
  padding: 7px 10px; outline: none;
}
.bg-url-input:focus { border-color: rgba(96,165,250,.5); }
.bg-url-input::placeholder { color: var(--faint); }
.bg-apply {
  font-family: var(--font-mono); font-size: var(--text-xs); font-weight: 600;
  color: var(--text-dim); background: var(--surface-2);
  border: 1px solid var(--border); border-radius: var(--radius);
  padding: 7px 14px; cursor: pointer; flex-shrink: 0;
}
.bg-apply:hover { color: var(--text); border-color: var(--border-strong); }
.bg-file-row { display: flex; gap: 8px; }
.bg-upload {
  font-family: var(--font-mono); font-size: var(--text-xs); font-weight: 600;
  color: var(--text-dim); background: var(--surface-2);
  border: 1px solid var(--border); border-radius: var(--radius);
  padding: 7px 14px; cursor: pointer;
}
.bg-upload:hover { color: var(--text); border-color: var(--border-strong); }
.bg-file-hidden { display: none; }
.bg-hint { font-size: var(--text-2xs); color: var(--faint); margin-top: 8px; }
</style>
