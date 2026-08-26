// Protocol message types — aligned with Go internal/protocol/messages.go

export interface Pane {
  pane_id: string
  agent?: string
  display_agent?: string // after kind_override
  project?: string
  cwd?: string
  host?: string
  status?: string
  agent_status?: string // herdr 0.7+ raw field
  workspace_id?: string
  workspace_label?: string
  kind?: string // "agent" | "shell"
  terminal_title?: string
  terminal_title_stripped?: string
  branch?: string
  prompt?: string
  options?: string[] // approval payloads
  option_labels?: string[] // approval display labels
  last_active?: number // epoch seconds of last content change
  valid_paths?: string[] // file paths in last content that exist on disk
}

export interface Hello {
  type: 'hello'
  v: number
  server_version: string
  features: string[]
}

export interface AgentsSnapshot {
  type: 'agents'
  agents: Pane[]
}

export interface BlockedNotify {
  type: 'blocked'
  pane_id: string
  agent?: string
  project?: string
  options: string[]
}

export interface PaneContent {
  type: 'pane_content'
  pane_id: string
  content: string
  lines?: number
  purpose?: 'term' | 'preview' | string
  /** true = content is an incremental delta to append to existing content */
  append?: boolean
  /** revision this content represents (full snapshot) */
  rev?: number
  /** for append: revision of the snapshot the delta extends */
  base_rev?: number
  /** echoed: subscription refresh — the client must not replace its content with this */
  poll?: boolean
}

export interface TodoBinding {
  host: string
  workspace_id: string
  workspace_label?: string
}

export interface Todo {
  id: string
  body: string
  binding?: TodoBinding
  created_at: string
  updated_at: string
}

export interface TodoUpdated {
  type: 'todo_updated'
  todo: Todo
  deleted?: boolean
}

export interface TodosSnapshot {
  type: 'todos'
  todos: Todo[]
}

export interface ErrorMsg {
  type: 'error'
  op: string
  message: string
  key?: string
  req_id?: string
}

export interface FileContent {
  type: 'file_content'
  path: string
  content: string
  encoding?: 'utf-8' | 'base64'
  mime_type?: string
  is_text?: boolean
  is_image?: boolean
  size?: number
  req_id?: string
}

export interface FileEntry {
  name: string
  type: 'file' | 'dir' | 'link'
  size: number
  mtime?: number
}

export interface FileListing {
  type: 'file_listing'
  path: string
  entries: FileEntry[]
  req_id?: string
}

export interface Skill {
  label: string
  template: string
}

export interface KindOverride {
  kind: string
  display_agent: string
  updated_at: string
}

export interface Prefs {
  skills?: Skill[]
  agent_cmds?: string[]
  shell_cmds?: string[]
  kind_overrides?: Record<string, KindOverride>
  prefix_by_agent?: Record<string, string>
}

export interface PrefsSnapshot {
  type: 'prefs' | 'prefs_updated'
  prefs: Prefs
}

// C→S
export interface Respond {
  type: 'respond'
  text: string
  pane_id: string
  opt_index?: number
}

export interface SendText {
  type: 'send_text'
  text: string
  pane_id: string
  submit?: boolean
}

export interface SendKeys {
  type: 'send_keys'
  keys: string
  pane_id: string
}

export interface ReadPane {
  type: 'read_pane'
  pane_id: string
  lines?: number
  purpose?: 'term' | 'preview' | string
  /** force a direct herdr read, bypassing the poller cache (Refresh button) */
  refresh?: boolean
  /** client declares it understands append deltas (incremental-terminal) */
  incremental?: boolean
  /** subscription-baseline refresh; response is not used to replace content */
  poll?: boolean
}

export interface TodoGet {
  type: 'todo_get'
}

export interface TodoSet {
  type: 'todo_set'
  id?: string
  body: string
  binding?: TodoBinding
}

export interface MarkPaneRead {
  type: 'mark_pane_read'
  pane_id: string
}

export interface KindOverrideSet {
  type: 'kind_override_set'
  pane_id: string
  host?: string
  display_agent?: string
}

export interface FileRead {
  type: 'file_read'
  path: string
  host?: string
  req_id?: string
}

export interface FileList {
  type: 'file_list'
  path: string
  host?: string
  req_id?: string
}

export interface PrefsGet {
  type: 'prefs_get'
}

export interface PrefsSet {
  type: 'prefs_set'
  section: 'skills' | 'agent_cmds' | 'shell_cmds' | 'kind_overrides' | 'prefix_by_agent'
  data: unknown
}

export interface KindOverrideClear {
  type: 'kind_override_clear'
  pane_id: string
  host?: string
}

export interface TunnelInfo {
  id: string
  port: number
  target_host?: string
  public_url: string
  status: 'starting' | 'active' | 'error' | 'closed'
  auth_mode: 'token_gate' | 'basic_auth' | 'direct'
  token?: string
  error?: string
  created_at: number
}

export interface TunnelsSnapshot {
  type: 'tunnels'
  tunnels: TunnelInfo[]
}

export interface TunnelUpdated {
  type: 'tunnel_updated'
  tunnel: TunnelInfo
}

export interface TunnelCreate {
  type: 'tunnel_create'
  port: number
  auth_mode?: 'token_gate' | 'basic_auth' | 'direct'
  pin?: string
}

export interface TunnelClose {
  type: 'tunnel_close'
  id: string
}

export interface TunnelsList {
  type: 'tunnels_list'
}

export interface PaneCreate {
  type: 'pane_create'
  mode: 'shell' | 'workspace' | 'tab'
  pane_id?: string
  workspace_id?: string
  host?: string
  direction?: 'right' | 'down'
  cwd?: string
  label?: string
}

export interface PaneCreated {
  type: 'pane_created'
  pane_id: string
  mode: 'shell' | 'workspace' | 'tab'
  host?: string
}

export type ServerMessage =
  | Hello
  | AgentsSnapshot
  | BlockedNotify
  | PaneContent
  | TodoUpdated
  | TodosSnapshot
  | FileContent
  | FileListing
  | PrefsSnapshot
  | ErrorMsg
  | TunnelsSnapshot
  | TunnelUpdated
  | PaneCreated

export type ClientMessage =
  | Respond
  | SendText
  | SendKeys
  | ReadPane
  | TodoGet
  | TodoSet
  | MarkPaneRead
  | KindOverrideSet
  | KindOverrideClear
  | FileRead
  | FileList
  | PrefsGet
  | PrefsSet
  | TunnelCreate
  | TunnelClose
  | TunnelsList
  | PaneCreate
