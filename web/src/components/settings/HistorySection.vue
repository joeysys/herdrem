<script setup lang="ts">
import { onBeforeUnmount, ref } from 'vue'
import { useCmdHistoryStore } from '../../stores/cmdHistory'

// Local, per-relay input history (shell commands + prompts sent to agents).
const history = useCmdHistoryStore()
const confirmClear = ref(false)
let timer: ReturnType<typeof setTimeout> | null = null

function onClear() {
  // Two-step so a stray tap cannot wipe the history.
  if (!confirmClear.value) {
    confirmClear.value = true
    timer = setTimeout(() => { confirmClear.value = false }, 3000)
    return
  }
  if (timer) clearTimeout(timer)
  confirmClear.value = false
  history.clear()
}
onBeforeUnmount(() => { if (timer) clearTimeout(timer) })
</script>

<template>
  <div class="hs">
    <h3>input history</h3>
    <label class="hs-row">
      <input
        type="checkbox"
        :checked="history.recordAgents"
        @change="history.setRecordAgents(($event.target as HTMLInputElement).checked)"
      />
      <span>save prompts I send to agents (this device)</span>
    </label>
    <div class="hs-row count" role="status">
      <span>this relay</span>
      <span class="v">{{ history.counts.sh }} commands · {{ history.counts.ag }} prompts</span>
    </div>
    <div class="hs-row count">
      <span>stored</span>
      <span class="v" :class="{ warn: !history.shared }">{{ history.shared ? 'on the relay, shared by all devices' : 'in this browser only' }}</span>
    </div>
    <button type="button" class="hs-clear" :class="{ confirm: confirmClear }" :disabled="!history.entries.length" @click="onClear">
      {{ confirmClear ? 'tap again to clear' : history.shared ? 'clear history on the relay' : 'clear history for this relay' }}
    </button>
    <p class="hs-hint">
      <template v-if="history.shared">
        Saved on the relay (plain text, in its data directory), so every device sees the same history.
        Clearing it clears it for all of them.
      </template>
      <template v-else>
        This relay is too old to keep history, so it stays in this browser, separately for each relay.
      </template>
      Anything that looks like a password, token or key is never saved; start a line with a space to skip it.
    </p>
  </div>
</template>

<style scoped>
h3 {
  font-size: var(--text-sm); font-weight: 700; text-transform: uppercase;
  letter-spacing: 0.12em; color: var(--muted); margin-bottom: 10px;
}
.hs-row {
  display: flex; align-items: center; gap: 8px; margin-bottom: 8px;
  font-family: var(--font-mono); font-size: var(--text-xs); color: var(--text-dim);
}
.hs-row.count { justify-content: space-between; color: var(--muted); }
.hs-row .v { color: var(--text-dim); }
.hs-row .v.warn { color: var(--orange); }
.hs-clear {
  font-family: var(--font-mono); font-size: var(--text-xs); font-weight: 600;
  color: var(--text-dim); background: var(--surface-2);
  border: 1px solid var(--border); border-radius: var(--radius);
  padding: 7px 14px; cursor: pointer;
}
.hs-clear:hover:not(:disabled) { color: var(--text); border-color: var(--border-strong); }
.hs-clear.confirm { color: var(--red); border-color: rgba(248, 113, 113, 0.5); }
.hs-clear:disabled { opacity: 0.4; cursor: not-allowed; }
.hs-hint { font-size: var(--text-2xs); color: var(--faint); margin-top: 8px; }
</style>
