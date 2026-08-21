<script setup lang="ts">
import { ref } from 'vue'
import { useRelaysStore, type RelayEntry } from '../../stores/relays'
import { useRelayConnection } from '../../composables/useRelayConnection'
import { relayHostLabel } from '../../utils/relay'

const relays = useRelaysStore()
const { switchTo, connectRelay } = useRelayConnection()

const emit = defineEmits<{ switched: [] }>()

const showForm = ref(false)
const editingId = ref<string | null>(null)
const name = ref('')
const url = ref('')
const token = ref('')

function startAdd() {
  editingId.value = null
  name.value = ''
  url.value = ''
  token.value = ''
  showForm.value = true
}

function startEdit(r: RelayEntry) {
  editingId.value = r.id
  name.value = r.name
  url.value = r.url
  token.value = r.token || ''
  showForm.value = true
}

function save() {
  if (!url.value.trim()) return
  const displayName = name.value.trim() || relayHostLabel(url.value)
  if (editingId.value) {
    relays.update(editingId.value, { name: displayName, url: url.value, token: token.value || undefined })
  } else {
    const entry = relays.upsert({ name: displayName, url: url.value, token: token.value || undefined })
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
      <input v-model="token" class="fld mono" type="password" placeholder="token (optional)" aria-label="Relay token" />
      <div class="rm-form-actions">
        <button class="btn ghost" @click="showForm = false">cancel</button>
        <button class="btn primary" :disabled="!url.trim()" @click="save">{{ editingId ? 'save' : 'add & connect' }}</button>
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
.fld:focus { border-color: rgba(96,165,250,.5); }
.rm-form-actions { display: flex; justify-content: flex-end; gap: 8px; margin-top: 2px; }

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
