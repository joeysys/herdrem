import { defineStore } from 'pinia'
import { ref } from 'vue'
import { ws } from '../api/ws'
import type { Prefs, PrefsSet, Skill } from '../api/types'

const DEFAULT_SHELL_CMDS = ['ls -la', 'git status', 'git diff', 'cat package.json', 'npm test', 'make build']
const DEFAULT_AGENT_CMDS = ['/review', '/commit', '/help', '/clear', '/compact']
const DEFAULT_PREFIX_BY_AGENT: Record<string, string> = { codex: '$', default: '/' }

// prefs store mirrors the relay's prefs (skills / agent_cmds / shell_cmds / kind_overrides / prefix_by_agent).
export const usePrefsStore = defineStore('prefs', () => {
  const skills = ref<Skill[]>([])
  const agentCmds = ref<string[]>([...DEFAULT_AGENT_CMDS])
  const shellCmds = ref<string[]>([...DEFAULT_SHELL_CMDS])
  const kindOverrides = ref<Record<string, { kind: string; display_agent: string; updated_at: string }>>({})
  const prefixByAgent = ref<Record<string, string>>({ ...DEFAULT_PREFIX_BY_AGENT })

  function setPrefs(p: Prefs) {
    if (p.skills) skills.value = p.skills
    if (p.agent_cmds) agentCmds.value = p.agent_cmds
    if (p.shell_cmds) shellCmds.value = p.shell_cmds
    if (p.kind_overrides) kindOverrides.value = p.kind_overrides
    if (p.prefix_by_agent) prefixByAgent.value = p.prefix_by_agent
  }

  function setSection(section: 'skills' | 'agent_cmds' | 'shell_cmds' | 'kind_overrides' | 'prefix_by_agent', data: unknown) {
    const msg: PrefsSet = { type: 'prefs_set', section, data }
    ws.send(msg)
  }

  function setShellCmds(list: string[]) {
    shellCmds.value = list
    setSection('shell_cmds', list)
  }

  function setAgentCmds(list: string[]) {
    agentCmds.value = list
    setSection('agent_cmds', list)
  }

  function setSkills(list: Skill[]) {
    skills.value = list
    setSection('skills', list)
  }

  function setPrefixByAgent(map: Record<string, string>) {
    prefixByAgent.value = map
    setSection('prefix_by_agent', map)
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
    skills, agentCmds, shellCmds, kindOverrides, prefixByAgent,
    setPrefs, setSection, setShellCmds, setAgentCmds, setSkills, setPrefixByAgent,
    prefixForAgent, resolveSkill, kindOverrideFor,
  }
})
