import { createHash, type Hash } from 'node:crypto'
import { execFileSync } from 'node:child_process'
import { lstatSync, readdirSync, readFileSync, readlinkSync } from 'node:fs'
import { isAbsolute, join, normalize, relative, sep } from 'node:path'

// Settings build id.
//   clean tree:  <git short HEAD>
//   dirty tree:  <git short HEAD>+<7 hex>
//   no git:      nogit+<7 hex>
// Dev/watch appends .<rebuild count>. Production (vite build) does not:
// the count resets every process, so it cannot tell two installs apart.
//
// The dirty suffix hashes porcelain status plus worktree bytes, including
// untracked files. A boolean "-dirty" is not enough: a second uncommitted
// edit would keep the same id.

const ROOT_FILES = [
  'build-id.ts',
  'index.html',
  'package-lock.json',
  'package.json',
  'tsconfig.app.json',
  'tsconfig.json',
  'tsconfig.node.json',
  'vite.config.ts',
  'vitest.config.ts',
]
const ROOT_DIRS = ['public', 'src']

interface StatusEntry {
  xy: string
  path: string
  orig?: string
}

export function computeBuildId(cwd: string, counter?: number): string {
  const base = revisionId(cwd)
  if (counter === undefined) return base
  return `${base}.${counter}`
}

function revisionId(cwd: string): string {
  const head = gitText(cwd, ['rev-parse', '--short', 'HEAD'])
  const top = gitText(cwd, ['rev-parse', '--show-toplevel'])
  if (!head || !top) return `nogit+${hashSources(cwd)}`
  const status = readStatus(top)
  if (status === null) return `${head}+${hashSources(cwd)}`
  const entries = parsePorcelainZ(status)
  if (entries.length === 0) return head
  return `${head}+${hashStatus(top, entries)}`
}

function readStatus(top: string): Buffer | null {
  return (
    gitBuf(top, ['status', '--porcelain=v1', '--no-renames', '--untracked-files=all', '-z'])
    ?? gitBuf(top, ['status', '--porcelain=v1', '--untracked-files=all', '-z'])
  )
}

function gitBuf(cwd: string, args: string[]): Buffer | null {
  try {
    return execFileSync('git', args, {
      cwd,
      stdio: ['ignore', 'pipe', 'ignore'],
      maxBuffer: 32 * 1024 * 1024,
      env: { ...process.env, GIT_TERMINAL_PROMPT: '0' },
    })
  } catch {
    return null
  }
}

function gitText(cwd: string, args: string[]): string | null {
  const buf = gitBuf(cwd, args)
  if (!buf) return null
  const text = buf.toString('utf8').trim()
  return text || null
}

export function parsePorcelainZ(buf: Buffer): StatusEntry[] {
  const parts = splitNul(buf)
  const entries: StatusEntry[] = []
  for (let i = 0; i < parts.length; i++) {
    const head = parts[i].toString('utf8')
    if (head.length < 4 || head[2] !== ' ') continue
    const xy = head.slice(0, 2)
    const path = head.slice(3)
    // -z renames are "to" then "from": XY NEW\0OLD\0 (short format is from -> to).
    if (xy.includes('R') || xy.includes('C')) {
      const next = parts[++i]
      if (!next) break
      entries.push({ xy, path, orig: next.toString('utf8') })
      continue
    }
    entries.push({ xy, path })
  }
  entries.sort((a, b) => {
    if (a.path !== b.path) return a.path < b.path ? -1 : 1
    const ao = a.orig ?? ''
    const bo = b.orig ?? ''
    if (ao !== bo) return ao < bo ? -1 : 1
    if (a.xy !== b.xy) return a.xy < b.xy ? -1 : 1
    return 0
  })
  return entries
}

function splitNul(buf: Buffer): Buffer[] {
  const parts: Buffer[] = []
  let start = 0
  for (let i = 0; i < buf.length; i++) {
    if (buf[i] !== 0) continue
    if (i > start) parts.push(buf.subarray(start, i))
    start = i + 1
  }
  if (start < buf.length) parts.push(buf.subarray(start))
  return parts
}

function hashStatus(top: string, entries: StatusEntry[]): string {
  const hash = createHash('sha256')
  for (const entry of entries) {
    hash.update(entry.xy)
    hash.update('\0')
    hash.update(entry.path)
    hash.update('\0')
    hash.update(entry.orig ?? '')
    hash.update('\0')
    hashContent(hash, top, entry.path)
    if (entry.orig) hashContent(hash, top, entry.orig)
  }
  return hash.digest('hex').slice(0, 7)
}

function hashContent(hash: Hash, root: string, relPath: string) {
  const abs = inside(root, relPath)
  if (!abs) {
    hash.update('missing\0')
    return
  }
  let st: ReturnType<typeof lstatSync>
  try {
    st = lstatSync(abs)
  } catch {
    hash.update('missing\0')
    return
  }
  if (st.isSymbolicLink()) {
    hash.update('symlink\0')
    hash.update(readlinkSync(abs))
    hash.update('\0')
    return
  }
  if (st.isDirectory()) {
    hash.update('dir\0')
    return
  }
  if (!st.isFile()) {
    hash.update('other\0')
    return
  }
  hash.update('file\0')
  try {
    hash.update(readFileSync(abs))
  } catch {
    hash.update('unreadable\0')
    return
  }
  hash.update('\0')
}

function inside(root: string, relPath: string): string | null {
  if (!relPath || isAbsolute(relPath)) return null
  const abs = normalize(join(root, relPath))
  const rel = relative(root, abs)
  if (!rel || rel.startsWith('..') || isAbsolute(rel)) return null
  return abs
}

function hashSources(cwd: string): string {
  const files: string[] = []
  for (const name of ROOT_FILES) {
    const abs = join(cwd, name)
    try {
      if (lstatSync(abs).isFile()) files.push(name)
    } catch { /* absent */ }
  }
  for (const dir of ROOT_DIRS) walkFiles(join(cwd, dir), cwd, files)
  files.sort()
  const hash = createHash('sha256')
  for (const rel of files) {
    hash.update(rel)
    hash.update('\0')
    hashContent(hash, cwd, rel)
  }
  return hash.digest('hex').slice(0, 7)
}

function walkFiles(dir: string, root: string, out: string[]) {
  let entries
  try {
    entries = readdirSync(dir, { withFileTypes: true })
  } catch {
    return
  }
  for (const ent of entries) {
    if (ent.name === 'node_modules' || ent.name === '.git') continue
    const abs = join(dir, ent.name)
    const rel = relative(root, abs).split(sep).join('/')
    if (ent.isDirectory()) {
      walkFiles(abs, root, out)
      continue
    }
    if (ent.isFile() || ent.isSymbolicLink()) out.push(rel)
  }
}
