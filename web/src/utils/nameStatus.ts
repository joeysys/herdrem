// Parse `git diff --name-status` / `git show --name-status` output.
//
// Each file is "<status>[<score>]\t<path>" or, for renames and copies,
// "<R|C><score>\t<from>\t<to>". A merge lists one letter per parent ("MM").
// `git show` puts the commit header first; every line before the first file
// line belongs to it (message lines are indented, so they never match).

export interface NameStatusEntry {
  /** M A D R C T U X B, or one letter per parent for a merge */
  status: string
  /** similarity percentage for R / C, e.g. "100" */
  score: string
  path: string
  /** rename / copy source */
  orig?: string
}

export interface NameStatusView {
  header: string
  entries: NameStatusEntry[]
}

const ENTRY = /^([A-Z]+)(\d*)\t(.+)$/

export function parseNameStatus(content: string): NameStatusView {
  const header: string[] = []
  const entries: NameStatusEntry[] = []
  for (const line of content.split('\n')) {
    const m = ENTRY.exec(line)
    if (!m) {
      if (!entries.length) header.push(line)
      continue
    }
    const paths = m[3].split('\t').map(unquotePath)
    entries.push(
      paths.length > 1
        ? { status: m[1], score: m[2], orig: paths[0], path: paths[1] }
        : { status: m[1], score: m[2], path: paths[0] },
    )
  }
  return { header: header.join('\n').trim(), entries }
}

const ESCAPES: Record<string, number> = { a: 7, b: 8, t: 9, n: 10, v: 11, f: 12, r: 13, '"': 34, '\\': 92 }

/** Undo git's C-style quoting ("a\tb", "\303\251"); other paths pass through. */
export function unquotePath(p: string): string {
  if (p.length < 2 || !p.startsWith('"') || !p.endsWith('"')) return p
  const chars = Array.from(p.slice(1, -1))
  const bytes: number[] = []
  const enc = new TextEncoder()
  for (let i = 0; i < chars.length; i++) {
    const ch = chars[i]
    if (ch !== '\\' || i + 1 >= chars.length) {
      bytes.push(...enc.encode(ch))
      continue
    }
    const next = chars[++i]
    if (/[0-7]/.test(next)) {
      let oct = next
      while (oct.length < 3 && /[0-7]/.test(chars[i + 1] ?? '')) oct += chars[++i]
      bytes.push(parseInt(oct, 8))
    } else {
      bytes.push(ESCAPES[next] ?? next.charCodeAt(0))
    }
  }
  return new TextDecoder().decode(new Uint8Array(bytes))
}
