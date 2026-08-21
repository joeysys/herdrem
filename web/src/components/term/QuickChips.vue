<script setup lang="ts">
import { computed, ref, onMounted, onUnmounted } from 'vue'
import { usePrefsStore } from '../../stores/prefs'
import { useHaptic } from '../../composables/useHaptic'

const props = defineProps<{ agentName?: string; mode?: 'agent' | 'shell' }>()
const emit = defineEmits<{ fill: [text: string]; edit: [] }>()
const prefs = usePrefsStore()
const { haptic } = useHaptic()

const showSkills = computed(() => props.mode !== 'shell')
const showCmds = computed(() => props.mode !== 'agent')

const skillChips = computed(() =>
  prefs.skills.map((s) => {
    const prefix = prefs.prefixForAgent(props.agentName || '')
    return {
      label: s.label.replace(/\{prefix\}/g, '').trim() || s.label,
      text: prefs.resolveSkill(s.template, prefix),
    }
  }),
)

const openPopover = ref<'skills' | 'cmds' | null>(null)

function togglePopover(which: 'skills' | 'cmds') {
  haptic()
  openPopover.value = openPopover.value === which ? null : which
}

function fill(text: string) {
  haptic()
  emit('fill', text)
  openPopover.value = null
}

function onDocClick(e: MouseEvent) {
  const target = e.target as HTMLElement
  if (!target.closest('.qc-pop-wrap')) openPopover.value = null
}
onMounted(() => document.addEventListener('click', onDocClick))
onUnmounted(() => document.removeEventListener('click', onDocClick))
</script>

<template>
  <div class="quick-chips">
    <div v-if="showSkills" class="qc-pop-wrap">
      <button
        class="qc-btn skills"
        :class="{ on: openPopover === 'skills' }"
        :disabled="!skillChips.length"
        aria-label="Skills"
        title="Skills"
        @click.stop="togglePopover('skills')"
      >⚡</button>
      <div v-if="openPopover === 'skills'" class="qc-pop" @click.stop>
        <button
          v-for="(s, i) in skillChips"
          :key="'sk' + i"
          class="qc-item"
          :title="s.text"
          @click="fill(s.text)"
        >{{ s.label }}</button>
        <button class="qc-edit" @click="emit('edit'); openPopover = null">✎ edit skills</button>
      </div>
    </div>

    <div v-if="showCmds" class="qc-pop-wrap">
      <button
        class="qc-btn cmds"
        :class="{ on: openPopover === 'cmds' }"
        :disabled="!prefs.shellCmds.length"
        aria-label="Commands"
        title="Commands"
        @click.stop="togglePopover('cmds')"
      >▶</button>
      <div v-if="openPopover === 'cmds'" class="qc-pop" @click.stop>
        <button
          v-for="(c, i) in prefs.shellCmds"
          :key="'sc' + i"
          class="qc-item"
          :title="c"
          @click="fill(c)"
        >{{ c.length > 30 ? c.slice(0, 30) + '…' : c }}</button>
        <button class="qc-edit" @click="emit('edit'); openPopover = null">✎ edit commands</button>
      </div>
    </div>
  </div>
</template>

<style scoped>
.quick-chips {
  display: flex;
  gap: 6px;
  align-items: flex-start;
  padding-bottom: 8px;
  margin-bottom: 2px;
}
.qc-pop-wrap { position: relative; }

.qc-btn {
  width: 32px; height: 28px;
  display: grid; place-items: center;
  font-size: var(--text-sm);
  border-radius: var(--radius);
  border: 1px solid var(--border);
  color: var(--muted);
  background: var(--surface-2);
  flex-shrink: 0;
  transition: color 0.15s, border-color 0.15s, background 0.15s;
}
.qc-btn:hover:not(:disabled) { color: var(--text); border-color: var(--border-strong); }
.qc-btn:disabled { opacity: 0.35; cursor: default; }
.qc-btn.skills.on { color: var(--amber); border-color: rgba(251,191,36,.5); background: var(--amber-soft); }
.qc-btn.cmds.on { color: var(--blue); border-color: rgba(96,165,250,.5); background: var(--blue-soft); }

.qc-pop {
  position: absolute;
  bottom: calc(100% + 6px);
  left: 0;
  z-index: 40;
  min-width: 160px;
  max-width: 280px;
  max-height: 240px;
  overflow-y: auto;
  background: var(--surface);
  border: 1px solid var(--border-strong);
  border-radius: var(--radius-lg);
  box-shadow: var(--shadow-2);
  padding: 5px;
  animation: view-in 0.15s ease both;
}
.qc-item {
  display: block;
  width: 100%;
  text-align: left;
  font-family: var(--font-mono);
  font-size: var(--text-xs);
  color: var(--text-dim);
  background: transparent;
  border: none;
  border-radius: var(--radius);
  padding: 7px 10px;
  cursor: pointer;
  white-space: nowrap;
  overflow: hidden;
  text-overflow: ellipsis;
  transition: background 0.12s, color 0.12s;
}
.qc-item:hover { background: var(--surface-2); color: var(--text); }

.qc-edit {
  display: block;
  width: 100%;
  text-align: left;
  font-family: var(--font-mono);
  font-size: var(--text-2xs);
  color: var(--muted);
  background: transparent;
  border: none;
  border-top: 1px dashed var(--border);
  border-radius: 0 0 var(--radius) var(--radius);
  padding: 7px 10px;
  cursor: pointer;
  margin-top: 2px;
}
.qc-edit:hover { color: var(--text); background: var(--surface-2); }
</style>
