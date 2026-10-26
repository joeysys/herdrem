<script setup lang="ts">
import { useWakeLock } from '../../composables/useWakeLock'

const { enabled, setEnabled } = useWakeLock()
</script>

<template>
  <div>
    <h3>screen</h3>
    <label class="wl-toggle">
      <span class="wl-text">
        <span class="wl-label">keep screen on</span>
        <span class="wl-hint">while a terminal is open. needs https or localhost. this device only.</span>
      </span>
      <input
        type="checkbox"
        role="switch"
        class="wl-switch"
        :checked="enabled"
        aria-label="keep screen on"
        @change="setEnabled(($event.target as HTMLInputElement).checked)"
      />
    </label>
  </div>
</template>

<style scoped>
h3 {
  font-size: var(--text-sm); font-weight: 700; text-transform: uppercase;
  letter-spacing: 0.12em; color: var(--muted); margin-bottom: 10px;
}
.wl-toggle {
  display: flex; align-items: center; justify-content: space-between; gap: 12px;
  cursor: pointer;
}
.wl-text { display: flex; flex-direction: column; gap: 2px; min-width: 0; }
.wl-label { font-size: var(--text-sm); color: var(--text-dim); }
.wl-hint { font-size: var(--text-2xs); color: var(--faint); line-height: 1.4; }
.wl-switch {
  appearance: none; flex-shrink: 0; position: relative; margin: 0;
  width: 36px; height: 20px; border-radius: 999px; cursor: pointer;
  background: var(--surface-3, var(--surface-2)); border: 1px solid var(--border-strong);
  transition: background 0.15s, border-color 0.15s;
}
.wl-switch::after {
  content: ''; position: absolute; top: 2px; left: 2px; width: 14px; height: 14px;
  border-radius: 50%; background: var(--muted); transition: transform 0.15s, background 0.15s;
}
.wl-switch:checked { background: var(--green); border-color: var(--green); }
.wl-switch:checked::after { transform: translateX(16px); background: #fff; }
.wl-switch:focus-visible { outline: 2px solid var(--blue); outline-offset: 2px; }
</style>
