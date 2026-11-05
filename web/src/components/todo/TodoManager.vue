<script setup lang="ts">
import { computed, onMounted, ref, watch } from 'vue'
import { useAgentsStore } from '../../stores/connection'
import { useTodoStore, sameBinding, addScope, itemScope } from '../../stores/todo'
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

// The scope+reqId this manager actually submitted. The workspace select is
// also the list filter and can move while the add is in flight; the result is
// still consumed against the op we sent, not whatever is selected now — and
// ownership ends with that op, so a later foreign op on the same scope is
// never mistaken for ours.
const ownedAddScope = ref('')
const ownedAddReqId = ref('')
const addPending = computed(() => {
  const owned = ownedAddScope.value ? todo.pendingFor(ownedAddScope.value) : undefined
  if (owned && owned.outcome !== 'confirmed') return owned
  return todo.pendingFor(addScope(selectedBinding(newBindingKey.value)))
})
// Overrides let the rebind select show the picked workspace while its op is
// in flight; resolving the op drops the override so it snaps back on failure.
const rebindOverride = ref<Record<string, string>>({})
const normalize = (s: string) => s.trim().slice(0, 64 * 1024)
// reqId → outcome last consumed here, so a late transition (unconfirmed →
// confirmed) is reprocessed while a repeated same-outcome record is not.
const handled = new Map<string, string>()

function add() {
  const body = normalize(newText.value)
  if (!body) return
  const binding = selectedBinding(newBindingKey.value)
  const scope = addScope(binding)
  const res = todo.addItem(binding, body)
  if (res.ok && res.reqId) {
    ownedAddScope.value = scope
    ownedAddReqId.value = res.reqId
  } else if (!res.ok && res.reason === 'not connected') {
    // trackFailed parks the op under an internal local-* id; take it off the
    // parked record so ownership still binds this one op.
    ownedAddScope.value = scope
    ownedAddReqId.value = todo.pendingFor(scope)?.reqId ?? ''
  }
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
// Add results follow the scope submitted here (ownedAddScope), not the
// filter the select happens to show when the echo arrives. newBindingKey is
// a watch source so switching back still consumes an op we had not seen.
watch(
  () => [todo.pending, newBindingKey.value] as const,
  () => {
    const currentAdd = addScope(selectedBinding(newBindingKey.value))
    for (const op of Object.values(todo.pending)) {
      if (op.outcome === 'pending' || handled.get(op.reqId) === op.outcome) continue
      if (op.scope.startsWith('todo:add:')) {
        const owned = op.scope === ownedAddScope.value && op.reqId === ownedAddReqId.value
        if (!owned && op.scope !== currentAdd) continue
        handled.set(op.reqId, op.outcome)
        // unconfirmed stays owned — a late echo may still flip it to
        // confirmed and that transition is ours to consume as well.
        if (owned && op.outcome !== 'unconfirmed') {
          ownedAddScope.value = ''
          ownedAddReqId.value = ''
        }
        if (op.outcome === 'confirmed') {
          // Clear only what was submitted; anything typed since stays.
          if (normalize(newText.value) === op.snapshot) newText.value = ''
        } else if (!normalize(newText.value)) {
          newText.value = op.snapshot
        }
      } else if (op.scope.startsWith('todo:item:')) {
        const id = op.scope.slice('todo:item:'.length)
        if (!todo.todos[id] && editingID.value !== id) continue
        handled.set(op.reqId, op.outcome)
        if (op.kind === 'rebind') delete rebindOverride.value[id]
        else if (op.kind === 'set' && editingID.value === id) {
          if (op.outcome === 'confirmed') {
            if (normalize(editText.value) === op.snapshot) editingID.value = ''
          } else if (!normalize(editText.value)) {
            editText.value = op.snapshot
          }
        }
      }
    }
  },
  { deep: true, immediate: true },
)

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
  const body = normalize(editText.value)
  if (!body) return
  const res = todo.setItem({ id: item.id, body, binding: item.binding })
  if (res.ok && !res.reqId) editingID.value = ''
}

function rebind(item: Todo, event: Event) {
  const key = (event.target as HTMLSelectElement).value
  rebindOverride.value[item.id] = key
  const res = todo.rebindItem(item, selectedBinding(key))
  // A refused submit never produces a pending op — drop the override now or
  // the select would stick on a workspace the item was never moved to.
  if (!res.ok) delete rebindOverride.value[item.id]
}

function currentBindingKey(item: Todo): string {
  return item.binding && workspaces.value.some((binding) => sameBinding(binding, item.binding))
    ? bindingKey(item.binding)
    : ''
}

onMounted(() => {
  const key = props.initialBinding ? bindingKey(props.initialBinding) : ''
  if (key && workspaces.value.some((binding) => bindingKey(binding) === key)) {
    newBindingKey.value = key
  }
})
</script>

<template>
  <div class="tm-backdrop" @click.self="emit('close')">
    <section class="todo-manager" role="dialog" aria-modal="true" aria-labelledby="todo-manager-title">
      <header class="tm-head">
        <h2 id="todo-manager-title">Todo</h2>
        <button class="tm-close" aria-label="Close Todo manager" title="Close" @click="emit('close')">×</button>
      </header>

      <form class="tm-add" @submit.prevent="add">
        <textarea v-model="newText" rows="2" maxlength="65536" placeholder="Add a note or snippet…" aria-label="New todo" :readonly="addPending?.outcome === 'pending'" @keydown.ctrl.enter.prevent="ctrlEnterAdd" @keydown.meta.enter.prevent="ctrlEnterAdd"></textarea>
        <select v-model="newBindingKey" aria-label="Workspace scope: filters list, binds new todos" title="Filters the todo list and chooses where new todos go; All workspaces creates unbound todos">
          <option value="">All workspaces (new todos unbound)</option>
          <option v-for="binding in workspaces" :key="bindingKey(binding)" :value="bindingKey(binding)">{{ bindingLabel(binding) }}</option>
        </select>
        <button :disabled="!newText.trim() || addPending?.outcome === 'pending'">{{ addPending?.outcome === 'pending' ? 'adding…' : 'add' }}</button>
        <p v-if="addPending && addPending.outcome !== 'pending' && addPending.message" class="tm-msg" :data-outcome="addPending.outcome">{{ addPending.message }}</p>
      </form>

      <div class="tm-list">
        <div v-if="sections.length === 0 && addPending?.outcome !== 'pending'" class="tm-empty">No todos yet.</div>
        <div v-if="addPending?.outcome === 'pending'" class="tm-item tm-pending">adding: {{ addPending.snapshot }} — sending…</div>

        <section v-for="section in sections" :key="section.key" class="tm-group">
          <h3>{{ section.label }}</h3>
          <div v-for="item in section.items" :key="item.id" class="tm-item">
            <template v-if="editingID === item.id">
              <textarea v-model="editText" rows="3" maxlength="65536" aria-label="Edit todo" :readonly="itemBusy(item.id)"></textarea>
              <div class="tm-actions"><button :disabled="itemBusy(item.id)" @click="save(item)">save</button><button :disabled="itemBusy(item.id)" @click="editingID = ''">cancel</button></div>
            </template>
            <template v-else>
              <pre>{{ item.body }}</pre>
              <span v-if="section.unbound && item.binding" class="tm-old-binding">from {{ bindingLabel(item.binding) }}</span>
              <div class="tm-actions">
                <select :value="rebindOverride[item.id] ?? currentBindingKey(item)" :disabled="itemBusy(item.id)" aria-label="Rebind todo" @change="rebind(item, $event)">
                  <option value="">Unbound</option>
                  <option v-for="binding in workspaces" :key="bindingKey(binding)" :value="bindingKey(binding)">{{ bindingLabel(binding) }}</option>
                </select>
                <button :disabled="itemBusy(item.id)" @click="startEdit(item)">Edit</button>
                <button class="danger" :disabled="itemBusy(item.id)" aria-label="Delete todo" @click="todo.deleteItem(item.id)">Delete</button>
              </div>
            </template>
            <span v-if="itemPending(item.id) && (itemPending(item.id)!.outcome === 'pending' || itemPending(item.id)!.message)" class="tm-item-state" :data-outcome="itemPending(item.id)!.outcome">
              {{ itemPending(item.id)!.outcome === 'pending'
                ? (itemPending(item.id)!.kind === 'delete' ? 'deleting…' : itemPending(item.id)!.kind === 'rebind' ? 'rebinding…' : 'saving…')
                : itemPending(item.id)!.message }}
            </span>
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
.tm-pending { border-style: dashed; color: var(--faint); font: var(--text-xs) var(--font-mono); }
.tm-msg { grid-column: 1 / -1; margin: 0; color: var(--red); font: var(--text-2xs) var(--font-mono); }
.tm-item-state { display: block; margin-top: 8px; color: var(--muted); font: var(--text-2xs) var(--font-mono); }
.tm-item-state[data-outcome='failed'], .tm-item-state[data-outcome='unconfirmed'] { color: var(--red); }
.tm-actions { display: flex; gap: 6px; align-items: center; }
.tm-actions select { min-width: 0; flex: 1; }
textarea, select, button { color: var(--text-dim); background: var(--term-bg); border: 1px solid var(--border); border-radius: var(--radius); padding: 7px 9px; font: var(--text-xs) var(--font-mono); }
textarea { width: 100%; resize: vertical; }
textarea:focus, select:focus { outline: none; border-color: rgba(96,165,250,.5); }
.danger:hover { color: var(--red); border-color: rgba(248,113,113,.5); }
</style>
