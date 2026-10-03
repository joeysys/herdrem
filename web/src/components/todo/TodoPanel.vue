<script setup lang="ts">
import { computed, ref } from 'vue'
import { useTodoStore } from '../../stores/todo'
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

function add() {
  const body = newText.value.trim()
  if (!body) return
  haptic()
  todo.addItem(props.binding, body.slice(0, 64 * 1024))
  newText.value = ''
}

function startEdit(id: string, body: string) {
  editingKey.value = id
  editText.value = body
}

function saveEdit() {
  const body = editText.value.trim()
  if (editingKey.value && body) {
    todo.setItem({ id: editingKey.value, body: body.slice(0, 64 * 1024), binding: props.binding })
    editingKey.value = null
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
      <div v-if="items.length === 0" class="tp-empty">No notes yet.</div>
      <div v-for="it in items" :key="it.id" class="tp-item">
        <template v-if="editingKey === it.id">
          <textarea v-model="editText" class="tp-edit" rows="3" maxlength="65536"></textarea>
          <div class="tp-item-actions">
            <button class="mini-btn primary" @click="saveEdit">save</button>
            <button class="mini-btn" @click="editingKey = null">cancel</button>
          </div>
        </template>
        <template v-else>
          <pre class="tp-body">{{ it.body }}</pre>
          <div class="tp-item-actions">
            <button class="mini-btn" title="Fill into input" @click="fill(it.body)">→ input</button>
            <button class="mini-btn" @click="startEdit(it.id, it.body)">edit</button>
            <button class="mini-btn danger" aria-label="Delete todo" @click="remove(it.id)">×</button>
          </div>
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
        @keydown.ctrl.enter.prevent="add"
        @keydown.meta.enter.prevent="add"
      ></textarea>
      <button class="tp-add-btn" :disabled="!newText.trim()" @click="add">add</button>
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
