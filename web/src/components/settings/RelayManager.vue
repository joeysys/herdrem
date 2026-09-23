<script setup lang="ts">
import { computed, ref, watch } from 'vue'
import { useRelaysStore, type RelayEntry } from '../../stores/relays'
import { useConnectionStore } from '../../stores/connection'
import { useRelayConnection } from '../../composables/useRelayConnection'
import { normalizeRelayUrl, relayHostLabel, relayNeedsToken, sessionTokenFor } from '../../utils/relay'

const relays = useRelaysStore()
const conn = useConnectionStore()
const { switchTo, connectRelay } = useRelayConnection()

const emit = defineEmits<{ switched: [] }>()

const showForm = ref(false)
const editingId = ref<string | null>(null)
const name = ref('')
const url = ref('')
const token = ref('')
const showToken = ref(false)
const missingToken = computed(() =>
  Boolean(url.value.trim()) && relayNeedsToken(url.value) &&
  !token.value.trim() && !sessionTokenFor(normalizeRelayUrl(url.value))
)

function startAdd() {
  editingId.value = null
  name.value = ''
  url.value = ''
  token.value = ''
  showToken.value = false
  showForm.value = true
}

function startEdit(r: RelayEntry) {
  editingId.value = r.id
  name.value = r.name
  url.value = r.url
  token.value = r.token || sessionTokenFor(r.url)
  showToken.value = false
  showForm.value = true
}

// The prefilled token belongs to the relay's stored address — once the URL
// diverges it must not ride along to the new one. Clearing the field makes
// save() treat it as no explicit input (update() then adopts the new URL's
// own session credential, or none) and lets missingToken gate the save.
watch(url, () => {
  if (!editingId.value) return
  const entry = relays.relays.find((r) => r.id === editingId.value)
  if (entry && normalizeRelayUrl(url.value) !== entry.url) token.value = ''
})

function save() {
  if (!url.value.trim() || missingToken.value) return
  const displayName = name.value.trim() || relayHostLabel(url.value)
  const enteredToken = token.value.trim()
  if (editingId.value) {
    const previous = relays.relays.find((relay) => relay.id === editingId.value)
    // An empty token field keeps the stored credential — the same meaning the
    // add path gives it via upsert's sessionTokenFor fallback. Writing
    // `token: undefined` would erase only the in-memory copy (sessionStorage
    // resurrects it on reload) and the token diff would force a reconnect.
    const patch: { name: string; url: string; token?: string } = { name: displayName, url: url.value }
    if (enteredToken) patch.token = enteredToken
    relays.update(editingId.value, patch)
    const updated = relays.relays.find((relay) => relay.id === editingId.value)
    if (updated && updated.id === relays.currentId && (
      conn.status !== 'connected' || previous?.url !== updated.url || previous?.token !== updated.token
    )) {
      connectRelay(updated)
      emit('switched')
    }
  } else {
    const entry = relays.upsert({ name: displayName, url: url.value, token: enteredToken || undefined })
    relays.setCurrent(entry.id)
    connectRelay(entry)
    emit('switched')
  }
  showForm.value = false
}

function onSwitch(id: string) {
  switchTo(id)
  emit('switched')
}

function onRemove(id: string) {
  relays.remove(id)
}
</script>

<template>
  <div class="relay-manager">
    <div class="rm-head">
      <h3>relays</h3>
      <button class="rm-add" @click="startAdd">+ add</button>
    </div>

    <div v-if="showForm" class="rm-form">
      <input v-model="name" class="fld" placeholder="name (optional)" aria-label="Relay name" />
      <input v-model="url" class="fld mono" placeholder="wss://your-tunnel…" aria-label="Relay URL" />
      <div class="rm-token-row">
        <input v-model="token" class="fld mono" :type="showToken ? 'text' : 'password'" placeholder="token (required for non-local)" aria-label="Relay token" autocomplete="off" />
        <button
          class="btn ghost"
          type="button"
          :aria-label="showToken ? 'Hide relay token' : 'Show relay token'"
          :aria-pressed="showToken"
          @click="showToken = !showToken"
        >{{ showToken ? 'hide' : 'show' }}</button>
      </div>
      <p v-if="missingToken" class="rm-hint">A token is required for this relay address.</p>
      <div class="rm-form-actions">
        <button class="btn ghost" @click="showForm = false">cancel</button>
        <button class="btn primary" :disabled="!url.trim() || missingToken" @click="save">{{ editingId ? 'save' : 'add & connect' }}</button>
      </div>
    </div>

    <ul class="rm-list">
      <li v-if="relays.relays.length === 0" class="rm-empty">No relays yet — add one to get started.</li>
      <li
        v-for="r in relays.relays"
        :key="r.id"
        class="rm-item"
        :class="{ active: r.id === relays.currentId }"
      >
        <button class="rm-main" @click="onSwitch(r.id)">
          <span class="rm-dot" aria-hidden="true"></span>
          <span class="rm-text">
            <span class="rm-name">{{ r.name || relayHostLabel(r.url) }}</span>
            <span class="rm-host">{{ relayHostLabel(r.url) }}</span>
          </span>
          <span v-if="r.id === relays.currentId" class="rm-current">current</span>
        </button>
        <div class="rm-actions">
          <button class="mini" aria-label="Edit" title="Edit relay" @click="startEdit(r)">✎</button>
          <button class="mini danger" aria-label="Delete" title="Delete relay" @click="onRemove(r.id)">×</button>
        </div>
      </li>
    </ul>
  </div>
</template>

<style scoped>
.relay-manager { margin-bottom: 6px; }
.rm-head { display: flex; align-items: center; justify-content: space-between; margin-bottom: 10px; }
.rm-head h3 {
  font-size: var(--text-sm); font-weight: 700;
  text-transform: uppercase; letter-spacing: 0.12em; color: var(--muted);
}
.rm-add {
  font-family: var(--font-mono); font-size: var(--text-xs); font-weight: 700;
  color: var(--blue); background: var(--blue-soft);
  border: 1px solid rgba(96,165,250,.4); border-radius: var(--radius);
  padding: 5px 12px;
}
.rm-add:hover { filter: brightness(1.15); }

.rm-form {
  display: flex; flex-direction: column; gap: 8px;
  padding: 12px; margin-bottom: 12px;
  background: var(--surface-2); border: 1px solid var(--border); border-radius: var(--radius-lg);
}
.fld {
  background: var(--term-bg); border: 1px solid var(--border); border-radius: var(--radius);
  color: var(--text); font-family: var(--font-display); font-size: var(--text-sm);
  padding: 8px 10px; outline: none;
}
.fld.mono { font-family: var(--font-mono); font-size: var(--text-xs); }
.rm-token-row { display: flex; gap: 8px; }
.rm-token-row .fld { flex: 1; min-width: 0; }
.fld:focus { border-color: rgba(96,165,250,.5); }
.rm-form-actions { display: flex; justify-content: flex-end; gap: 8px; margin-top: 2px; }
.rm-hint { margin: -2px 0 0; color: var(--red); font-size: var(--text-xs); }

.btn { font-family: var(--font-mono); font-size: var(--text-xs); font-weight: 700; padding: 7px 14px; border-radius: var(--radius); border: 1px solid; }
.btn.ghost { color: var(--muted); background: transparent; border-color: var(--border); }
.btn.ghost:hover { color: var(--text); }
.btn.primary { color: #06281a; background: var(--green); border-color: var(--green); }
.btn.primary:disabled { opacity: 0.4; }

.rm-list { list-style: none; display: flex; flex-direction: column; gap: 7px; }
.rm-empty { color: var(--faint); font-size: var(--text-sm); text-align: center; padding: 18px; }
.rm-item {
  display: flex; align-items: center; gap: 6px;
  background: var(--surface); border: 1px solid var(--border); border-radius: var(--radius-lg);
  transition: border-color 0.15s;
}
.rm-item.active { border-color: rgba(52,211,153,.5); }
.rm-main {
  flex: 1; min-width: 0; display: flex; align-items: center; gap: 10px;
  background: transparent; border: none; text-align: left; padding: 10px 12px; cursor: pointer;
}
.rm-dot { width: 8px; height: 8px; border-radius: 50%; background: var(--faint); flex-shrink: 0; }
.rm-item.active .rm-dot { background: var(--green); animation: pulse 2s ease-in-out infinite; }
.rm-text { flex: 1; min-width: 0; }
.rm-name { display: block; color: var(--text); font-weight: 600; font-size: var(--text-sm); white-space: nowrap; overflow: hidden; text-overflow: ellipsis; }
.rm-host { display: block; font-family: var(--font-mono); font-size: var(--text-2xs); color: var(--faint); white-space: nowrap; overflow: hidden; text-overflow: ellipsis; }
.rm-current { font-family: var(--font-mono); font-size: var(--text-2xs); text-transform: uppercase; letter-spacing: 0.1em; color: var(--green); flex-shrink: 0; }
.rm-actions { display: flex; gap: 4px; padding-right: 8px; }
.mini {
  width: 26px; height: 26px; display: grid; place-items: center;
  color: var(--muted); background: var(--surface-2); border: 1px solid var(--border); border-radius: var(--radius);
  font-size: var(--text-sm);
}
.mini:hover { color: var(--text); }
.mini.danger:hover { color: var(--red); border-color: rgba(248,113,113,.5); }
</style>
