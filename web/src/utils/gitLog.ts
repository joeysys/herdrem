// Split `git log` text (as the relay sends it: full hashes, --decorate=short,
// oneline or medium, optionally --graph) into lines the log view can render,
// with each commit's hash picked out so it can link to `git show`.

export interface LogLine {
  /** --graph drawing in front of the line, '' without --graph */
  graph: string
  /** full hash when this line starts a commit */
  hash?: string
  /** medium format: the line reads "commit <hash>" */
  keyword?: boolean
  /** " (HEAD -> main, tag: v1)" */
  decor?: string
  /** the rest of the line, verbatim */
  text: string
}

// git draws the graph with these; '-' and '.' only appear in octopus merges.
const GRAPH = String.raw`[*|/\\_ .\-]*?`
const ONELINE = new RegExp(String.raw`^(${GRAPH})([0-9a-f]{40})( \([^)]*\))?(.*)$`)
const MEDIUM = new RegExp(String.raw`^(${GRAPH})commit ([0-9a-f]{40})( \([^)]*\))?(.*)$`)
// A line of nothing but drawing (connectors between commits).
const GRAPH_LINE = /^[*|/\\_ .\-]*$/
// The drawing in front of a message line: one cell per lane, each a symbol
// and its gap. Stopping at the first cell that starts with a space leaves
// git's own indent ("|     subject") and any "*" or "-" of the message
// with the text.
const GRAPH_CELLS = /^(?:[|/\\_.\-][ |/\\_.\-])*/

export function parseGitLog(content: string, opts: { oneline: boolean; graph: boolean }): LogLine[] {
  const lines = content.replace(/\n$/, '').split('\n')
  if (lines.length === 1 && lines[0] === '') return []
  return lines.map((line) => {
    const m = (opts.oneline ? ONELINE : MEDIUM).exec(line)
    if (m && (opts.graph || m[1] === '')) {
      return { graph: m[1], hash: m[2], keyword: !opts.oneline, decor: m[3] || undefined, text: m[4] }
    }
    // body lines: only peel off a graph prefix when one was asked for, so an
    // indented message line keeps its own leading characters
    const graph = !opts.graph ? '' : GRAPH_LINE.test(line) ? line : GRAPH_CELLS.exec(line)![0]
    return { graph, text: line.slice(graph.length) }
  })
}
