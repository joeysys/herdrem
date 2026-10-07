// @vitest-environment node
import { execFileSync } from 'node:child_process'
import { mkdtempSync, mkdirSync, rmSync, writeFileSync } from 'node:fs'
import { tmpdir } from 'node:os'
import { join } from 'node:path'
import { afterEach, describe, expect, it } from 'vitest'
import { computeBuildId, parsePorcelainZ } from './build-id.ts'

const gitEnv = {
  ...process.env,
  GIT_AUTHOR_NAME: 'test',
  GIT_AUTHOR_EMAIL: 'test@example.com',
  GIT_COMMITTER_NAME: 'test',
  GIT_COMMITTER_EMAIL: 'test@example.com',
  GIT_CONFIG_COUNT: '1',
  GIT_CONFIG_KEY_0: 'commit.gpgsign',
  GIT_CONFIG_VALUE_0: 'false',
  GIT_TERMINAL_PROMPT: '0',
}

const dirs: string[] = []

function tempDir(): string {
  const dir = mkdtempSync(join(tmpdir(), 'herdrem-build-id-'))
  dirs.push(dir)
  return dir
}

function git(cwd: string, args: string[]) {
  execFileSync('git', args, { cwd, env: gitEnv, stdio: 'pipe' })
}

function repo(): string {
  const dir = tempDir()
  git(dir, ['init', '-b', 'main'])
  writeFileSync(join(dir, 'README'), 'v1\n')
  git(dir, ['add', 'README'])
  git(dir, ['commit', '-m', 'init'])
  return dir
}

function head(cwd: string): string {
  return execFileSync('git', ['rev-parse', '--short', 'HEAD'], { cwd, env: gitEnv, encoding: 'utf8' }).trim()
}

afterEach(() => {
  for (const dir of dirs.splice(0)) rmSync(dir, { recursive: true, force: true })
})

describe('computeBuildId', () => {
  it('uses the short HEAD when the worktree is clean', () => {
    const dir = repo()
    expect(computeBuildId(dir)).toBe(head(dir))
    expect(computeBuildId(dir)).toBe(computeBuildId(dir))
  })

  it('appends a dev counter without changing the content id', () => {
    const dir = repo()
    const base = computeBuildId(dir)
    expect(computeBuildId(dir, 1)).toBe(`${base}.1`)
    expect(computeBuildId(dir, 2)).toBe(`${base}.2`)
  })

  it('changes the suffix when an uncommitted file changes again', () => {
    const dir = repo()
    const clean = computeBuildId(dir)
    writeFileSync(join(dir, 'README'), 'v2\n')
    const once = computeBuildId(dir)
    expect(once).toMatch(new RegExp(`^${clean}\\+[0-9a-f]{7}$`))
    writeFileSync(join(dir, 'README'), 'v3\n')
    const twice = computeBuildId(dir)
    expect(twice).toMatch(new RegExp(`^${clean}\\+[0-9a-f]{7}$`))
    expect(twice).not.toBe(once)
    expect(computeBuildId(dir)).toBe(twice)
  })

  it('includes untracked files, including files inside a new directory', () => {
    const dir = repo()
    const clean = computeBuildId(dir)
    mkdirSync(join(dir, 'extra'))
    writeFileSync(join(dir, 'extra', 'note.ts'), 'one\n')
    const added = computeBuildId(dir)
    expect(added).not.toBe(clean)
    writeFileSync(join(dir, 'extra', 'note.ts'), 'two\n')
    expect(computeBuildId(dir)).not.toBe(added)
  })

  it('changes when a tracked file is deleted', () => {
    const dir = repo()
    const clean = computeBuildId(dir)
    rmSync(join(dir, 'README'))
    expect(computeBuildId(dir)).toMatch(new RegExp(`^${clean}\\+[0-9a-f]{7}$`))
  })

  it('ignores gitignored files', () => {
    const dir = repo()
    writeFileSync(join(dir, '.gitignore'), 'secret\n')
    git(dir, ['add', '.gitignore'])
    git(dir, ['commit', '-m', 'ignore'])
    const clean = computeBuildId(dir)
    writeFileSync(join(dir, 'secret'), 'nope\n')
    expect(computeBuildId(dir)).toBe(clean)
  })

  it('resolves dirty paths from a subdirectory the same way as the repo root', () => {
    const dir = repo()
    const sub = join(dir, 'web')
    mkdirSync(sub)
    writeFileSync(join(dir, 'README'), 'from sub\n')
    expect(computeBuildId(sub)).toBe(computeBuildId(dir))
    expect(computeBuildId(sub)).not.toBe(head(dir))
  })

  it('parses -z renames as the new path followed by the original', () => {
    expect(parsePorcelainZ(Buffer.from('R  new\0old\0M  README\0'))).toEqual([
      { xy: 'M ', path: 'README' },
      { xy: 'R ', path: 'new', orig: 'old' },
    ])
  })

  it('hashes web sources when git is unavailable', () => {
    const dir = tempDir()
    mkdirSync(join(dir, 'src'))
    writeFileSync(join(dir, 'src', 'main.ts'), 'export const n = 1\n')
    const first = computeBuildId(dir)
    expect(first).toMatch(/^nogit\+[0-9a-f]{7}$/)
    expect(computeBuildId(dir)).toBe(first)
    writeFileSync(join(dir, 'src', 'main.ts'), 'export const n = 2\n')
    const second = computeBuildId(dir)
    expect(second).toMatch(/^nogit\+[0-9a-f]{7}$/)
    expect(second).not.toBe(first)
    expect(computeBuildId(dir, 4)).toBe(`${second}.4`)
  })
})
