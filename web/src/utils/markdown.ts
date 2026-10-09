// markdown.ts — lightweight custom Markdown renderer (no external dep).
//
// Supports headings, bold/italic/inline-code, links, images, nested ul/ol, task lists,
// fenced code blocks, tables, hr, and YAML frontmatter (rendered as a kv table).
// Output is sanitized HTML (input is escaped first).

function esc(s: string): string {
  return s
    .replace(/&/g, '&amp;')
    .replace(/</g, '&lt;')
    .replace(/>/g, '&gt;')
    .replace(/"/g, '&quot;')
    .replace(/'/g, '&#39;')
}

// decodeEntities performs a single entity-decoding pass over already-escaped
// text so scheme checks see the value the browser will decode inside an
// attribute (e.g. "&amp;#58;" decodes to the literal "&#58;", not ":").
function decodeEntities(s: string): string {
  const map: Record<string, string> = { amp: '&', lt: '<', gt: '>', quot: '"', '#39': "'" }
  return s.replace(/&(amp|lt|gt|quot|#39);/g, (m, e) => map[e] ?? m)
}

const LINK_SCHEMES = new Set(['http:', 'https:', 'mailto:', 'tel:'])
const IMG_SCHEMES = new Set(['http:', 'https:'])

// stripUrlIgnorables removes characters the WHATWG URL parser discards before
// scheme resolution — ASCII C0 controls, DEL, and whitespace. Scheme checks
// must run on the stripped value or "\x01javascript:" slips through as a
// scheme-less relative URL that the browser then re-parses as javascript:.
function stripUrlIgnorables(s: string): string {
  return s.replace(/[\x00-\x20\x7F]/g, '')
}

// safeUrl returns the normalized URL when it is safe to emit, else null.
// Scheme-less URLs (relative paths, //host, #anchor) plus a whitelist of
// schemes are allowed; images additionally allow data:image/… sources.
// The returned value is entity-decoded and control-stripped; callers must
// re-escape it into the attribute so the emitted markup parses to the same
// URL the check validated.
function safeUrl(u: string, kind: 'link' | 'img'): string | null {
  const d = stripUrlIgnorables(decodeEntities(u))
  if (kind === 'img' && /^data:image\//i.test(d)) return d
  const m = d.match(/^([a-zA-Z][a-zA-Z0-9+.-]*):/)
  if (!m) return d
  return (kind === 'img' ? IMG_SCHEMES : LINK_SCHEMES).has(m[1].toLowerCase() + ':') ? d : null
}

function inlineMd(text: string): string {
  let s = esc(text)
  // inline code first so its contents are not further processed
  const codes: string[] = []
  s = s.replace(/`([^`]+)`/g, (_m, c) => {
    codes.push(c)
    return `${codes.length - 1}`
  })
  s = s.replace(/!\[([^\]]*)\]\(([^)\s]+)\)/g, (_m, alt, src) => {
    const u = safeUrl(src, 'img')
    return u ? `<img class="md-img" alt="${alt}" src="${esc(u)}" />` : alt
  })
  s = s.replace(/\[([^\]]+)\]\(([^)\s]+)\)/g, (_m, label, href) => {
    const u = safeUrl(href, 'link')
    return u ? `<a href="${esc(u)}" target="_blank" rel="noopener">${label}</a>` : label
  })
  s = s.replace(/\*\*([^*]+)\*\*/g, '<strong>$1</strong>')
  s = s.replace(/(^|[^*])\*([^*\n]+)\*/g, '$1<em>$2</em>')
  s = s.replace(/~~([^~]+)~~/g, '<del>$1</del>')
  s = s.replace(/(\d+)/g, (_m, i) => `<code>${codes[+i]}</code>`)
  return s
}

function isTableSep(line: string): boolean {
  return /^\s*\|?[\s:|-]+\|[\s:|-]*$/.test(line) && line.includes('-')
}

function splitRow(line: string): string[] {
  return line.replace(/^\s*\|/, '').replace(/\|\s*$/, '').split('|').map((c) => c.trim())
}

function renderFrontmatter(fm: string): string {
  const rows = fm
    .split('\n')
    .map((l) => l.match(/^(\w[\w-]*):\s*(.*)$/))
    .filter((m): m is RegExpMatchArray => !!m)
    .map((m) => `<tr><td class="fm-k">${esc(m[1])}</td><td class="fm-v">${inlineMd(m[2])}</td></tr>`)
  return rows.length ? `<table class="md-frontmatter">${rows.join('')}</table>` : ''
}

/** renderMarkdown converts Markdown source to sanitized HTML. */
export function renderMarkdown(md: string): string {
  let text = String(md ?? '')
  let frontmatter = ''
  if (text.startsWith('---\n')) {
    const end = text.indexOf('\n---\n', 4)
    if (end > 0) {
      frontmatter = text.slice(4, end)
      text = text.slice(end + 5)
    }
  }

  const lines = text.split('\n')
  const out: string[] = []
  let i = 0
  let listStack: { tag: 'ul' | 'ol'; indent: number; task: boolean }[] = []
  let para: string[] = []

  const flushPara = () => {
    if (para.length) {
      out.push(`<p>${para.map(inlineMd).join('<br>')}</p>`)
      para = []
    }
  }
  // A nested list sits inside the parent <li>, so that <li> stays open until
  // the nested list ends. Close it by appending </li> when it is still the
  // last chunk, otherwise emit a separate closer after the nested markup.
  const closeLi = () => {
    const last = out[out.length - 1]
    if (last?.startsWith('<li')) out[out.length - 1] = `${last}</li>`
    else out.push('</li>')
  }
  const flushList = () => {
    while (listStack.length) {
      closeLi()
      out.push(`</${listStack.pop()!.tag}>`)
    }
  }
  const listIndent = (ws: string) => {
    let n = 0
    for (const ch of ws) n += ch === '\t' ? 4 : 1
    return n
  }
  const pushItem = (tag: 'ul' | 'ol', indent: number, task: boolean, inner: string, liAttrs = '') => {
    flushPara()
    const top = () => listStack[listStack.length - 1]
    const same = (f: { tag: 'ul' | 'ol'; indent: number; task: boolean }) =>
      f.tag === tag && f.indent === indent && f.task === task
    if (!listStack.length || indent > top().indent) {
      out.push(task ? `<${tag} class="md-task">` : `<${tag}>`)
      listStack.push({ tag, indent, task })
    } else {
      while (listStack.length && top().indent > indent) {
        closeLi()
        out.push(`</${listStack.pop()!.tag}>`)
      }
      if (!listStack.length || !same(top())) {
        if (listStack.length) {
          closeLi()
          out.push(`</${listStack.pop()!.tag}>`)
        }
        out.push(task ? `<${tag} class="md-task">` : `<${tag}>`)
        listStack.push({ tag, indent, task })
      } else {
        closeLi()
      }
    }
    out.push(`<li${liAttrs}>${inner}`)
  }

  while (i < lines.length) {
    const line = lines[i]

    // fenced code block
    const fence = line.match(/^```(\w*)\s*$/)
    if (fence) {
      flushPara(); flushList()
      const lang = fence[1]
      const buf: string[] = []
      i++
      while (i < lines.length && !/^```\s*$/.test(lines[i])) { buf.push(lines[i]); i++ }
      out.push(`<pre class="md-code"${lang ? ` data-lang="${esc(lang)}"` : ''}><code>${esc(buf.join('\n'))}</code></pre>`)
      i++
      continue
    }

    // table
    if (line.includes('|') && i + 1 < lines.length && isTableSep(lines[i + 1])) {
      flushPara(); flushList()
      const header = splitRow(line)
      i += 2
      const rows: string[][] = []
      while (i < lines.length && lines[i].includes('|') && lines[i].trim()) {
        rows.push(splitRow(lines[i])); i++
      }
      const th = header.map((h) => `<th>${inlineMd(h)}</th>`).join('')
      const trs = rows.map((r) => `<tr>${r.map((c) => `<td>${inlineMd(c)}</td>`).join('')}</tr>`).join('')
      out.push(`<table class="md-table"><thead><tr>${th}</tr></thead><tbody>${trs}</tbody></table>`)
      continue
    }

    // heading
    const h = line.match(/^(#{1,6})\s+(.*)$/)
    if (h) {
      flushPara(); flushList()
      const level = h[1].length
      out.push(`<h${level} class="md-h${level}">${inlineMd(h[2])}</h${level}>`)
      i++
      continue
    }

    // hr
    if (/^\s*(-{3,}|\*{3,}|_{3,})\s*$/.test(line)) {
      flushPara(); flushList()
      out.push('<hr class="md-hr" />')
      i++
      continue
    }

    // task list
    const task = line.match(/^([ \t]*)[-*+]\s+\[([ xX])\]\s+(.*)$/)
    if (task) {
      const checked = task[2].toLowerCase() === 'x'
      pushItem(
        'ul',
        listIndent(task[1]),
        true,
        `<input type="checkbox" disabled${checked ? ' checked' : ''} /> ${inlineMd(task[3])}`,
        ` class="md-task-item${checked ? ' done' : ''}"`,
      )
      i++
      continue
    }

    // unordered list. Indent opens a nested list instead of another sibling.
    const ul = line.match(/^([ \t]*)[-*+]\s+(.*)$/)
    if (ul) {
      pushItem('ul', listIndent(ul[1]), false, inlineMd(ul[2]))
      i++
      continue
    }

    // ordered list
    const ol = line.match(/^([ \t]*)\d+\.\s+(.*)$/)
    if (ol) {
      pushItem('ol', listIndent(ol[1]), false, inlineMd(ol[2]))
      i++
      continue
    }

    // blank
    if (!line.trim()) {
      flushPara(); flushList()
      i++
      continue
    }

    // paragraph text
    para.push(line)
    i++
  }
  flushPara(); flushList()

  return renderFrontmatter(frontmatter) + out.join('\n')
}
