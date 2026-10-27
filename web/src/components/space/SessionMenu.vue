<script setup lang="ts">
import { computed, ref, watch } from 'vue'
import { ws } from '../../api/ws'
import type { Pane } from '../../api/types'
import { useConnectionStore } from '../../stores/connection'
import { safeStorage } from '../../utils/storage'
import { operationFields, sessionError, sessionOK, worktreeLists } from '../../utils/session'

const props = defineProps<{
  spaceId: string
  label: string
  host: string
  linked: boolean
  pane: Pane | null
  mode: 'workspace' | 'pane'
  index: number
  count: number
  canManage: boolean
  canWorktree: boolean
}>()
const emit = defineEmits<{ close: []; move: [dir: -1 | 1] }>()

const AGENTS = ['shell', 'claude', 'codex', 'cursor', 'grok', 'gemini', 'opencode', 'pi'] as const
const AGENT_KEY = 'herdrem_worktree_agent'

type Dialog = '' | 'rename' | 'close' | 'create' | 'open' | 'remove' | 'rename-pane' | 'rename-tab' | 'close-pane' | 'close-tab'
const dialog = ref<Dialog>('')
const draft = ref('')
const branch = ref('')
const agent = ref<string>(safeStorage.getItem(AGENT_KEY) || 'shell')
const waiting = ref('')
const removeError = ref('')
const formError = ref('')

const conn = useConnectionStore()
const canCommit = computed(() => !!conn.bootId)
const entries = computed(() => worktreeLists.value[props.spaceId] || [])
const titleId = 'session-dialog-title'

const dialogTitle = computed(() => {
  switch (dialog.value) {
    case 'rename': return 'Rename workspace'
    case 'rename-pane': return 'Rename pane'
    case 'rename-tab': return 'Rename tab'
    case 'close': return 'Close workspace'
    case 'close-pane': return 'Close pane'
    case 'close-tab': return 'Close tab'
    case 'create': return 'New worktree'
    case 'open': return 'Open worktree'
    case 'remove': return 'Remove worktree'
    default: return 'Workspace actions'
  }
})

watch(sessionError, (err) => {
  if (!err || err.op !== waiting.value) return
  if (err.op === 'worktree_remove') {
    removeError.value = err.message
    return
  }
  formError.value = err.message
  if (err.op === 'worktree_create') waiting.value = ''
})

watch(sessionOK, (ok) => {
  if (!ok || ok.op !== waiting.value) return
  emit('close')
})

function requireOp() {
  const fields = operationFields()
  if (!fields.operation_id || !fields.boot_id) return null
  return fields
}

function openDialog(next: Dialog) {
  dialog.value = next
  draft.value = next === 'rename' ? props.label : next === 'rename-pane' ? (props.pane?.label || '') : next === 'rename-tab' ? (props.pane?.tab_label || '') : ''
  removeError.value = ''
  formError.value = ''
  waiting.value = ''
  if (next === 'open') {
    ws.send({ type: 'worktree_list', workspace_id: props.spaceId, host: props.host })
  }
}

function submitRename() {
  const label = draft.value.trim()
  if (!label) return
  const target = dialog.value === 'rename-pane' ? 'pane' : dialog.value === 'rename-tab' ? 'tab' : 'workspace'
  const id = target === 'pane' ? props.pane?.pane_id : target === 'tab' ? props.pane?.tab_id : props.spaceId
  if (!id) return
  waiting.value = 'session_rename'
  ws.send({ type: 'session_rename', target, id, label, host: props.host })
}

function clearPaneName() {
  if (!props.pane) return
  waiting.value = 'session_rename'
  ws.send({ type: 'session_rename', target: 'pane', id: props.pane.pane_id, host: props.host, clear: true })
}

function submitClose() {
  const fields = requireOp()
  if (!fields) return
  if (dialog.value === 'close-pane' && props.pane) {
    waiting.value = 'session_close'
    ws.send({ type: 'session_close', target: 'pane', id: props.pane.pane_id, host: props.host, ...fields })
    return
  }
  if (dialog.value === 'close-tab' && props.pane?.tab_id) {
    waiting.value = 'session_close'
    ws.send({ type: 'session_close', target: 'tab', id: props.pane.tab_id, host: props.host, ...fields })
    return
  }
  if (dialog.value !== 'close') return
  waiting.value = 'session_close'
  ws.send({
    type: 'session_close',
    target: 'workspace',
    id: props.spaceId,
    host: props.host,
    group: !props.linked,
    ...fields,
  })
}

function submitCreate() {
  if (waiting.value === 'worktree_create') return
  formError.value = ''
  const kind = agent.value || 'shell'
  safeStorage.setItem(AGENT_KEY, kind)
  waiting.value = 'worktree_create'
  ws.send({
    type: 'worktree_create',
    workspace_id: props.spaceId,
    host: props.host,
    branch: branch.value.trim() || undefined,
    agent: kind,
    ...operationFields(),
  })
}

function submitOpen(path: string) {
  waiting.value = 'worktree_open'
  ws.send({ type: 'worktree_open', workspace_id: props.spaceId, host: props.host, path })
}

function submitRemove(force: boolean) {
  const fields = requireOp()
  if (!fields) return
  const id = props.mode === 'workspace' ? props.spaceId : (props.pane?.workspace_id || props.spaceId)
  waiting.value = 'worktree_remove'
  if (!force) removeError.value = ''
  ws.send({ type: 'worktree_remove', workspace_id: id, host: props.host, force: force || undefined, ...fields })
}
</script>

<template>
  <div class="session-scrim" @click.self="emit('close')">
    <div v-if="!dialog" class="session-sheet" role="menu" aria-label="Workspace actions">
      <template v-if="mode === 'workspace'">
        <button v-if="canManage" type="button" role="menuitem" data-action="rename" @click="openDialog('rename')">Rename</button>
        <button v-if="canManage && index > 0" type="button" role="menuitem" data-action="move-up" @click="emit('move', -1)">Move up</button>
        <button v-if="canManage && index < count - 1" type="button" role="menuitem" data-action="move-down" @click="emit('move', 1)">Move down</button>
        <button v-if="canWorktree" type="button" role="menuitem" data-action="create-worktree" @click="openDialog('create')">New worktree</button>
        <button v-if="canWorktree" type="button" role="menuitem" data-action="open-worktree" @click="openDialog('open')">Open worktree</button>
        <button v-if="canManage" type="button" role="menuitem" data-action="close" @click="openDialog('close')">Close workspace</button>
        <button v-if="canWorktree && linked" type="button" role="menuitem" data-action="remove" @click="openDialog('remove')">Remove worktree</button>
      </template>
      <template v-else>
        <button v-if="canManage" type="button" role="menuitem" data-action="rename-pane" @click="openDialog('rename-pane')">Rename pane</button>
        <button v-if="canManage && pane?.tab_id" type="button" role="menuitem" data-action="rename-tab" @click="openDialog('rename-tab')">Rename tab</button>
        <button v-if="canManage" type="button" role="menuitem" data-action="close-pane" @click="openDialog('close-pane')">Close pane</button>
        <button v-if="canManage && pane?.tab_id" type="button" role="menuitem" data-action="close-tab" @click="openDialog('close-tab')">Close tab</button>
      </template>
      <button type="button" role="menuitem" data-action="dismiss" @click="emit('close')">Cancel</button>
    </div>

    <div v-else class="session-sheet" role="dialog" aria-modal="true" :aria-labelledby="titleId">
      <h2 :id="titleId" class="session-title">{{ dialogTitle }}</h2>
      <pre v-if="formError" class="session-error">{{ formError }}</pre>

      <form v-if="dialog === 'rename' || dialog === 'rename-pane' || dialog === 'rename-tab'" @submit.prevent="submitRename">
        <label class="session-label">Name <input v-model="draft" name="label" required maxlength="200" /></label>
        <div class="session-actions">
          <button type="button" @click="dialog = ''">Back</button>
          <button v-if="dialog === 'rename-pane'" type="button" data-action="clear-pane" @click="clearPaneName">Clear name</button>
          <button type="submit" data-action="save-rename">Save</button>
        </div>
      </form>

      <div v-else-if="dialog === 'close' || dialog === 'close-pane' || dialog === 'close-tab'">
        <p v-if="dialog === 'close' && !linked">Close this workspace and the workspaces of its open worktrees. Checkouts on disk stay.</p>
        <p v-else-if="dialog === 'close'">Close this worktree workspace. The checkout stays. To delete the checkout, use Remove worktree.</p>
        <p v-else>Close this {{ dialog === 'close-tab' ? 'tab' : 'pane' }}. This cannot be undone from here.</p>
        <div class="session-actions">
          <button type="button" @click="dialog = ''">Back</button>
          <button type="button" data-action="confirm-close" :disabled="!canCommit" @click="submitClose">Close</button>
        </div>
      </div>

      <form v-else-if="dialog === 'create'" @submit.prevent="submitCreate">
        <label class="session-label">Branch
          <input v-model="branch" name="branch" maxlength="200" placeholder="worktree/adjective-noun-hex" />
        </label>
        <label class="session-label">Agent
          <select v-model="agent" name="agent">
            <option v-for="name in AGENTS" :key="name" :value="name">{{ name === 'shell' ? 'Shell' : name }}</option>
          </select>
        </label>
        <p class="session-note">Shell starts a checkout and does not launch an agent. The new workspace does not take focus on the desktop.</p>
        <p v-if="waiting === 'worktree_create'" class="session-note" role="status">Creating the worktree. This can take a minute.</p>
        <div class="session-actions">
          <button type="button" :disabled="waiting === 'worktree_create'" @click="dialog = ''">Back</button>
          <button type="submit" data-action="confirm-create" :disabled="waiting === 'worktree_create'">{{ waiting === 'worktree_create' ? 'Creating…' : 'Create' }}</button>
        </div>
      </form>

      <div v-else-if="dialog === 'open'">
        <p v-if="entries.length === 0" class="session-note">No worktrees yet.</p>
        <ul v-else class="session-list">
          <li v-for="row in entries" :key="row.path">
            <span>{{ row.label || row.branch || row.path }}</span>
            <button type="button" data-action="confirm-open" @click="submitOpen(row.path)">Open</button>
          </li>
        </ul>
        <div class="session-actions">
          <button type="button" @click="dialog = ''">Back</button>
        </div>
      </div>

      <div v-else-if="dialog === 'remove'">
        <p>Remove the checkout. The branch stays.</p>
        <pre v-if="removeError" class="session-error">{{ removeError }}</pre>
        <div class="session-actions">
          <button type="button" @click="dialog = ''">Back</button>
          <button v-if="!removeError" type="button" data-action="confirm-remove" :disabled="!canCommit" @click="submitRemove(false)">Remove</button>
          <button v-else type="button" data-action="force-remove" :disabled="!canCommit" @click="submitRemove(true)">Delete anyway</button>
        </div>
      </div>
    </div>
  </div>
</template>

<style scoped>
.session-scrim {
  position: fixed; inset: 0; z-index: 40;
  background: rgba(0, 0, 0, 0.35);
  display: flex; align-items: flex-end; justify-content: center;
}
.session-sheet {
  width: min(420px, 100%);
  background: var(--surface);
  color: var(--text);
  border: 1px solid var(--border);
  border-radius: var(--radius-lg) var(--radius-lg) 0 0;
  padding: 12px;
  display: flex; flex-direction: column; gap: 8px;
}
.session-sheet button, .session-sheet input, .session-sheet select {
  font: inherit; color: inherit;
}
.session-sheet button {
  text-align: left;
  background: var(--surface-2);
  border: 1px solid var(--border);
  border-radius: var(--radius);
  padding: 8px 10px;
  cursor: pointer;
}
.session-sheet button:disabled { opacity: 0.45; cursor: not-allowed; }
.session-title { margin: 0; font-size: var(--text-md); }
.session-label { display: flex; flex-direction: column; gap: 4px; font-size: var(--text-sm); }
.session-label input, .session-label select {
  background: var(--surface-2);
  border: 1px solid var(--border);
  border-radius: var(--radius);
  padding: 6px 8px;
}
.session-actions { display: flex; justify-content: flex-end; gap: 8px; }
.session-actions button { text-align: center; }
.session-note, .session-sheet p { margin: 0; color: var(--muted); font-size: var(--text-sm); }
.session-list { list-style: none; margin: 0; padding: 0; display: flex; flex-direction: column; gap: 6px; }
.session-list li { display: flex; align-items: center; justify-content: space-between; gap: 8px; }
.session-error {
  margin: 0; white-space: pre-wrap;
  font-family: var(--font-mono); font-size: var(--text-xs);
  color: var(--red); background: var(--red-soft);
  border-radius: var(--radius); padding: 8px;
}
</style>
