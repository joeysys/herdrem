<script setup lang="ts">
import { computed, ref, watch } from 'vue'
import { useTodoStore, addScope, itemScope } from '../../stores/todo'
import { useHaptic } from '../../composables/useHaptic'
import type { TodoBinding } from '../../api/types'

const props = defineProps<{ binding: TodoBinding; title?: string }>()
const emit = defineEmits<{ close: []; fill: [text: string] }>()
const todo = useTodoStore()
const { haptic } = useHaptic()

const items = computed(() => todo.itemsFor(props.binding))
const newText = ref('')
const editingKey = ref<string | null>(null)
const editText = ref('')

const scope = computed(() => addScope(props.binding))
const pending = computed(() => todo.pendingFor(scope.value))
const normalize = (s: string) => s.trim().slice(0, 64 * 1024)
// reqId → outcome last consumed here, so a late transition (unconfirmed →
// confirmed) is reprocessed while a repeated same-outcome record is not.
const handled = new Map<string, string>()

function add() {
  const body = normalize(newText.value)
  if (!body) return
  haptic()
  const res = todo.addItem(props.binding, body)
  // Echoed sends keep the draft in the pending row, not the input box.
  if (res.ok && !res.reqId) newText.value = ''
}

function itemPending(id: string) {
  return todo.pendingFor(itemScope(id))
}

function itemBusy(id: string): boolean {
  return itemPending(id)?.outcome === 'pending'
}

// Resolved ops stay in the store until the next submit replaces them. Each
// component consumes only the scopes it owns — never acks: acking an op a
// sibling component shares (same binding) would steal its resolution.
watch(
  () => todo.pending,
  (map) => {
    for (const op of Object.values(map)) {
      if (op.outcome === 'pending' || handled.get(op.reqId) === op.outcome) continue
      if (op.scope === scope.value) {
        handled.set(op.reqId, op.outcome)
        if (op.outcome === 'confirmed') {
          // Clear only what was submitted; anything typed since stays.
          if (normalize(newText.value) === op.snapshot) newText.value = ''
        } else if (!normalize(newText.value)) {
          newText.value = op.snapshot
        }
      } else if (op.scope.startsWith('todo:item:')) {
        const id = op.scope.slice('todo:item:'.length)
        if (!items.value.some((it) => it.id === id)) continue
        handled.set(op.reqId, op.outcome)
        if (op.outcome === 'confirmed' && editingKey.value === id && normalize(editText.value) === op.snapshot) {
          editingKey.value = null
        }
      }
    }
  },
  { deep: true, immediate: true },
)

function startEdit(id: string, body: string) {
  editingKey.value = id
  editText.value = body
}

function saveEdit() {
  const body = normalize(editText.value)
  if (editingKey.value && body) {
    const res = todo.setItem({ id: editingKey.value, body, binding: props.binding })
    if (res.ok && !res.reqId) editingKey.value = null
  }
}

function remove(id: string) {
  haptic()
  todo.deleteItem(id)
}

function fill(body: string) {
  haptic()
  emit('fill', body)
}
</script>

<template>
  <div class="todo-panel" role="dialog" aria-label="Todo notes">
    <header class="tp-head">
      <h3>{{ title || 'todo' }}</h3>
      <button class="tp-close" aria-label="Close" title="Close" @click="emit('close')">×</button>
    </header>

    <div class="tp-list">
      <div v-if="items.length === 0 && pending?.outcome !== 'pending'" class="tp-empty">No notes yet.</div>
      <div v-if="pending?.outcome === 'pending'" class="tp-item tp-pending">
        <pre class="tp-body">{{ pending.snapshot }}</pre>
        <span class="tp-state">sending…</span>
      </div>
      <div v-for="it in items" :key="it.id" class="tp-item">
        <template v-if="editingKey === it.id">
          <textarea v-model="editText" class="tp-edit" rows="3" maxlength="65536" :readonly="itemBusy(it.id)"></textarea>
          <div class="tp-item-actions">
            <button class="mini-btn primary" :disabled="itemBusy(it.id)" @click="saveEdit">save</button>
            <button class="mini-btn" :disabled="itemBusy(it.id)" @click="editingKey = null">cancel</button>
          </div>
          <span v-if="itemPending(it.id) && (itemPending(it.id)!.outcome === 'pending' || itemPending(it.id)!.message)" class="tp-item-state" :data-outcome="itemPending(it.id)!.outcome">
            {{ itemPending(it.id)!.outcome === 'pending' ? 'saving…' : itemPending(it.id)!.message }}
          </span>
        </template>
        <template v-else>
          <pre class="tp-body">{{ it.body }}</pre>
          <div class="tp-item-actions">
            <button class="mini-btn" title="Fill into input" :disabled="itemBusy(it.id)" @click="fill(it.body)">→ input</button>
            <button class="mini-btn" :disabled="itemBusy(it.id)" @click="startEdit(it.id, it.body)">edit</button>
            <button class="mini-btn danger" aria-label="Delete todo" :disabled="itemBusy(it.id)" @click="remove(it.id)">×</button>
          </div>
          <span v-if="itemPending(it.id) && (itemPending(it.id)!.outcome === 'pending' || itemPending(it.id)!.message)" class="tp-item-state" :data-outcome="itemPending(it.id)!.outcome">
            {{ itemPending(it.id)!.outcome === 'pending' ? (itemPending(it.id)!.kind === 'delete' ? 'deleting…' : 'saving…') : itemPending(it.id)!.message }}
          </span>
        </template>
      </div>
    </div>

    <div class="tp-add">
      <textarea
        v-model="newText"
        class="tp-new"
        rows="2"
        maxlength="65536"
        placeholder="add a note / snippet…"
        :readonly="pending?.outcome === 'pending'"
        @keydown.ctrl.enter.prevent="add"
        @keydown.meta.enter.prevent="add"
      ></textarea>
      <button class="tp-add-btn" :disabled="!newText.trim() || pending?.outcome === 'pending'" @click="add">
        {{ pending?.outcome === 'pending' ? 'adding…' : 'add' }}
      </button>
      <p v-if="pending && pending.outcome !== 'pending' && pending.message" class="tp-msg" :data-outcome="pending.outcome">{{ pending.message }}</p>
    </div>
  </div>
</template>

<style scoped>
.todo-panel {
  position: absolute; right: 0; top: 0; bottom: 0; z-index: 78;
  width: min(380px, 100%);
  display: flex; flex-direction: column;
  background: var(--bg); border-left: 1px solid var(--border); box-shadow: var(--shadow-2);
  animation: view-in 0.18s ease both;
}
.tp-head {
  display: flex; align-items: center; justify-content: space-between;
  padding: 14px 16px; border-bottom: 1px solid var(--border); flex-shrink: 0;
}
.tp-head h3 { font-size: var(--text-sm); font-weight: 700; text-transform: uppercase; letter-spacing: 0.12em; color: var(--muted); }
.tp-close {
  width: 30px; height: 30px; display: grid; place-items: center;
  font-size: var(--text-lg); color: var(--muted);
  background: var(--surface-2); border: 1px solid var(--border); border-radius: var(--radius);
}
.tp-close:hover { color: var(--text); }
.tp-list { flex: 1; overflow-y: auto; padding: 12px; display: flex; flex-direction: column; gap: 9px; }
.tp-empty { text-align: center; color: var(--faint); padding: 30px; font-size: var(--text-sm); }
.tp-item {
  background: var(--surface); border: 1px solid var(--border); border-radius: var(--radius-lg);
  padding: 10px 12px;
}
.tp-body {
  font-family: var(--font-mono); font-size: var(--text-xs); line-height: 1.55;
  color: var(--text-dim); white-space: pre-wrap; word-break: break-word; margin: 0 0 8px;
}
.tp-item-actions { display: flex; gap: 6px; }
.mini-btn {
  font-family: var(--font-mono); font-size: var(--text-2xs); color: var(--muted);
  background: var(--surface-2); border: 1px solid var(--border); border-radius: var(--radius);
  padding: 4px 10px;
}
.mini-btn:hover { color: var(--text); }
.mini-btn.primary { color: #06281a; background: var(--green); border-color: var(--green); }
.mini-btn.danger:hover { color: var(--red); border-color: rgba(248,113,113,.5); }
.tp-pending { border-style: dashed; }
.tp-pending .tp-body { color: var(--faint); }
.tp-state { font-size: var(--text-2xs); color: var(--muted); }
.tp-item-state { display: block; margin-top: 6px; font-size: var(--text-2xs); color: var(--muted); }
.tp-item-state[data-outcome='failed'], .tp-item-state[data-outcome='unconfirmed'] { color: var(--red); }
.tp-add { flex-wrap: wrap; }
.tp-msg { flex-basis: 100%; margin: 0; font-size: var(--text-2xs); color: var(--red); }
.tp-edit, .tp-new {
  width: 100%; font-family: var(--font-mono); font-size: var(--text-xs);
  background: var(--term-bg); border: 1px solid var(--border); border-radius: var(--radius);
  color: var(--term-fg); padding: 8px 10px; outline: none; resize: vertical;
}
.tp-edit:focus, .tp-new:focus { border-color: rgba(96,165,250,.5); }
.tp-edit { margin-bottom: 8px; }
.tp-add { display: flex; gap: 8px; padding: 12px; border-top: 1px solid var(--border); flex-shrink: 0; }
.tp-add-btn {
  font-family: var(--font-mono); font-size: var(--text-xs); font-weight: 700;
  color: #06281a; background: var(--green); border: 1px solid var(--green); border-radius: var(--radius);
  padding: 0 16px; align-self: stretch;
}
.tp-add-btn:disabled { opacity: 0.4; }
</style>
