<script setup lang="ts">
import { computed, onMounted, ref } from 'vue';
import { useAgentsStore, bucket } from '../../stores/connection';
import { useTerminalStore } from '../../stores/terminal';
import { formatLineRef, buildCommentText } from '../../utils/lineRef';

const props = defineProps<{
  /** File reference shown to the user and sent to the agent */
  path: string;
  start: number;
  end: number;
  paneId: string;
}>();

const emit = defineEmits<{ close: []; sent: [] }>();

const agents = useAgentsStore();
const term = useTerminalStore();

const comment = ref('');
const error = ref('');
const inputEl = ref<HTMLInputElement | null>(null);

const refLabel = computed(() => formatLineRef(props.path, props.start, props.end));
const pane = computed(() => agents.agents.find((a) => a.pane_id === props.paneId) || null);
const blocked = computed(() => !!pane.value && bucket(pane.value.status) === 'blocked');
const disabledReason = computed(() => {
  if (!pane.value) return 'Pane unavailable';
  if (blocked.value) return 'Pane is waiting for approval — send the comment after resolving it';
  return '';
});
const canSend = computed(() => !!comment.value.trim() && !disabledReason.value);

function submit() {
  const text = comment.value.trim();
  if (!text || disabledReason.value) return;
  const ok = term.sendText(buildCommentText(props.path, props.start, props.end, text), true, {
    paneId: props.paneId,
  });
  if (!ok) {
    error.value = 'Send failed — not connected';
    return;
  }
  emit('sent');
  emit('close');
}

onMounted(() => {
  inputEl.value?.focus();
});
</script>

<template>
  <div class="lcb" role="form" aria-label="Send a comment about the selected lines">
    <span class="lcb-ref mono" :title="refLabel">{{ refLabel }}</span>
    <input
      ref="inputEl"
      v-model="comment"
      class="lcb-input"
      type="text"
      placeholder="Comment for the agent…"
      aria-label="Comment for the agent"
      :disabled="!!disabledReason"
      @keydown.enter.prevent="submit"
      @keydown.escape.prevent="emit('close')"
    />
    <button
      type="button"
      class="lcb-send"
      :disabled="!canSend"
      :title="disabledReason || 'Send to agent'"
      @click="submit"
    >
      Send
    </button>
    <button type="button" class="lcb-close" aria-label="Clear selection" @click="emit('close')">×</button>
    <p v-if="disabledReason" class="lcb-note" role="status">{{ disabledReason }}</p>
    <p v-else-if="error" class="lcb-note err" role="alert">{{ error }}</p>
  </div>
</template>

<style scoped>
.lcb {
  display: flex;
  align-items: center;
  gap: 8px;
  padding: 8px 12px;
  border-top: 1px solid var(--border);
  background: var(--surface);
  flex-wrap: wrap;
  flex-shrink: 0;
}
.lcb-ref {
  font-size: var(--text-xs);
  color: var(--blue);
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
  max-width: 40%;
}
.lcb-input {
  flex: 1;
  min-width: 120px;
  padding: 5px 8px;
  font: inherit;
  font-size: var(--text-xs);
  color: var(--text);
  background: var(--bg);
  border: 1px solid var(--border);
  border-radius: var(--radius);
}
.lcb-input:focus {
  outline: none;
  border-color: var(--blue);
}
.lcb-send {
  font-family: var(--font-mono);
  font-size: var(--text-2xs);
  color: var(--text);
  background: var(--blue-soft);
  border: 1px solid rgba(96, 165, 250, 0.4);
  border-radius: var(--radius);
  padding: 4px 10px;
}
.lcb-send:disabled {
  opacity: 0.45;
  cursor: not-allowed;
}
.lcb-close {
  border: none;
  background: transparent;
  color: var(--muted);
  font-size: 16px;
  padding: 0 2px;
}
.lcb-note {
  flex-basis: 100%;
  margin: 2px 0 0;
  font-size: var(--text-2xs);
  color: var(--muted);
}
.lcb-note.err {
  color: var(--red, #f87171);
}
</style>
