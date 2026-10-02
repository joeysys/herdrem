<script setup lang="ts">
import { computed, onBeforeUnmount, ref } from 'vue'
import { useCmdHistoryStore } from '../../stores/cmdHistory'

// Input history (shell commands + prompts sent to agents): kept on the relay
// when it supports it, otherwise per relay in this browser.
const history = useCmdHistoryStore()
const confirmClear = ref(false)
let timer: ReturnType<typeof setTimeout> | null = null

const plural = (n: number, one: string) => `${n} ${one}${n === 1 ? '' : 's'}`
const summary = computed(() => `${plural(history.counts.sh, 'command')} · ${plural(history.counts.ag, 'prompt')}`)

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

    <div class="hs-card">
      <div class="hs-stats">
        <span class="hs-count" role="status" :aria-label="summary">
          <span><b>{{ history.counts.sh }}</b> {{ history.counts.sh === 1 ? 'command' : 'commands' }}</span>
          <span><b>{{ history.counts.ag }}</b> {{ history.counts.ag === 1 ? 'prompt' : 'prompts' }}</span>
        </span>
        <span class="hs-where" :class="{ local: !history.shared }">
          <i aria-hidden="true"></i>{{ history.shared ? 'on the relay, shared by all devices' : 'in this browser only' }}
        </span>
      </div>
      <p class="hs-note">
        <template v-if="history.shared">
          Saved on the relay as plain text, so every device sees the same history. Clearing it clears it for all of them.
        </template>
        <template v-else>
          This relay is too old to keep history, so it stays in this browser, separately for each relay.
        </template>
      </p>
      <button
        type="button"
        class="hs-clear"
        :class="{ confirm: confirmClear }"
        :disabled="!history.entries.length"
        @click="onClear"
      >{{ confirmClear ? 'tap again to clear' : history.shared ? 'clear history on the relay' : 'clear history for this relay' }}</button>
    </div>

    <label class="hs-toggle">
      <span class="hs-toggle-text">
        <span class="hs-toggle-label">save prompts sent to agents</span>
        <span class="hs-toggle-hint">this device only — shell commands are always saved</span>
      </span>
      <input
        type="checkbox"
        role="switch"
        class="hs-switch"
        :checked="history.recordAgents"
        @change="history.setRecordAgents(($event.target as HTMLInputElement).checked)"
      />
    </label>

    <p class="hs-hint">
      Anything that looks like a password, token or key is never saved. Start a line with a space to skip it.
    </p>
  </div>
</template>

<style scoped>
h3 {
  font-size: var(--text-sm); font-weight: 700; text-transform: uppercase;
  letter-spacing: 0.12em; color: var(--muted); margin-bottom: 10px;
}
.hs-card {
  background: var(--surface); border: 1px solid var(--border); border-radius: var(--radius-lg);
  padding: 12px 14px;
}
.hs-stats { display: flex; align-items: center; justify-content: space-between; gap: 10px; flex-wrap: wrap; }
.hs-count { display: flex; gap: 14px; font-family: var(--font-mono); font-size: var(--text-xs); color: var(--muted); }
.hs-count b { font-size: var(--text-md); font-weight: 700; color: var(--text); margin-right: 2px; }
.hs-where {
  display: inline-flex; align-items: center; gap: 6px;
  font-family: var(--font-mono); font-size: var(--text-2xs); color: var(--green);
  background: color-mix(in srgb, var(--green) 14%, transparent); border-radius: 999px; padding: 3px 9px;
}
.hs-where i { width: 6px; height: 6px; border-radius: 50%; background: currentColor; }
.hs-where.local { color: var(--orange); background: color-mix(in srgb, var(--orange) 14%, transparent); }
.hs-note { font-size: var(--text-2xs); color: var(--faint); margin: 10px 0 12px; line-height: 1.5; }
.hs-clear {
  font-family: var(--font-mono); font-size: var(--text-2xs); font-weight: 600;
  color: var(--red); background: transparent;
  border: 1px solid color-mix(in srgb, var(--red) 40%, transparent); border-radius: var(--radius);
  padding: 6px 12px; cursor: pointer; transition: background 0.15s, color 0.15s;
}
.hs-clear:hover:not(:disabled) { background: var(--red-soft); }
.hs-clear.confirm { color: #fff; background: var(--red); border-color: var(--red); }
.hs-clear:disabled { color: var(--faint); border-color: var(--border); opacity: 0.6; cursor: not-allowed; }

.hs-toggle {
  display: flex; align-items: center; justify-content: space-between; gap: 12px;
  margin-top: 12px; padding: 4px 0; cursor: pointer;
}
.hs-toggle-text { display: flex; flex-direction: column; gap: 2px; min-width: 0; }
.hs-toggle-label { font-size: var(--text-sm); color: var(--text-dim); }
.hs-toggle-hint { font-size: var(--text-2xs); color: var(--faint); }
.hs-switch {
  appearance: none; flex-shrink: 0; position: relative; margin: 0;
  width: 36px; height: 20px; border-radius: 999px; cursor: pointer;
  background: var(--surface-3, var(--surface-2)); border: 1px solid var(--border-strong);
  transition: background 0.15s, border-color 0.15s;
}
.hs-switch::after {
  content: ''; position: absolute; top: 2px; left: 2px; width: 14px; height: 14px;
  border-radius: 50%; background: var(--muted); transition: transform 0.15s, background 0.15s;
}
.hs-switch:checked { background: var(--green); border-color: var(--green); }
.hs-switch:checked::after { transform: translateX(16px); background: #fff; }
.hs-switch:focus-visible { outline: 2px solid var(--blue); outline-offset: 2px; }

.hs-hint { font-size: var(--text-2xs); color: var(--faint); margin-top: 10px; line-height: 1.5; }
</style>
