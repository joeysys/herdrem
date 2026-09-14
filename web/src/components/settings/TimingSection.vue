<script setup lang="ts">
import { ref, watch } from 'vue'
import { usePrefsStore } from '../../stores/prefs'
import { useSaveFeedback } from '../../composables/useSaveFeedback'

const prefs = usePrefsStore()

// Six tunables live here; grid preview refresh is stored in prefs but hidden
// until the wall/monitor feature lands. Values are milliseconds on the wire,
// seconds in the UI.
const timingOpen = ref(false)

// saveTiming writes two prefs sections (terminal + timing) — the button is
// 'saving' until the broadcast confirms both.
const { state: saveState, label: saveLabel, save } = useSaveFeedback('terminal', 'timing')

function sec(v: number) { return String(v / 1000) }
function clampSec(text: string, min: number, max: number): number | null {
  const v = parseFloat(text)
  if (!isFinite(v) || v <= 0) return null
  return Math.round(Math.min(max, Math.max(min, v)) * 1000)
}

const termPollSec = ref(sec(prefs.termPollMs))
const snapshotPollSec = ref(sec(prefs.snapshotPollMs))
const enrichGraceSec = ref(sec(prefs.enrichGraceMs))
const hostRetrySec = ref(sec(prefs.hostRetryMs))
const reconnectDelaySec = ref(sec(prefs.reconnectDelayMs))
const autoMarkReadSec = ref(sec(prefs.autoMarkReadMs))

watch(
  () => [prefs.termPollMs, prefs.snapshotPollMs, prefs.enrichGraceMs, prefs.hostRetryMs, prefs.reconnectDelayMs, prefs.autoMarkReadMs],
  ([term, snap, grace, retry, recon, autoRead]) => {
    termPollSec.value = sec(term)
    snapshotPollSec.value = sec(snap)
    enrichGraceSec.value = sec(grace)
    hostRetrySec.value = sec(retry)
    reconnectDelaySec.value = sec(recon)
    autoMarkReadSec.value = sec(autoRead)
  },
)

function saveTiming() {
  const term = clampSec(termPollSec.value, 1, 60)
  const snap = clampSec(snapshotPollSec.value, 0.5, 60)
  const grace = clampSec(enrichGraceSec.value, 5, 600)
  const retry = clampSec(hostRetrySec.value, 5, 600)
  const recon = clampSec(reconnectDelaySec.value, 0.2, 30)
  const autoRead = clampSec(autoMarkReadSec.value, 1, 600)
  // Revert invalid inputs to the current effective values.
  if (term === null) termPollSec.value = sec(prefs.termPollMs)
  if (snap === null) snapshotPollSec.value = sec(prefs.snapshotPollMs)
  if (grace === null) enrichGraceSec.value = sec(prefs.enrichGraceMs)
  if (retry === null) hostRetrySec.value = sec(prefs.hostRetryMs)
  if (recon === null) reconnectDelaySec.value = sec(prefs.reconnectDelayMs)
  if (autoRead === null) autoMarkReadSec.value = sec(prefs.autoMarkReadMs)
  if (term !== null) prefs.setTermPollMs(term)
  return prefs.setTiming({
    snapshot_poll_ms: snap ?? prefs.snapshotPollMs,
    enrich_grace_ms: grace ?? prefs.enrichGraceMs,
    host_retry_ms: retry ?? prefs.hostRetryMs,
    reconnect_delay_ms: recon ?? prefs.reconnectDelayMs,
    auto_mark_read_ms: autoRead ?? prefs.autoMarkReadMs,
  })
}
</script>

<template>
  <button
    class="ts-fold"
    :aria-expanded="timingOpen"
    aria-controls="timing-body"
    @click="timingOpen = !timingOpen"
  >
    <h3>timing</h3>
    <span class="ts-fold-arrow" aria-hidden="true">{{ timingOpen ? '▴' : '▾' }}</span>
  </button>
  <div v-show="timingOpen" id="timing-body">
    <p class="ts-hint">all values in seconds, stored on the relay</p>
    <div class="t-row">
      <label class="t-label" for="t-term">terminal refresh</label>
      <input id="t-term" v-model="termPollSec" class="ts-num" type="number" min="1" max="60" step="0.5" />
      <span class="t-hint">open terminal re-reads the pane (1–60)</span>
    </div>
    <div class="t-row">
      <label class="t-label" for="t-snap">snapshot poll</label>
      <input id="t-snap" v-model="snapshotPollSec" class="ts-num" type="number" min="0.5" max="60" step="0.5" />
      <span class="t-hint">agents snapshot tick — drives status/blocked detection (0.5–60)</span>
    </div>
    <div class="t-row">
      <label class="t-label" for="t-grace">enrichment grace</label>
      <input id="t-grace" v-model="enrichGraceSec" class="ts-num" type="number" min="5" max="600" step="1" />
      <span class="t-hint">keep path/menu/delta work running this long after last client leaves (5–600)</span>
    </div>
    <div class="t-row">
      <label class="t-label" for="t-retry">host retry backoff</label>
      <input id="t-retry" v-model="hostRetrySec" class="ts-num" type="number" min="5" max="600" step="1" />
      <span class="t-hint">wait this long before retrying a host whose snapshot failed (5–600)</span>
    </div>
    <div class="t-row">
      <label class="t-label" for="t-recon">ws reconnect delay</label>
      <input id="t-recon" v-model="reconnectDelaySec" class="ts-num" type="number" min="0.2" max="30" step="0.1" />
      <span class="t-hint">browser reconnect cadence after the socket drops (0.2–30)</span>
    </div>
    <div class="t-row">
      <label class="t-label" for="t-autoread">done auto-read</label>
      <input id="t-autoread" v-model="autoMarkReadSec" class="ts-num" type="number" min="1" max="600" step="1" />
      <span class="t-hint">viewing a finished pane this long auto-marks it idle (1–600)</span>
    </div>
    <button
      class="ts-save"
      :class="{ err: saveState === 'error' }"
      :disabled="saveState === 'saving'"
      @click="save(saveTiming)"
    >{{ saveLabel }}</button>
  </div>
</template>

<style scoped>
h3 {
  font-size: var(--text-sm); font-weight: 700; text-transform: uppercase;
  letter-spacing: 0.12em; color: var(--muted); margin-bottom: 9px;
}
.ts-hint { font-size: var(--text-2xs); color: var(--faint); margin-bottom: 6px; }
.ts-num {
  width: 110px; font-family: var(--font-mono); font-size: var(--text-xs);
  background: var(--term-bg); border: 1px solid var(--border); border-radius: var(--radius);
  color: var(--term-fg); padding: 9px 11px; outline: none;
}
.ts-num:focus { border-color: rgba(96,165,250,.5); }
.ts-fold {
  display: flex; align-items: center; justify-content: space-between; width: 100%;
  background: none; border: none; padding: 0; cursor: pointer; text-align: left;
}
.ts-fold h3 { margin-bottom: 0; }
.ts-fold-arrow { font-size: var(--text-xs); color: var(--muted); }
.t-row { display: flex; align-items: center; gap: 8px; margin-top: 7px; flex-wrap: wrap; }
.t-label { font-family: var(--font-mono); font-size: var(--text-2xs); color: var(--text-dim); width: 132px; flex-shrink: 0; }
.t-hint { font-size: var(--text-2xs); color: var(--faint); flex: 1; min-width: 200px; }
.ts-save {
  margin-top: 7px; font-family: var(--font-mono); font-size: var(--text-xs); font-weight: 700;
  color: #06281a; background: var(--green); border: 1px solid var(--green); border-radius: var(--radius);
  padding: 6px 16px; transition: background 0.15s, opacity 0.15s;
}
.ts-save:disabled { opacity: 0.55; }
.ts-save.err { background: var(--red); border-color: var(--red); color: #fff; }
</style>
