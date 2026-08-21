// linkify.ts — turn file paths in terminal text into clickable links.
//
// Operates on HTML-escaped text (paths rarely contain & < >, so escaping does
// not disturb matching). Produces <a class="term-file-link" data-fp data-line>
// anchors that the terminal view intercepts to open the file preview.
//
// Matches the path forms that appear in agent output.

// Extensions treated as previewable source/config/doc/data files.
const EXTS =
  'ts|tsx|js|jsx|vue|py|go|rs|md|json|yaml|yml|toml|css|scss|html|sh|bash|zsh|sql|c|cpp|h|hpp|java|kt|swift|rb|php|lua|pl|ex|erl|hs|ml|clj|elm|dart|svelte|astro|txt|log|csv|diff|patch|xml|jsonl|mdx|rst|pdf|png|jpg|jpeg|gif|svg|webp|ico|mod|sum|env|lock|work|proto|graphql|make|cmake'

// CJK punctuation, full-width symbols, and Han ideographs that can bound a path.
const CJK = '\\u3000-\\u303f\\uff00-\\uffef\\u4e00-\\u9fff'

// Chars allowed inside a single path segment (no whitespace / HTML metachars).
const SEG = '[\\w.+~=@#%$-]'

// Left boundary: start, whitespace, opening brackets/quotes, CJK punctuation/symbols.
const BOUNDARY = `(^|[\\s[\\]()"'\`{>}|,;:${CJK}])`
// Right boundary (lookahead): end, whitespace, closing brackets/quotes, CJK,
// or '&' (start of an HTML entity like &lt; in escaped text).
const TAIL = `(?=$|[\\s[\\]()"'\`{}<>|,;:&${CJK}])`

// Path candidate pattern:
// 1. file:///path/to/file
// 2. /path/to/file or ~/path/to/file or ./path/to/file or ../path/to/file or @/path/to/file
// 3. dir/subdir/file.ext or dir/subdir/file
// 4. filename.ext (e.g. App.vue, index.html, package.json, README.md, review.txt)
// 5. Common build files (Makefile, Dockerfile, LICENSE, go.mod, go.sum, Cargo.toml)
// Optional :line or :line:col suffix
const PATH_PATTERN =
  '(?:file://)?(?:(?:/|~/|\\./|\\.\\./|@/)?' + SEG + '+(?:/' + SEG + '+)+|' + SEG + '+\\.(?:' + EXTS + ')|Makefile|Dockerfile|LICENSE|go\\.mod|go\\.sum|Cargo\\.toml)(?::\\d+){0,2}'

const PATH_RE = new RegExp(BOUNDARY + '(' + PATH_PATTERN + ')' + TAIL, 'gi')

const extList = new Set(EXTS.split('|'))
const knownBuildFiles = new Set(['Makefile', 'Dockerfile', 'LICENSE', 'go.mod', 'go.sum', 'Cargo.toml'])

/** linkifyFilePaths wraps file paths in escaped text with clickable anchors.
 *  If validPaths is provided, only paths in the set are linked; others are left as plain text. */
export function linkifyFilePaths(escaped: string, validPaths?: Set<string> | null): string {
  return escaped.replace(PATH_RE, (m, boundary: string, full: string) => {
    if (full.startsWith('http://') || full.startsWith('https://')) return m

    // Strip scheme, leading ./, and :line or :line:col suffixes
    const raw = full.replace(/^file:\/\//, '').replace(/^\.\//, '')
    const clean = raw.replace(/(?::\d+)+$/, '')
    const lineMatch = raw.match(/:(\d+)(?::\d+)?$/)
    const line = lineMatch ? lineMatch[1] : undefined

    let targetPath = clean

    // If validPaths is explicitly provided (even if empty)
    if (validPaths !== undefined && validPaths !== null) {
      if (validPaths.size === 0) return m
      if (!validPaths.has(clean)) {
        const suffix = '/' + clean
        const extensionPrefix = clean + '.'
        const found = [...validPaths].find((vp) =>
          vp === clean || vp.endsWith(suffix) || vp.endsWith(clean) ||
          vp.startsWith(extensionPrefix) || vp.includes('/' + extensionPrefix)
        )
        if (!found) return m // return full match as plain text
        targetPath = found
      } else {
        // If clean is a bare filename, prefer a longer relative/absolute path from validPaths if available
        const suffix = '/' + clean
        const longerPath = [...validPaths].find((vp) => vp !== clean && (vp.endsWith(suffix) || vp.endsWith(clean)))
        if (longerPath) {
          targetPath = longerPath
        }
      }
    } else {
      // In fallback mode (no validPaths provided), verify extension is known
      const extMatch = clean.match(/\.([a-zA-Z0-9]+)$/)
      if (extMatch) {
        if (!extList.has(extMatch[1].toLowerCase())) return m
      } else {
        const basename = clean.split('/').pop() || clean
        if (!knownBuildFiles.has(basename)) return m
      }
    }

    const encoded = encodeURIComponent(targetPath)
    const lineAttr = line ? ` data-line="${line}"` : ''
    return `${boundary}<a href="#" class="term-file-link" data-fp="${encoded}"${lineAttr}>${full}</a>`
  })
}
