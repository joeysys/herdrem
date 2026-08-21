// useQuickPopover — shared popover state for skill/cmd buttons in key-row.
// Uses fixed positioning via Teleport to escape overflow-clipped containers.
// Desktop: open on hover (with delayed close to allow mouse to travel to popover);
// Mobile: open on click.
import { ref, onMounted, onUnmounted, computed, nextTick } from 'vue'
import { usePrefsStore } from '../stores/prefs'
import { useHaptic } from './useHaptic'

export type PopoverKind = 'skills' | 'agentCmds' | 'shellCmds' | null

export interface PopoverPos { left: number; bottom: number }

export function useQuickPopover(agentName: () => string, mode: 'agent' | 'shell') {
  const prefs = usePrefsStore()
  const { haptic } = useHaptic()
  const open = ref<PopoverKind>(null)
  const btnEl = ref<HTMLElement | null>(null)
  const pos = ref<PopoverPos>({ left: 0, bottom: 0 })
  let closeTimer: ReturnType<typeof setTimeout> | null = null

  // Desktop = supports hover (fine pointer + hover capable)
  const useHover = ref(
    typeof window !== 'undefined' &&
    window.matchMedia('(hover: hover) and (pointer: fine)').matches,
  )

  const skillItems = computed(() =>
    prefs.skills.map((s) => {
      const prefix = prefs.prefixForAgent(agentName())
      return {
        label: s.label.replace(/\{prefix\}/g, '').trim() || s.label,
        text: prefs.resolveSkill(s.template, prefix),
      }
    }),
  )

  // agent cmds: prepend prefix if not already starting with / or $
  const agentCmdItems = computed(() => {
    const prefix = prefs.prefixForAgent(agentName())
    return prefs.agentCmds.map((c) => {
      if (c.startsWith('/') || c.startsWith('$')) return c
      return prefix + c.replace(/^\{prefix\}/, '')
    })
  })

  const shellCmdItems = computed(() => prefs.shellCmds)

  function computePos() {
    if (!btnEl.value) return
    const r = btnEl.value.getBoundingClientRect()
    pos.value = { left: r.left, bottom: window.innerHeight - r.top + 6 }
  }

  function cancelClose() {
    if (closeTimer) { clearTimeout(closeTimer); closeTimer = null }
  }

  async function toggle(which: PopoverKind) {
    cancelClose()
    haptic()
    if (open.value === which) {
      open.value = null
      return
    }
    open.value = which
    await nextTick()
    computePos()
  }

  // For desktop hover: open immediately
  async function hoverOpen(which: PopoverKind) {
    if (!useHover.value) return
    cancelClose()
    if (open.value === which) return
    open.value = which
    await nextTick()
    computePos()
  }

  // Delayed close so mouse can travel from button to popover
  function hoverClose() {
    if (!useHover.value) return
    cancelClose()
    closeTimer = setTimeout(() => { open.value = null }, 200)
  }

  function close() { cancelClose(); open.value = null }

  function onDocClick(e: MouseEvent) {
    const target = e.target as HTMLElement
    if (btnEl.value?.contains(target)) return
    if (target.closest('.qp-pop')) return
    close()
  }
  onMounted(() => document.addEventListener('click', onDocClick))
  onUnmounted(() => {
    document.removeEventListener('click', onDocClick)
    cancelClose()
  })

  // which buttons to show based on mode
  const showSkills = mode !== 'shell'
  const showAgentCmds = mode !== 'shell'
  const showShellCmds = mode !== 'agent'

  return {
    open, btnEl, pos, toggle, hoverOpen, hoverClose, close, cancelClose, useHover,
    skillItems, agentCmdItems, shellCmdItems,
    showSkills, showAgentCmds, showShellCmds,
  }
}
