// markdown.ts — lightweight custom Markdown renderer (no external dep).
//
// Supports headings, bold/italic/inline-code, links, images, ul/ol, task lists,
// fenced code blocks, tables, hr, and YAML frontmatter (rendered as a kv table).
// Output is sanitized HTML (input is escaped first).

function esc(s: string): string {
  return s.replace(/&/g, '&amp;').replace(/</g, '&lt;').replace(/>/g, '&gt;')
}

function inlineMd(text: string): string {
  let s = esc(text)
  // inline code first so its contents are not further processed
  const codes: string[] = []
  s = s.replace(/`([^`]+)`/g, (_m, c) => {
    codes.push(c)
    return `${codes.length - 1}`
  })
  s = s.replace(/!\[([^\]]*)\]\(([^)\s]+)\)/g, '<img class="md-img" alt="$1" src="$2" />')
  s = s.replace(/\[([^\]]+)\]\(([^)\s]+)\)/g, '<a href="$2" target="_blank" rel="noopener">$1</a>')
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
  let listStack: string[] = []
  let para: string[] = []

  const flushPara = () => {
    if (para.length) {
      out.push(`<p>${para.map(inlineMd).join('<br>')}</p>`)
      para = []
    }
  }
  const flushList = () => {
    while (listStack.length) out.push(`</${listStack.pop()}>`)
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
    const task = line.match(/^\s*[-*+]\s+\[([ xX])\]\s+(.*)$/)
    if (task) {
      flushPara()
      if (listStack[listStack.length - 1] !== 'ul') { flushList(); out.push('<ul class="md-task">'); listStack.push('ul') }
      const checked = task[1].toLowerCase() === 'x'
      out.push(`<li class="md-task-item${checked ? ' done' : ''}"><input type="checkbox" disabled${checked ? ' checked' : ''} /> ${inlineMd(task[2])}</li>`)
      i++
      continue
    }

    // unordered list
    const ul = line.match(/^\s*[-*+]\s+(.*)$/)
    if (ul) {
      flushPara()
      if (listStack[listStack.length - 1] !== 'ul') { flushList(); out.push('<ul>'); listStack.push('ul') }
      out.push(`<li>${inlineMd(ul[1])}</li>`)
      i++
      continue
    }

    // ordered list
    const ol = line.match(/^\s*\d+\.\s+(.*)$/)
    if (ol) {
      flushPara()
      if (listStack[listStack.length - 1] !== 'ol') { flushList(); out.push('<ol>'); listStack.push('ol') }
      out.push(`<li>${inlineMd(ol[1])}</li>`)
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
