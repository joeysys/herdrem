<script setup lang="ts">
import { computed, onMounted, onUnmounted, ref } from 'vue'
import { useAgentsStore } from '../../stores/connection'
import { useTodoStore, sameBinding } from '../../stores/todo'
import type { Todo, TodoBinding } from '../../api/types'

const props = defineProps<{ initialBinding?: TodoBinding }>()
const emit = defineEmits<{ close: [] }>()
const agents = useAgentsStore()
const todo = useTodoStore()
const newText = ref('')
const newBindingKey = ref('')
const editingID = ref('')
const editText = ref('')

const workspaces = computed(() => {
  const byKey = new Map<string, TodoBinding>()
  for (const pane of agents.agents) {
    const binding: TodoBinding = {
      host: pane.host || 'local',
      workspace_id: pane.workspace_id || 'default',
      workspace_label: pane.workspace_label,
    }
    byKey.set(bindingKey(binding), binding)
  }
  return [...byKey.values()].sort((a, b) => bindingLabel(a).localeCompare(bindingLabel(b)))
})

const items = computed(() => Object.values(todo.todos).sort((a, b) => b.updated_at.localeCompare(a.updated_at)))
const unbound = computed(() => {
  const active = new Set(workspaces.value.map(bindingKey))
  return items.value.filter((item) => !item.binding || !active.has(bindingKey(item.binding)))
})
const groups = computed(() => workspaces.value.map((binding) => ({
  binding,
  items: items.value.filter((item) => sameBinding(item.binding, binding)),
})).filter((group) => group.items.length))
// The workspace select doubles as the list filter: a picked workspace scopes
// the list to it, the empty "All workspaces" option shows every section and
// creates unbound todos — the option label says so explicitly.
const scopeKey = newBindingKey
const sections = computed(() => {
  const scoped = scopeKey.value
    ? groups.value.filter((group) => bindingKey(group.binding) === scopeKey.value)
    : groups.value
  return [
    ...(scopeKey.value || !unbound.value.length
      ? []
      : [{ key: 'unbound', label: 'Unbound', unbound: true, items: unbound.value }]),
    ...scoped.map((group) => ({ key: bindingKey(group.binding), label: bindingLabel(group.binding), unbound: false, items: group.items })),
  ]
})

function bindingKey(binding: TodoBinding): string {
  return `${binding.host || 'local'}\u0000${binding.workspace_id || 'default'}`
}

function bindingLabel(binding: TodoBinding): string {
  return `${binding.workspace_label || binding.workspace_id} · ${binding.host}`
}

function selectedBinding(key: string): TodoBinding | undefined {
  return workspaces.value.find((binding) => bindingKey(binding) === key)
}

function add() {
  const body = newText.value.trim()
  if (!body) return
  todo.addItem(selectedBinding(newBindingKey.value), body.slice(0, 64 * 1024))
  newText.value = ''
}

// Ctrl+Enter mirrors the add button exactly — including "All workspaces"
// (empty scope), which creates an unbound todo.
function ctrlEnterAdd() {
  add()
}

function startEdit(item: Todo) {
  editingID.value = item.id
  editText.value = item.body
}

function save(item: Todo) {
  const body = editText.value.trim()
  if (!body) return
  todo.setItem({ id: item.id, body: body.slice(0, 64 * 1024), binding: item.binding })
  editingID.value = ''
}

function rebind(item: Todo, event: Event) {
  const binding = selectedBinding((event.target as HTMLSelectElement).value)
  todo.setItem({ id: item.id, body: item.body, binding })
}

function currentBindingKey(item: Todo): string {
  return item.binding && workspaces.value.some((binding) => sameBinding(binding, item.binding))
    ? bindingKey(item.binding)
    : ''
}

function onKeydown(event: KeyboardEvent) {
  if (event.key === 'Escape') emit('close')
}

onMounted(() => {
  window.addEventListener('keydown', onKeydown)
  const key = props.initialBinding ? bindingKey(props.initialBinding) : ''
  if (key && workspaces.value.some((binding) => bindingKey(binding) === key)) {
    newBindingKey.value = key
  }
})
onUnmounted(() => window.removeEventListener('keydown', onKeydown))
</script>

<template>
  <div class="tm-backdrop" @click.self="emit('close')">
    <section class="todo-manager" role="dialog" aria-modal="true" aria-labelledby="todo-manager-title">
      <header class="tm-head">
        <h2 id="todo-manager-title">Todo</h2>
        <button class="tm-close" aria-label="Close Todo manager" title="Close" @click="emit('close')">×</button>
      </header>

      <form class="tm-add" @submit.prevent="add">
        <textarea v-model="newText" rows="2" maxlength="65536" placeholder="Add a note or snippet…" aria-label="New todo" @keydown.ctrl.enter.prevent="ctrlEnterAdd"></textarea>
        <select v-model="newBindingKey" aria-label="Workspace scope: filters list, binds new todos" title="Filters the todo list and chooses where new todos go; All workspaces creates unbound todos">
          <option value="">All workspaces (new todos unbound)</option>
          <option v-for="binding in workspaces" :key="bindingKey(binding)" :value="bindingKey(binding)">{{ bindingLabel(binding) }}</option>
        </select>
        <button :disabled="!newText.trim()">add</button>
      </form>

      <div class="tm-list">
        <div v-if="sections.length === 0" class="tm-empty">No todos yet.</div>

        <section v-for="section in sections" :key="section.key" class="tm-group">
          <h3>{{ section.label }}</h3>
          <div v-for="item in section.items" :key="item.id" class="tm-item">
            <template v-if="editingID === item.id">
              <textarea v-model="editText" rows="3" maxlength="65536" aria-label="Edit todo"></textarea>
              <div class="tm-actions"><button @click="save(item)">save</button><button @click="editingID = ''">cancel</button></div>
            </template>
            <template v-else>
              <pre>{{ item.body }}</pre>
              <span v-if="section.unbound && item.binding" class="tm-old-binding">from {{ bindingLabel(item.binding) }}</span>
              <div class="tm-actions">
                <select :value="currentBindingKey(item)" aria-label="Rebind todo" @change="rebind(item, $event)">
                  <option value="">Unbound</option>
                  <option v-for="binding in workspaces" :key="bindingKey(binding)" :value="bindingKey(binding)">{{ bindingLabel(binding) }}</option>
                </select>
                <button @click="startEdit(item)">Edit</button>
                <button class="danger" aria-label="Delete todo" @click="todo.deleteItem(item.id)">Delete</button>
              </div>
            </template>
          </div>
        </section>
      </div>
    </section>
  </div>
</template>

<style scoped>
.tm-backdrop { position: fixed; inset: 0; z-index: 85; background: rgba(0,0,0,.45); }
.todo-manager { position: absolute; inset: 0 0 0 auto; width: min(520px, 100%); display: flex; flex-direction: column; background: var(--bg); border-left: 1px solid var(--border); box-shadow: var(--shadow-2); }
.tm-head { display: flex; align-items: center; justify-content: space-between; padding: 14px 16px; border-bottom: 1px solid var(--border); }
.tm-head h2, .tm-group h3 { margin: 0; font-size: var(--text-sm); text-transform: uppercase; letter-spacing: .12em; color: var(--muted); }
.tm-close { width: 30px; height: 30px; display: flex; align-items: center; justify-content: center; padding: 0; line-height: 1; color: var(--muted); background: var(--surface-2); border: 1px solid var(--border); border-radius: var(--radius); font-size: var(--text-lg); }
.tm-add { display: grid; grid-template-columns: 1fr auto; gap: 8px; padding: 12px; border-bottom: 1px solid var(--border); }
.tm-add textarea { grid-column: 1 / -1; }
.tm-add button { color: #06281a; background: var(--green); border-color: var(--green); font-weight: 700; }
.tm-add button:disabled { opacity: .4; }
.tm-list { flex: 1; overflow: auto; padding: 12px; }
.tm-empty { padding: 30px; text-align: center; color: var(--faint); }
.tm-group { margin-bottom: 18px; }
.tm-group h3 { margin: 6px 2px 8px; }
.tm-item { padding: 10px 12px; margin-bottom: 8px; background: var(--surface); border: 1px solid var(--border); border-radius: var(--radius-lg); }
.tm-item pre { margin: 0 0 8px; color: var(--text-dim); white-space: pre-wrap; word-break: break-word; font: var(--text-xs)/1.55 var(--font-mono); }
.tm-old-binding { display: block; margin: -3px 0 8px; color: var(--faint); font: var(--text-2xs) var(--font-mono); }
.tm-actions { display: flex; gap: 6px; align-items: center; }
.tm-actions select { min-width: 0; flex: 1; }
textarea, select, button { color: var(--text-dim); background: var(--term-bg); border: 1px solid var(--border); border-radius: var(--radius); padding: 7px 9px; font: var(--text-xs) var(--font-mono); }
textarea { width: 100%; resize: vertical; }
textarea:focus, select:focus { outline: none; border-color: rgba(96,165,250,.5); }
.danger:hover { color: var(--red); border-color: rgba(248,113,113,.5); }
</style>
