<script setup lang="ts">
import { ref, watch } from 'vue'
import { useConnectionStore } from '../../stores/connection'
import { useTunnelsStore } from '../../stores/tunnels'
import { useHaptic } from '../../composables/useHaptic'

const conn = useConnectionStore()
const tunnels = useTunnelsStore()
const { haptic } = useHaptic()

const inputPort = ref(5173)
const authMode = ref<'token_gate' | 'basic_auth' | 'direct'>('token_gate')
const inputPin = ref('')

watch(
  () => conn.hasFeature('tunnel-direct'),
  (ok) => {
    if (!ok && authMode.value === 'direct') authMode.value = 'token_gate'
  },
)

const copiedId = ref<string | null>(null)

function startTunnel() {
  if (!inputPort.value || inputPort.value <= 0 || inputPort.value > 65535) return
  // `direct` creates an anonymous Quick Tunnel with no gate in front of it.
  // It is NOT Cloudflare Access, so require an explicit acknowledgement before
  // publishing a local port to the internet unauthenticated.
  if (authMode.value === 'direct') {
    const ok = window.confirm(
      `Publish localhost:${inputPort.value} to the public internet with NO authentication?\n\n` +
      'Anyone who obtains the generated *.trycloudflare.com URL will have full access ' +
      'to this port. This mode does not use Cloudflare Access.',
    )
    if (!ok) return
  }
  haptic()
  tunnels.createTunnel(inputPort.value, authMode.value, inputPin.value.trim() || undefined)
  inputPin.value = ''
}

function copyText(text: string, id: string) {
  haptic()
  navigator.clipboard.writeText(text)
  copiedId.value = id
  setTimeout(() => {
    if (copiedId.value === id) copiedId.value = null
  }, 2000)
}

function getFullUrlWithToken(t: { public_url: string; token?: string; auth_mode: string }) {
  if (!t.public_url) return ''
  if (t.auth_mode === 'token_gate' && t.token) {
    const sep = t.public_url.includes('?') ? '&' : '?'
    return `${t.public_url}${sep}token=${encodeURIComponent(t.token)}`
  }
  return t.public_url
}

function previewTunnel(t: { public_url: string; token?: string; auth_mode: string; port: number }) {
  haptic()
  // The embedded iframe must not carry the gate token in its src: the URL
  // lives in the DOM and browser history, and a sandboxed previewed page
  // could otherwise read it. The clean URL lands on the tunnel's PIN/login
  // page, which works inside the iframe; the "open in new tab" link still
  // carries the token for one-click auth.
  if (t.public_url) {
    tunnels.openPreview(t.public_url, `Local Port :${t.port}`)
  }
}
</script>

<template>
  <div class="tunnel-panel-backdrop" @click.self="tunnels.showPanel = false">
    <div class="tunnel-panel">
      <header class="tp-head">
        <div class="tp-title-group">
          <h3>Cloudflare Tunnels</h3>
          <span class="tp-sub">Dynamic Port Forwarding & Access Control</span>
        </div>
        <button class="tp-close" aria-label="Close" @click="tunnels.showPanel = false">×</button>
      </header>

      <div v-if="!conn.hasFeature('tunnels')" class="tp-warn-banner">
        ⚠ The running Relay does not include the <code>tunnels</code> dynamic port-forwarding feature. Rebuild and restart the Relay process.
      </div>

      <!-- Port Forwarding Creation Form -->
      <div class="tp-create">
        <div class="tp-form-row">
          <div class="tp-field flex-1">
            <label>Port</label>
            <input v-model.number="inputPort" type="number" min="1" max="65535" placeholder="5173" />
          </div>
          <div class="tp-field flex-2">
            <label>Access Control Mode</label>
            <select v-model="authMode">
              <option value="token_gate">🔒 Token Gate (Default - Web Auto-Auth)</option>
              <option value="basic_auth">🔑 Basic Auth (Native Browser Popup)</option>
              <option v-if="conn.hasFeature('tunnel-direct')" value="direct">⚠ No Auth — public, anyone with the URL</option>
            </select>
            <p class="tp-sub" style="margin-top:6px">
              Allowed ports: 5173, 3000, 8080, 4173 (plus <code>HERDREM_TUNNEL_PORTS</code> singles or ranges like <code>8000-8100</code>; ports below 1024 are blocked).
            </p>
            <p v-if="authMode === 'direct'" class="tp-danger-note">
              This publishes <code>localhost:{{ inputPort }}</code> to the public internet
              with <strong>no authentication</strong>. It does not use Cloudflare Access.
              Anyone who obtains the URL gets full access to that port.
            </p>
          </div>
        </div>
        <div class="tp-form-row">
          <div class="tp-field flex-1">
            <label>Custom PIN / Token (Optional)</label>
            <input v-model="inputPin" type="text" placeholder="Auto-generated if empty" />
          </div>
          <button class="tp-submit-btn" :disabled="!inputPort" @click="startTunnel">
            + Forward Port
          </button>
        </div>
      </div>

      <!-- Tunnel List -->
      <div class="tp-list">
        <div v-if="tunnels.tunnels.length === 0" class="tp-empty">
          No active port tunnels. Forward a local dev port (e.g. 5173, 3000) to get a public HTTPS URL.
        </div>

        <div v-for="t in tunnels.tunnels" :key="t.id" class="tp-card" :class="t.status">
          <div class="tp-card-head">
            <div class="tp-card-info">
              <span class="tp-status-dot" :class="t.status"></span>
              <span class="tp-port mono">localhost:{{ t.port }}</span>
              <span class="tp-auth-tag mono">{{ t.auth_mode }}</span>
            </div>
            <button class="tp-card-close" title="Close Tunnel" @click="tunnels.closeTunnel(t.id)">×</button>
          </div>

          <div v-if="t.status === 'starting'" class="tp-loading">
            <span class="spinner"></span> Spawning cloudflared quick tunnel…
          </div>

          <div v-else-if="t.status === 'error'" class="tp-error">
            ⚠ {{ t.error || 'Failed to start cloudflared tunnel' }}
          </div>

          <template v-else-if="t.status === 'active'">
            <div class="tp-url-box mono">
              <span class="tp-url">{{ t.public_url }}</span>
              <button class="tp-icon-btn" title="Copy Public URL" @click="copyText(t.public_url, `url-${t.id}`)">
                {{ copiedId === `url-${t.id}` ? '✓ Copied' : '📋' }}
              </button>
              <a :href="getFullUrlWithToken(t)" target="_blank" rel="noopener" class="tp-icon-btn" title="Open in New Tab">
                ↗
              </a>
            </div>

            <div v-if="t.auth_mode !== 'direct' && t.token" class="tp-token-bar mono">
              <span class="tp-token-label">PIN / Token:</span>
              <code class="tp-token-val">{{ t.token }}</code>
              <button class="tp-mini-copy" @click="copyText(t.token, `tk-${t.id}`)">
                {{ copiedId === `tk-${t.id}` ? '✓' : 'Copy' }}
              </button>
            </div>

            <div class="tp-card-actions">
              <button class="tp-action-btn primary" @click="previewTunnel(t)">
                Embedded Preview
              </button>
            </div>
          </template>
        </div>
      </div>
    </div>
  </div>

  <!-- Embedded Preview Modal Overlay -->
  <div v-if="tunnels.previewUrl" class="embedded-preview-backdrop">
    <div class="embedded-preview-modal">
      <header class="ep-head">
        <div class="ep-title mono">
          {{ tunnels.previewTitle || 'Embedded Dev Preview' }}
        </div>
        <div class="ep-actions">
          <a :href="tunnels.previewUrl" target="_blank" rel="noopener" class="ep-btn" title="Open in New Tab">↗ Browser</a>
          <button class="ep-close" aria-label="Close Preview" @click="tunnels.closePreview()">×</button>
        </div>
      </header>
      <div class="ep-body">
        <iframe :src="tunnels.previewUrl" sandbox="allow-scripts allow-forms allow-popups allow-same-origin" class="ep-iframe" title="Embedded Web Preview"></iframe>
      </div>
    </div>
  </div>
</template>

<style scoped>
.mono { font-family: var(--font-mono); }
.tunnel-panel-backdrop {
  position: fixed; inset: 0; z-index: 120;
  background: rgba(0, 0, 0, 0.6); backdrop-filter: blur(4px);
  display: flex; align-items: center; justify-content: center; padding: 20px;
}
.tunnel-panel {
  width: min(540px, 100%); max-height: 85vh; display: flex; flex-direction: column;
  background: var(--bg); border: 1px solid var(--border-strong, var(--border));
  border-radius: var(--radius-lg); box-shadow: var(--shadow-2); overflow: hidden;
  animation: modal-in 0.2s cubic-bezier(0.16, 1, 0.3, 1) both;
}
.tp-head {
  display: flex; align-items: center; justify-content: space-between;
  padding: 16px 20px; border-bottom: 1px solid var(--border); background: var(--surface);
}
.tp-title-group h3 { font-size: var(--text-base); font-weight: 700; margin: 0; color: var(--text); }
.tp-sub { font-size: var(--text-2xs); color: var(--muted); }
.tp-close {
  width: 32px; height: 32px; display: grid; place-items: center; font-size: 20px;
  color: var(--muted); background: var(--surface-2); border: 1px solid var(--border); border-radius: var(--radius);
}
.tp-close:hover { color: var(--text); }
.tp-warn-banner {
  background: rgba(251, 191, 36, 0.15); border-bottom: 1px solid rgba(251, 191, 36, 0.3);
  color: var(--amber); padding: 10px 16px; font-size: var(--text-xs); font-family: var(--font-mono); line-height: 1.5;
}

.tp-danger-note {
  margin: 8px 0 0; padding: 8px 10px; border-radius: 6px;
  background: rgba(239, 68, 68, 0.12); border: 1px solid rgba(239, 68, 68, 0.35);
  color: #fca5a5; font-size: var(--text-2xs); line-height: 1.5;
}
.tp-danger-note code { font-family: var(--font-mono); }

.tp-create { padding: 16px 20px; border-bottom: 1px solid var(--border); background: var(--bg-deep); display: flex; flex-direction: column; gap: 12px; }
.tp-form-row { display: flex; gap: 12px; align-items: flex-end; }
.flex-1 { flex: 1; min-width: 0; }
.flex-2 { flex: 2; min-width: 0; }
.tp-field label { display: block; font-size: var(--text-2xs); font-weight: 600; color: var(--muted); margin-bottom: 5px; }
.tp-field input, .tp-field select {
  width: 100%; box-sizing: border-box; font-family: var(--font-mono); font-size: var(--text-xs);
  background: var(--surface); border: 1px solid var(--border); border-radius: var(--radius);
  color: var(--text); padding: 7px 10px; outline: none;
}
.tp-field input:focus, .tp-field select:focus { border-color: var(--blue); }
.tp-submit-btn {
  font-family: var(--font-mono); font-size: var(--text-xs); font-weight: 700;
  color: #06281a; background: var(--green); border: 1px solid var(--green); border-radius: var(--radius);
  padding: 8px 16px; white-space: nowrap; height: 33px; cursor: pointer;
}
.tp-submit-btn:disabled { opacity: 0.5; cursor: not-allowed; }

.tp-list { flex: 1; overflow-y: auto; padding: 16px 20px; display: flex; flex-direction: column; gap: 12px; }
.tp-empty { text-align: center; color: var(--faint); padding: 30px; font-size: var(--text-xs); line-height: 1.6; }

.tp-card {
  background: var(--surface); border: 1px solid var(--border); border-radius: var(--radius);
  padding: 14px; display: flex; flex-direction: column; gap: 10px;
}
.tp-card.error { border-color: rgba(248, 113, 113, 0.4); }
.tp-card-head { display: flex; align-items: center; justify-content: space-between; }
.tp-card-info { display: flex; align-items: center; gap: 8px; }
.tp-status-dot { width: 8px; height: 8px; border-radius: 50%; background: var(--faint); }
.tp-status-dot.active { background: var(--green); box-shadow: 0 0 8px var(--green); }
.tp-status-dot.starting { background: var(--amber); }
.tp-status-dot.error { background: var(--red); }

.tp-port { font-weight: 700; font-size: var(--text-sm); color: var(--text); }
.tp-auth-tag { font-size: 10px; padding: 2px 6px; border-radius: 4px; background: var(--surface-2); color: var(--muted); border: 1px solid var(--border); }
.tp-card-close { font-size: 16px; color: var(--faint); background: none; border: none; cursor: pointer; }
.tp-card-close:hover { color: var(--red); }

.tp-loading { font-family: var(--font-mono); font-size: var(--text-xs); color: var(--amber); display: flex; align-items: center; gap: 8px; }
.tp-error { font-family: var(--font-mono); font-size: var(--text-xs); color: var(--red); }

.tp-url-box {
  display: flex; align-items: center; gap: 8px; background: var(--term-bg);
  border: 1px solid var(--border); border-radius: var(--radius); padding: 6px 10px; font-size: var(--text-xs);
}
.tp-url { flex: 1; min-width: 0; color: #38bdf8; overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }
.tp-icon-btn { color: var(--muted); font-size: var(--text-xs); text-decoration: none; padding: 2px 6px; border-radius: 4px; background: var(--surface); border: 1px solid var(--border); cursor: pointer; }
.tp-icon-btn:hover { color: var(--text); }

.tp-token-bar { display: flex; align-items: center; gap: 8px; font-size: var(--text-2xs); color: var(--muted); }
.tp-token-val { color: var(--green); font-weight: 600; }
.tp-mini-copy { font-size: 10px; padding: 1px 6px; background: var(--surface-2); border: 1px solid var(--border); border-radius: 3px; color: var(--muted); cursor: pointer; }

.tp-card-actions { display: flex; justify-content: flex-end; }
.tp-action-btn { font-family: var(--font-mono); font-size: var(--text-xs); padding: 5px 12px; border-radius: var(--radius); cursor: pointer; }
.tp-action-btn.primary { background: rgba(56, 189, 248, 0.15); color: #38bdf8; border: 1px solid rgba(56, 189, 248, 0.4); }
.tp-action-btn.primary:hover { background: rgba(56, 189, 248, 0.25); }

/* Embedded Preview Modal */
.embedded-preview-backdrop {
  position: fixed; inset: 0; z-index: 150; background: rgba(0, 0, 0, 0.75);
  display: flex; align-items: center; justify-content: center; padding: 16px;
}
.embedded-preview-modal {
  width: 95vw; height: 90vh; background: #121212; border: 1px solid var(--border-strong, var(--border));
  border-radius: var(--radius-lg); display: flex; flex-direction: column; overflow: hidden;
  box-shadow: 0 25px 50px -12px rgba(0, 0, 0, 0.7);
}
.ep-head {
  display: flex; align-items: center; justify-content: space-between;
  padding: 10px 16px; background: #1e1e1e; border-bottom: 1px solid var(--border);
}
.ep-title { font-size: var(--text-xs); color: #38bdf8; font-weight: 600; }
.ep-actions { display: flex; align-items: center; gap: 8px; }
.ep-btn { font-family: var(--font-mono); font-size: var(--text-2xs); color: var(--muted); text-decoration: none; padding: 3px 8px; border-radius: var(--radius); background: var(--surface-2); border: 1px solid var(--border); }
.ep-close { width: 26px; height: 26px; font-size: 18px; color: var(--muted); background: var(--surface-2); border: 1px solid var(--border); border-radius: var(--radius); cursor: pointer; }
.ep-body { flex: 1; min-height: 0; width: 100%; background: #ffffff; }
.ep-iframe { width: 100%; height: 100%; border: none; }
</style>
