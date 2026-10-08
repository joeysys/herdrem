import { beforeEach, describe, expect, it, vi } from 'vitest'
import { mount, type VueWrapper } from '@vue/test-utils'
import { createPinia, setActivePinia } from 'pinia'
import { nextTick } from 'vue'
import GitStatusView from './GitStatusView.vue'
import { useDiffStore } from '../../stores/diff'
import { useFilesStore } from '../../stores/files'
import { usePrefsStore } from '../../stores/prefs'
import { useConnectionStore } from '../../stores/connection'
import { ws } from '../../api/ws'
import type { GitStatusData } from '../../api/types'

vi.mock('../../api/ws', () => ({ ws: { send: vi.fn(() => true), setReconnectDelay: vi.fn() } }))

type Sent = Record<string, unknown>
const sent = () => vi.mocked(ws.send).mock.calls.map((c) => c[0] as unknown as Sent)
const last = () => sent().at(-1)!

function setup(data: Partial<GitStatusData> = {}): VueWrapper {
  const d = useDiffStore()
  d.openDiff('/repo', 'local', 'status')
  d.setStatusResult({
    type: 'git_status_result', cwd: '/repo', req_id: last().req_id as string,
    branch: 'main', ahead: 0, behind: 0, staged_outside: 0,
    staged: [], unstaged: [], untracked: [], conflicted: [],
    ...data,
  })
  return mount(GitStatusView, { attachTo: document.body })
}

// Answer the request in flight with a full status, as the relay does.
function reply(data: Partial<GitStatusData>) {
  useDiffStore().setStatusResult({
    type: 'git_status_result', cwd: '/repo', req_id: last().req_id as string,
    branch: 'main', ahead: 0, behind: 0, staged_outside: 0,
    staged: [], unstaged: [], untracked: [], conflicted: [],
    ...data,
  })
}

const BASIC: Partial<GitStatusData> = {
  unstaged: [{ path: 'src/a.go', status: 'M' }, { path: 'old.txt', status: 'D' }],
  untracked: [{ path: 'new.txt', status: '?' }],
  staged: [{ path: 'ready.go', status: 'A' }, { path: 'moved.go', status: 'R', orig: 'was.go' }],
}

beforeEach(() => {
  document.body.innerHTML = ''
  setActivePinia(createPinia())
  vi.mocked(ws.send).mockReset()
  vi.mocked(ws.send).mockReturnValue(true)
})

describe('GitStatusView', () => {
  it('shows the branch, its upstream and how far ahead/behind it is', () => {
    const w = setup({ upstream: 'origin/main', ahead: 2, behind: 1 })
    expect(w.get('.gs-branch').text()).toContain('main')
    expect(w.get('.gs-branch').text()).toContain('origin/main')
    expect(w.get('.gs-branch').text()).toContain('↑2')
    expect(w.get('.gs-branch').text()).toContain('↓1')
  })

  it('says when there is nothing to commit', () => {
    expect(setup().text()).toContain('nothing to commit, working tree clean')
  })

  it('lists changes (incl. new files) and staged files with readable status badges', () => {
    const w = setup(BASIC)
    const changes = w.get('[aria-label="Changes not staged"]')
    expect(changes.findAll('.gs-path').map((p) => p.text())).toEqual(['src/a.go', 'old.txt', 'new.txt'])
    expect(changes.findAll('.gs-badge').map((b) => b.attributes('aria-label'))).toEqual(['modified', 'deleted', 'new file'])
    const staged = w.get('[aria-label="Staged changes"]')
    expect(staged.findAll('.gs-path').map((p) => p.text())).toEqual(['ready.go', 'moved.go ← was.go'])
  })

  it('selects files one by one or all at once, and adds the selection', async () => {
    const w = setup(BASIC)
    const all = w.get('input[aria-label="Select all changes"]')
    const addSel = w.findAll('.gs-btn').find((b) => b.text().startsWith('add selected'))!
    expect(addSel.attributes('disabled')).toBeDefined()

    await w.get('input[aria-label="Select new.txt"]').setValue(true)
    expect((all.element as HTMLInputElement).indeterminate).toBe(true)
    expect(addSel.text()).toBe('add selected (1)')
    await addSel.trigger('click')
    expect(last()).toMatchObject({ type: 'git_stage', paths: ['new.txt'], unstage: false })
  })

  it('select-all toggles every change', async () => {
    const w = setup(BASIC)
    const all = w.get('input[aria-label="Select all changes"]')
    await all.trigger('change')
    expect(w.findAll('.gs-row-check').every((c) => (c.element as HTMLInputElement).checked)).toBe(true)
    const addSel = w.findAll('.gs-btn').find((b) => b.text().startsWith('add selected'))!
    await addSel.trigger('click')
    expect(last().paths).toEqual(['src/a.go', 'old.txt', 'new.txt'])
    await w.get('input[aria-label="Select all changes"]').trigger('change')
  })

  it('adds everything, a single file, or unstages', async () => {
    const w = setup(BASIC)
    await w.findAll('.gs-btn').find((b) => b.text() === 'add all')!.trigger('click')
    expect(last()).toMatchObject({ type: 'git_stage', all: true, unstage: false })
    reply(BASIC)
    await nextTick()
    await w.get('button[aria-label="Add src/a.go"]').trigger('click')
    expect(last()).toMatchObject({ paths: ['src/a.go'], unstage: false })
    reply(BASIC)
    await nextTick()
    await w.get('button[aria-label="Unstage ready.go"]').trigger('click')
    expect(last()).toMatchObject({ paths: ['ready.go'], unstage: true })
  })

  it('disables staging buttons while a stage is running', async () => {
    const w = setup(BASIC)
    await w.findAll('.gs-btn').find((b) => b.text() === 'add all')!.trigger('click')
    expect(w.get('button[aria-label="Add src/a.go"]').attributes('disabled')).toBeDefined()
    expect(w.text()).toContain('updating…')
  })

  it('opens a changed file\'s diff, a staged file\'s cached diff, and previews a new file', async () => {
    const w = setup(BASIC)
    const d = useDiffStore()
    const openPath = vi.spyOn(useFilesStore(), 'openPath')
    await w.findAll('.gs-path').find((p) => p.text() === 'new.txt')!.trigger('click')
    expect(openPath).toHaveBeenCalledWith('/repo/new.txt', '/repo', 'local', null)
    expect(d.state?.mode).toBe('status')

    await w.findAll('.gs-path').find((p) => p.text() === 'src/a.go')!.trigger('click')
    expect(d.state?.mode).toBe('unstaged')
    expect(d.focusPath).toBe('src/a.go')
  })

  it('opens the staged diff from the staged list', async () => {
    const w = setup(BASIC)
    await w.findAll('.gs-path').find((p) => p.text() === 'ready.go')!.trigger('click')
    expect(useDiffStore().state?.mode).toBe('cached')
  })

  describe('commit with a message', () => {
    it('needs something staged and a message', async () => {
      const nothing = setup({ unstaged: [{ path: 'a', status: 'M' }] })
      const btn = () => nothing.get('.gs-commit-btn')
      await nothing.get('textarea').setValue('msg')
      expect(btn().attributes('disabled')).toBeDefined()
      expect(nothing.text()).toContain('stage something first')
      nothing.unmount()

      const w = setup(BASIC)
      expect(w.get('.gs-commit-btn').attributes('disabled')).toBeDefined()
      expect(w.text()).toContain('write a commit message')
      await w.get('textarea').setValue('  ')
      expect(w.get('.gs-commit-btn').attributes('disabled')).toBeDefined()
      await w.get('textarea').setValue('add the parser')
      expect(w.get('.gs-commit-btn').attributes('disabled')).toBeUndefined()
      expect(w.get('.gs-commit-btn').text()).toBe('commit · 2 files')
    })

    it('commits on click or Ctrl+Enter and clears the box after success', async () => {
      const w = setup(BASIC)
      await w.get('textarea').setValue('add the parser')
      await w.get('textarea').trigger('keydown', { key: 'Enter', ctrlKey: true })
      expect(last()).toMatchObject({ type: 'git_commit', message: 'add the parser' })
      expect(w.get('.gs-commit-btn').text()).toBe('committing…')
      useDiffStore().setCommitResult({ type: 'git_commit_result', cwd: '/repo', req_id: last().req_id as string, ok: true, hash: '0123456789abcdef', subject: 'add the parser' })
      await nextTick()
      expect(w.get('.gs-outcome').text()).toContain('✓ committed 0123456 add the parser')
      expect((w.get('textarea').element as HTMLTextAreaElement).value).toBe('')
    })

    it('keeps the message and shows the hook output when the commit fails', async () => {
      const w = setup(BASIC)
      await w.get('textarea').setValue('wip')
      await w.get('.gs-commit-btn').trigger('click')
      useDiffStore().setCommitResult({ type: 'git_commit_result', cwd: '/repo', req_id: last().req_id as string, ok: false, error: 'git commit: exit status 1', output: 'lint failed: a.go:3' })
      await nextTick()
      expect(w.get('.gs-outcome').classes()).toContain('err')
      expect(w.get('.gs-output').text()).toBe('lint failed: a.go:3')
      expect((w.get('textarea').element as HTMLTextAreaElement).value).toBe('wip')
    })

    it('counts and warns about staged files outside this folder', () => {
      const w = setup({ ...BASIC, staged_outside: 3 })
      expect(w.text()).toContain('also records 3 staged files outside this folder')
    })
  })

  describe('commit command', () => {
    it('with a command configured, the button runs it — no message needed', async () => {
      usePrefsStore().setPrefs({ git: { commit_cmd: 'git commit -am "$(aicommit)"' } })
      const w = setup({ unstaged: [{ path: 'a', status: 'M' }] }) // nothing staged: the command may stage itself
      expect(w.find('textarea').exists()).toBe(false)
      expect(w.get('.gs-note code').text()).toBe('git commit -am "$(aicommit)"')
      await w.get('.gs-commit-btn').trigger('click')
      expect(last()).toMatchObject({ type: 'git_commit', use_command: true })
      expect(w.get('.gs-commit-btn').text()).toBe('running…')
      useDiffStore().setCommitResult({ type: 'git_commit_result', cwd: '/repo', req_id: last().req_id as string, ok: true, output: 'suggested: fix', command: 'x' })
      await nextTick()
      expect(w.get('.gs-outcome').text()).toContain('command finished — no new commit')
      expect(w.get('.gs-output').text()).toBe('suggested: fix')
    })

    it('can switch to a typed message for one commit and back', async () => {
      usePrefsStore().setPrefs({ git: { commit_cmd: 'cmd' } })
      const w = setup(BASIC)
      await w.findAll('.gs-link').find((b) => b.text() === 'write a message instead')!.trigger('click')
      expect(w.find('textarea').exists()).toBe(true)
      await w.findAll('.gs-link').find((b) => b.text() === 'use the commit command')!.trigger('click')
      expect(w.find('textarea').exists()).toBe(false)
    })

    it('is saved to relay prefs (shared by every device) and can be cleared', async () => {
      const w = setup(BASIC)
      const input = w.get('input[aria-label="Commit command"]')
      const save = () => w.findAll('.gs-cmd-row .gs-btn').find((b) => b.text() === 'save')!
      expect(save().attributes('disabled')).toBeDefined()
      await input.setValue('  npm run commit  ')
      await save().trigger('click')
      expect(last()).toMatchObject({ type: 'prefs_set', section: 'git', data: { commit_cmd: 'npm run commit' } })
      await nextTick()
      expect(w.find('textarea').exists()).toBe(false) // now in command mode
      await w.findAll('.gs-cmd-row .gs-btn').find((b) => b.text() === 'clear')!.trigger('click')
      expect(last()).toMatchObject({ type: 'prefs_set', section: 'git', data: { commit_cmd: '' } })
    })
  })

  it('offers fetch, a fast-forward pull, and push only when the relay supports them', async () => {
    const plain = setup({ upstream: 'origin/main', ahead: 2, behind: 0 })
    expect(plain.find('[aria-label="Remote"]').exists()).toBe(false)
    plain.unmount()

    useConnectionStore().setHello('t', ['git-sync'])
    const confirm = vi.spyOn(window, 'confirm').mockReturnValue(true)
    const w = setup({ upstream: 'origin/main', ahead: 2, behind: 0 })
    const remote = w.get('[aria-label="Remote"]')
    const btn = (label: string) => remote.findAll('.gs-btn').find((b) => b.text() === label)!
    expect(btn('push 2').attributes('disabled')).toBeUndefined()
    expect(btn('pull').attributes('disabled')).toBeDefined()
    expect(btn('fetch').attributes('disabled')).toBeUndefined()
    expect(remote.text()).toContain('whole branch')

    await btn('push 2').trigger('click')
    expect(confirm).toHaveBeenCalledWith(expect.stringContaining('Push 2 commits to origin/main'))
    expect(confirm.mock.calls[0][0]).toContain('whole branch')
    expect(last()).toMatchObject({ type: 'git_sync', action: 'push', cwd: '/repo' })
    confirm.mockReturnValue(false)
    w.unmount()

    const again = setup({ upstream: 'origin/main', ahead: 2, behind: 0 })
    vi.mocked(ws.send).mockClear()
    await again.get('[aria-label="Remote"]').findAll('.gs-btn').find((b) => b.text() === 'push 2')!.trigger('click')
    expect(sent()).toEqual([])
  })

  it('pulls only when strictly behind a clean tree, and fetches without asking', async () => {
    useConnectionStore().setHello('t', ['git-sync'])
    const confirm = vi.spyOn(window, 'confirm').mockReturnValue(true)
    const dirty = setup({
      upstream: 'origin/main', ahead: 0, behind: 1,
      unstaged: [{ path: 'a.go', status: 'M' }],
    })
    const dirtyPull = dirty.get('[aria-label="Remote"]').findAll('.gs-btn').find((b) => b.text() === 'pull 1')!
    expect(dirtyPull.attributes('disabled')).toBeDefined()
    dirty.unmount()

    const w = setup({ upstream: 'origin/main', ahead: 0, behind: 1 })
    const remote = w.get('[aria-label="Remote"]')
    const pullBtn = remote.findAll('.gs-btn').find((b) => b.text() === 'pull 1')!
    expect(pullBtn.attributes('disabled')).toBeUndefined()
    await pullBtn.trigger('click')
    expect(confirm.mock.calls[0][0]).toContain('Fast-forward only')
    expect(last()).toMatchObject({ type: 'git_sync', action: 'pull' })
    w.unmount()

    confirm.mockClear()
    const fetched = setup({ upstream: 'origin/main', ahead: 0, behind: 1 })
    await fetched.get('[aria-label="Remote"]').findAll('.gs-btn').find((b) => b.text() === 'fetch')!.trigger('click')
    expect(confirm).not.toHaveBeenCalled()
    expect(last()).toMatchObject({ type: 'git_sync', action: 'fetch' })
  })

  it('discards one unstaged file after confirmation, and not new files', async () => {
    useConnectionStore().setHello('t', ['git-sync'])
    const confirm = vi.spyOn(window, 'confirm').mockReturnValue(false)
    const w = setup(BASIC)
    expect(w.find('[aria-label="Discard unstaged changes in src/a.go"]').exists()).toBe(true)
    expect(w.find('[aria-label="Discard unstaged changes in old.txt"]').exists()).toBe(true)
    expect(w.find('[aria-label="Discard unstaged changes in new.txt"]').exists()).toBe(false)
    await w.get('[aria-label="Discard unstaged changes in src/a.go"]').trigger('click')
    expect(sent().some((m) => m.type === 'git_discard')).toBe(false)

    confirm.mockReturnValue(true)
    await w.get('[aria-label="Discard unstaged changes in src/a.go"]').trigger('click')
    expect(confirm).toHaveBeenCalledWith(expect.stringContaining('src/a.go'))
    expect(last()).toMatchObject({ type: 'git_discard', path: 'src/a.go', cwd: '/repo' })
  })

  it('shows a status error', () => {
    const d = useDiffStore()
    d.openDiff('/repo', 'local', 'status')
    d.setStatusError(last().req_id as string, 'git rev-parse --show-prefix: exit status 128 (fatal: not a git repository)')
    const w = mount(GitStatusView)
    expect(w.get('[role="alert"]').text()).toContain('not a git repository')
  })
})
