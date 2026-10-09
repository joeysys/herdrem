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
  truncated?: boolean
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

export interface CommitEntry {
  hash: string
  author: string
  date: string
  subject: string
}

export interface GitDiffContent {
  type: 'git_diff_content'
  cwd: string
  mode: 'unstaged' | 'cached' | 'commit' | 'range'
  commit?: string
  /** second hash, echoed when mode is range */
  commit2?: string
  /** echoed from the request; absent for a patch */
  format?: GitDiffFormat
  content: string
  req_id?: string
}

export interface GitLogViewResult {
  type: 'git_log_view_result'
  cwd: string
  /** git log text; hashes are always full */
  content: string
  /** most commits the relay lists */
  limit: number
  /** output hit the relay's byte cap */
  truncated?: boolean
  req_id?: string
}

export interface GitLogResult {
  type: 'git_log_result'
  cwd: string
  commits: CommitEntry[]
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

export interface TerminalPrefs {
  /** term view subscription-refresh interval in ms (0/absent = client default 6000) */
  poll_ms?: number
}

export interface TimingPrefs {
  /** base agents-snapshot poll tick in ms (0/absent = 3000) */
  snapshot_poll_ms?: number
  /** ms after last client disconnect before enrichment pauses (0/absent = 30000) */
  enrich_grace_ms?: number
  /** host snapshot failure retry backoff in ms (0/absent = 30000) */
  host_retry_ms?: number
  /** browser WS reconnect delay in ms (0/absent = 2000) */
  reconnect_delay_ms?: number
  /** grid card preview refresh in ms (0/absent = 8000); reserved — grid preview is disabled */
  grid_preview_ms?: number
  /** viewing a done pane this many ms auto-marks it read → idle (0/absent = 10000) */
  auto_mark_read_ms?: number
}

export interface Prefs {
  skills?: Skill[]
  agent_cmds?: string[]
  shell_cmds?: string[]
  kind_overrides?: Record<string, KindOverride>
  prefix_by_agent?: Record<string, string>
  terminal?: TerminalPrefs
  timing?: TimingPrefs
  git?: GitPrefs
}

export interface GitPrefs {
  /** run by the relay (sh, in the repo dir) when committing, instead of asking for a message */
  commit_cmd?: string
}

export interface PrefsSnapshot {
  type: 'prefs' | 'prefs_updated'
  prefs: Prefs
  req_id?: string // echoed from the triggering prefs_set
}

// C→S
export interface Respond {
  type: 'respond'
  text: string
  pane_id: string
  opt_index?: number
  submit?: boolean
}

export interface SendText {
  type: 'send_text'
  text: string
  pane_id: string
  submit?: boolean
  /** multi-line text goes as a bracketed paste (feature "send-text-paste") */
  paste?: boolean
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

export type GitDiffMode = 'unstaged' | 'cached' | 'commit' | 'range'
/** `git diff --name-status` instead of the patch */
export type GitDiffFormat = 'name-status'

// --- git status / stage / commit (feature "git-commit") ---
export interface GitStatusEntry {
  /** relative to the requested cwd */
  path: string
  /** M A D R C T U ? (two letters for conflicts, e.g. UU) */
  status: string
  /** rename / copy source */
  orig?: string
}
export interface GitStatusData {
  branch: string
  detached?: boolean
  /** no commit yet */
  initial?: boolean
  upstream?: string
  ahead: number
  behind: number
  staged: GitStatusEntry[]
  unstaged: GitStatusEntry[]
  untracked: GitStatusEntry[]
  conflicted: GitStatusEntry[]
  /** staged files outside cwd that a commit would also record */
  staged_outside: number
  truncated?: boolean
}
export interface GitStatusResultMsg extends GitStatusData {
  type: 'git_status_result'
  cwd: string
  req_id?: string
  /** a commit is running in this repo (possibly started from another page) */
  commit_running?: boolean
  /** the last finished commit's reply, kept by the relay for a few minutes */
  last_commit?: GitCommitResultMsg
  /** a sync operation is running in this repo */
  sync_running?: GitSyncAction
  /** the last finished sync's reply, kept by the relay for a few minutes */
  last_sync?: GitSyncResultMsg
}
export interface GitCommitResultMsg {
  type: 'git_commit_result'
  cwd: string
  req_id?: string
  ok: boolean
  hash?: string
  subject?: string
  output?: string
  error?: string
  command?: string
}
export interface GitStatusReq { type: 'git_status'; cwd: string; host?: string; req_id?: string }
export interface GitStageReq {
  type: 'git_stage'
  cwd: string
  host?: string
  paths?: string[]
  all?: boolean
  unstage?: boolean
  req_id?: string
}
export interface GitCommitReq {
  type: 'git_commit'
  cwd: string
  host?: string
  message?: string
  use_command?: boolean
  req_id?: string
}
export type GitSyncAction = 'fetch' | 'pull' | 'push'
export interface GitSyncReq {
  type: 'git_sync'
  cwd: string
  host?: string
  action: GitSyncAction
  req_id?: string
}
export interface GitSyncResultMsg {
  type: 'git_sync_result'
  cwd: string
  req_id?: string
  action: GitSyncAction
  ok: boolean
  output?: string
  error?: string
}
export interface GitDiscardReq {
  type: 'git_discard'
  cwd: string
  host?: string
  path: string
  req_id?: string
}

export interface GitDiff {
  type: 'git_diff'
  cwd: string
  host?: string
  mode: GitDiffMode
  /** one hash for commit mode; the "from" hash for range mode */
  commit?: string
  /** the "to" hash for range mode (feature "git-range") */
  commit2?: string
  /** feature "git-name-status"; omitted for a patch */
  format?: GitDiffFormat
  req_id?: string
}

export interface GitLog {
  type: 'git_log'
  cwd: string
  host?: string
  req_id?: string
}

/** The log view's switches (feature "git-log-view"), one git flag each. */
export interface GitLogOptions {
  oneline: boolean
  graph: boolean
  all: boolean
}

export interface GitLogViewReq extends Partial<GitLogOptions> {
  type: 'git_log_view'
  cwd: string
  host?: string
  req_id?: string
}

export interface PrefsGet {
  type: 'prefs_get'
}

export interface PrefsSet {
  type: 'prefs_set'
  section: 'skills' | 'agent_cmds' | 'shell_cmds' | 'kind_overrides' | 'prefix_by_agent' | 'terminal' | 'timing' | 'git'
  data: unknown
  req_id?: string // client-generated; echoed in prefs_updated / error
}

export interface KindOverrideClear {
  type: 'kind_override_clear'
  pane_id: string
  host?: string
}

export interface TunnelInfo {
  id: string
  port?: number
  file_path?: string
  host?: string
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
  port?: number
  file_path?: string
  host?: string
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

// --- shared input history (feature "input-history") ---
// Entry shape is the wire/disk format of internal/store/history.go.
export interface HistoryWireEntry {
  c: string
  t: number
  n: number
  p?: string
  k?: 'sh' | 'ag'
  a?: string
  w?: string
  h?: string
  s?: string
  l?: string
}
export interface HistorySnapshotMsg { type: 'history'; entries: HistoryWireEntry[] }
export interface HistoryAddedMsg { type: 'history_added'; entry: HistoryWireEntry }
export interface HistoryRemovedMsg { type: 'history_removed'; kind: 'sh' | 'ag'; text: string }
export interface HistoryClearedMsg { type: 'history_cleared'; kind: 'all' | 'sh' | 'ag'; host?: string; workspace_id?: string }
export interface HistoryRecord {
  type: 'history_record'
  text: string
  kind: 'sh' | 'ag'
  pane_id?: string
  host?: string
  workspace_id?: string
  workspace_label?: string
  agent?: string
  title?: string
}
export interface HistoryRemove { type: 'history_remove'; kind: 'sh' | 'ag'; text: string }
export interface HistoryClear { type: 'history_clear'; kind: 'all' | 'sh' | 'ag'; host?: string; workspace_id?: string }
export interface HistoryImport { type: 'history_import'; entries: HistoryWireEntry[] }

export type ServerMessage =
  | GitStatusResultMsg
  | GitCommitResultMsg
  | GitSyncResultMsg
  | HistorySnapshotMsg
  | HistoryAddedMsg
  | HistoryRemovedMsg
  | HistoryClearedMsg
  | Hello
  | AgentsSnapshot
  | BlockedNotify
  | PaneContent
  | TodoUpdated
  | TodosSnapshot
  | FileContent
  | FileListing
  | GitDiffContent
  | GitLogResult
  | GitLogViewResult
  | PrefsSnapshot
  | ErrorMsg
  | TunnelsSnapshot
  | TunnelUpdated
  | PaneCreated

export type ClientMessage =
  | GitStatusReq
  | GitStageReq
  | GitCommitReq
  | GitSyncReq
  | GitDiscardReq
  | HistoryRecord
  | HistoryRemove
  | HistoryClear
  | HistoryImport
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
  | GitDiff
  | GitLog
  | GitLogViewReq
  | PrefsGet
  | PrefsSet
  | TunnelCreate
  | TunnelClose
  | TunnelsList
  | PaneCreate
