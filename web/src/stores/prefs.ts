import { defineStore } from 'pinia'
import { ref } from 'vue'
import { ws } from '../api/ws'
import type { Prefs, PrefsSet, Skill, TimingPrefs } from '../api/types'

const DEFAULT_SHELL_CMDS = ['ls -la', 'git status', 'git diff', 'cat package.json', 'npm test', 'make build']
const DEFAULT_AGENT_CMDS = ['/review', '/commit', '/help', '/clear', '/compact']
const DEFAULT_PREFIX_BY_AGENT: Record<string, string> = { codex: '$', default: '/' }
export const DEFAULT_TERM_POLL_MS = 6000
// timing section defaults — must match the poller/ws built-ins; a 0 (absent)
// value in prefs means the same default.
export const DEFAULT_SNAPSHOT_POLL_MS = 3000
export const DEFAULT_ENRICH_GRACE_MS = 30000
export const DEFAULT_HOST_RETRY_MS = 30000
export const DEFAULT_RECONNECT_DELAY_MS = 2000
export const DEFAULT_GRID_PREVIEW_MS = 8000
export const DEFAULT_AUTO_MARK_READ_MS = 10000

// prefs store mirrors the relay's prefs (skills / agent_cmds / shell_cmds / kind_overrides / prefix_by_agent).
export const usePrefsStore = defineStore('prefs', () => {
  const skills = ref<Skill[]>([])
  const agentCmds = ref<string[]>([...DEFAULT_AGENT_CMDS])
  const shellCmds = ref<string[]>([...DEFAULT_SHELL_CMDS])
  const kindOverrides = ref<Record<string, { kind: string; display_agent: string; updated_at: string }>>({})
  const prefixByAgent = ref<Record<string, string>>({ ...DEFAULT_PREFIX_BY_AGENT })
  const termPollMs = ref(DEFAULT_TERM_POLL_MS)
  const snapshotPollMs = ref(DEFAULT_SNAPSHOT_POLL_MS)
  const enrichGraceMs = ref(DEFAULT_ENRICH_GRACE_MS)
  const hostRetryMs = ref(DEFAULT_HOST_RETRY_MS)
  const reconnectDelayMs = ref(DEFAULT_RECONNECT_DELAY_MS)
  const gridPreviewMs = ref(DEFAULT_GRID_PREVIEW_MS)
  const autoMarkReadMs = ref(DEFAULT_AUTO_MARK_READ_MS)
  /** custom commit command (relay prefs, shared by all devices); '' = ask for a message */
  const gitCommitCmd = ref('')

  function setPrefs(p: Prefs) {
    if (p.skills) skills.value = p.skills
    if (p.agent_cmds) agentCmds.value = p.agent_cmds
    if (p.shell_cmds) shellCmds.value = p.shell_cmds
    if (p.kind_overrides) kindOverrides.value = p.kind_overrides
    if (p.prefix_by_agent) prefixByAgent.value = p.prefix_by_agent
    termPollMs.value = p.terminal?.poll_ms || DEFAULT_TERM_POLL_MS
    snapshotPollMs.value = p.timing?.snapshot_poll_ms || DEFAULT_SNAPSHOT_POLL_MS
    enrichGraceMs.value = p.timing?.enrich_grace_ms || DEFAULT_ENRICH_GRACE_MS
    hostRetryMs.value = p.timing?.host_retry_ms || DEFAULT_HOST_RETRY_MS
    reconnectDelayMs.value = p.timing?.reconnect_delay_ms || DEFAULT_RECONNECT_DELAY_MS
    gridPreviewMs.value = p.timing?.grid_preview_ms || DEFAULT_GRID_PREVIEW_MS
    autoMarkReadMs.value = p.timing?.auto_mark_read_ms || DEFAULT_AUTO_MARK_READ_MS
    gitCommitCmd.value = p.git?.commit_cmd || ''
    ws.setReconnectDelay(reconnectDelayMs.value)
  }

  // Sections whose prefs_set has been sent but not yet confirmed by the
  // relay's prefs_updated broadcast. Drives save-button feedback. Pending is
  // tracked per request id: a multi-section save (timing writes terminal +
  // timing) must wait for every ack, another client's broadcast must not
  // clear our pending, and a late failure must still surface as error.
  const pendingSaves = ref<Set<string>>(new Set())
  const pendingByReq = new Map<string, string[]>()
  const saveError = ref('')
  let saveSeq = 0

  function rebuildPending() {
    pendingSaves.value = new Set(Array.from(pendingByReq.values()).flat())
  }

  function setSection(section: 'skills' | 'agent_cmds' | 'shell_cmds' | 'kind_overrides' | 'prefix_by_agent' | 'terminal' | 'timing' | 'git', data: unknown) {
    const reqId = `save-${Date.now().toString(36)}-${++saveSeq}`
    const msg: PrefsSet = { type: 'prefs_set', section, data, req_id: reqId }
    if (!ws.send(msg)) return false
    saveError.value = ''
    pendingByReq.set(reqId, [section])
    rebuildPending()
    return true
  }

  /** A prefs_updated echoing a save's req_id confirms that request's sections. */
  function confirmSaves(reqId?: string) {
    if (!reqId) return // unattributed broadcast can't prove a pending save landed
    if (pendingByReq.delete(reqId)) rebuildPending()
  }

  /** Server rejected a prefs_set — surface the reason to the save buttons. */
  function failSaves(message: string, reqId?: string) {
    if (reqId) pendingByReq.delete(reqId)
    else pendingByReq.clear()
    rebuildPending()
    saveError.value = message
  }

  function setShellCmds(list: string[]) {
    shellCmds.value = list
    return setSection('shell_cmds', list)
  }

  function setAgentCmds(list: string[]) {
    agentCmds.value = list
    return setSection('agent_cmds', list)
  }

  function setSkills(list: Skill[]) {
    skills.value = list
    return setSection('skills', list)
  }

  function setPrefixByAgent(map: Record<string, string>) {
    prefixByAgent.value = map
    return setSection('prefix_by_agent', map)
  }

  function setTermPollMs(ms: number) {
    termPollMs.value = ms || DEFAULT_TERM_POLL_MS
    return setSection('terminal', { poll_ms: ms })
  }

  /** Save the whole timing section; patch keys are TimingPrefs field names. */
  function setTiming(patch: Partial<TimingPrefs>) {
    const merged: TimingPrefs = {
      snapshot_poll_ms: snapshotPollMs.value,
      enrich_grace_ms: enrichGraceMs.value,
      host_retry_ms: hostRetryMs.value,
      reconnect_delay_ms: reconnectDelayMs.value,
      grid_preview_ms: gridPreviewMs.value,
      auto_mark_read_ms: autoMarkReadMs.value,
      ...patch,
    }
    snapshotPollMs.value = merged.snapshot_poll_ms || DEFAULT_SNAPSHOT_POLL_MS
    enrichGraceMs.value = merged.enrich_grace_ms || DEFAULT_ENRICH_GRACE_MS
    hostRetryMs.value = merged.host_retry_ms || DEFAULT_HOST_RETRY_MS
    reconnectDelayMs.value = merged.reconnect_delay_ms || DEFAULT_RECONNECT_DELAY_MS
    gridPreviewMs.value = merged.grid_preview_ms || DEFAULT_GRID_PREVIEW_MS
    autoMarkReadMs.value = merged.auto_mark_read_ms || DEFAULT_AUTO_MARK_READ_MS
    return setSection('timing', merged)
  }

  /** Set (or with '' clear) the custom commit command. */
  function setGitCommitCmd(cmd: string) {
    gitCommitCmd.value = cmd.trim()
    return setSection('git', { commit_cmd: gitCommitCmd.value })
  }

  /** resolve the prefix for a given agent name from prefix_by_agent config */
  function prefixForAgent(agentName: string): string {
    const a = (agentName || '').toLowerCase().trim()
    const table = prefixByAgent.value
    // try exact match, then includes match, then default
    if (table[a]) return table[a]
    for (const [key, val] of Object.entries(table)) {
      if (key === 'default') continue
      if (a.includes(key)) return val
    }
    return table.default || '/'
  }

  /** resolve a skill template's {prefix} for a given agent */
  function resolveSkill(template: string, prefix: string): string {
    return template.replace(/\{prefix\}/g, prefix)
  }

  function kindOverrideFor(host: string, paneId: string) {
    const key = `${host || 'local'}:${paneId}`
    return kindOverrides.value[key] || null
  }

  return {
    skills, agentCmds, shellCmds, kindOverrides, prefixByAgent, termPollMs,
    gitCommitCmd, setGitCommitCmd,
    snapshotPollMs, enrichGraceMs, hostRetryMs, reconnectDelayMs, gridPreviewMs, autoMarkReadMs,
    pendingSaves, saveError, confirmSaves, failSaves,
    setPrefs, setSection, setShellCmds, setAgentCmds, setSkills, setPrefixByAgent, setTermPollMs, setTiming,
    prefixForAgent, resolveSkill, kindOverrideFor,
  }
})
