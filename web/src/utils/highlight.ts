// highlight.ts — syntax highlighting via highlight.js (190+ languages).
// The "common" bundle includes 35+ languages; we register a few extras.
import hljs from 'highlight.js/lib/common'

// Extra languages not in the common bundle
import dockerfile from 'highlight.js/lib/languages/dockerfile'
import nginx from 'highlight.js/lib/languages/nginx'
import latex from 'highlight.js/lib/languages/latex'
import dart from 'highlight.js/lib/languages/dart'
import scala from 'highlight.js/lib/languages/scala'
import elixir from 'highlight.js/lib/languages/elixir'
import clojure from 'highlight.js/lib/languages/clojure'
import haskell from 'highlight.js/lib/languages/haskell'
import protobuf from 'highlight.js/lib/languages/protobuf'
import cmake from 'highlight.js/lib/languages/cmake'
import elm from 'highlight.js/lib/languages/elm'
import erlang from 'highlight.js/lib/languages/erlang'
import fsharp from 'highlight.js/lib/languages/fsharp'
import tcl from 'highlight.js/lib/languages/tcl'
import vim from 'highlight.js/lib/languages/vim'

// Register extra languages
hljs.registerLanguage('dockerfile', dockerfile)
hljs.registerLanguage('nginx', nginx)
hljs.registerLanguage('latex', latex)
hljs.registerLanguage('dart', dart)
hljs.registerLanguage('scala', scala)
hljs.registerLanguage('elixir', elixir)
hljs.registerLanguage('clojure', clojure)
hljs.registerLanguage('haskell', haskell)
hljs.registerLanguage('protobuf', protobuf)
hljs.registerLanguage('cmake', cmake)
hljs.registerLanguage('elm', elm)
hljs.registerLanguage('erlang', erlang)
hljs.registerLanguage('fsharp', fsharp)
hljs.registerLanguage('tcl', tcl)
hljs.registerLanguage('vim', vim)

// Map file extensions to highlight.js language names
const EXT_TO_LANG: Record<string, string> = {
  // web
  ts: 'typescript', tsx: 'typescript', js: 'javascript', jsx: 'javascript',
  mjs: 'javascript', cjs: 'javascript', vue: 'xml', svelte: 'xml',
  html: 'xml', htm: 'xml', xml: 'xml', svg: 'xml', css: 'css', scss: 'scss',
  less: 'less',
  // systems
  go: 'go', rs: 'rust', py: 'python', rb: 'ruby', java: 'java', kt: 'kotlin',
  swift: 'swift', scala: 'scala', dart: 'dart', lua: 'lua', r: 'r',
  c: 'c', h: 'c', cpp: 'cpp', cc: 'cpp', cxx: 'cpp', hpp: 'cpp', hxx: 'cpp',
  cs: 'csharp', fs: 'fsharp', clj: 'clojure', ex: 'elixir', exs: 'elixir',
  hs: 'haskell', elm: 'elm', erl: 'erlang',
  // shells / config
  sh: 'bash', bash: 'bash', zsh: 'bash', fish: 'bash',
  yaml: 'yaml', yml: 'yaml', toml: 'ini', ini: 'ini', cfg: 'ini',
  conf: 'nginx', dockerfile: 'dockerfile',
  // data
  json: 'json', jsonc: 'json', json5: 'json', sql: 'sql', graphql: 'graphql',
  gql: 'graphql', proto: 'protobuf',
  // docs
  md: 'markdown', markdown: 'markdown', tex: 'latex',
  // build
  makefile: 'makefile', mk: 'makefile', cmake: 'cmake',
  // other
  php: 'php', pl: 'perl', pm: 'perl', tcl: 'tcl', vim: 'vim',
  diff: 'diff', patch: 'diff',
}

export function langForPath(path: string): string {
  const ext = (path.match(/\.([^.]+)$/) || [])[1]?.toLowerCase() || ''
  return EXT_TO_LANG[ext] || ''
}

function esc(s: string): string {
  return s.replace(/&/g, '&amp;').replace(/</g, '&lt;').replace(/>/g, '&gt;')
}

/** highlight applies highlight.js to a full source body, returning per-line HTML. */
export function highlight(body: string, lang: string): string[] {
  const text = String(body ?? '')
  if (!text) return ['']
  // Size guard: highlightAuto tries all registered languages and is O(n*len).
  // For large files this blocks the main thread and freezes the tab.
  const HIGHLIGHT_LIMIT = 100_000 // 100 KiB
  if (text.length > HIGHLIGHT_LIMIT) {
    return text.split('\n').map(esc)
  }
  // Try highlighting with the detected language
  let result: { value: string } | null = null
  if (lang && hljs.getLanguage(lang)) {
    try {
      result = hljs.highlight(text, { language: lang, ignoreIllegals: true })
    } catch { /* fall through to auto */ }
  }
  // Fallback: auto-detect (only for small files — guarded above)
  if (!result) {
    try {
      result = hljs.highlightAuto(text)
    } catch {
      // Ultimate fallback: plain escaped text
      return text.split('\n').map(esc)
    }
  }
  // highlight.js returns a single HTML blob with <span> tags that may span
  // multiple lines. We need to split it back into per-line HTML while keeping
  // the span nesting valid.
  return splitHtmlLines(result.value)
}

/**
 * Split an HTML string (with potentially open spans) into per-line HTML,
 * re-opening any unclosed spans on the next line.
 */
function splitHtmlLines(html: string): string[] {
  const lines = html.split('\n')
  const result: string[] = []
  let openSpans: string[] = []

  for (const line of lines) {
    // Re-open spans from previous line at line start
    const prefix = openSpans.map((s) => `<span class="${s}">`).join('')

    // Track span open/close ONLY in this line's content (not the prepended prefix)
    const openStack = [...openSpans]
    const tokenRe = /<span class="([^"]*)">|<\/span>/g
    let m: RegExpExecArray | null
    while ((m = tokenRe.exec(line)) !== null) {
      if (m[0] === '</span>') {
        if (openStack.length > 0) openStack.pop()
      } else if (m[1]) {
        openStack.push(m[1])
      }
    }

    // Close any remaining open spans at end of line
    let suffix = ''
    for (let i = 0; i < openStack.length; i++) suffix += '</span>'

    result.push(prefix + line + suffix)
    openSpans = openStack
  }

  return result
}
