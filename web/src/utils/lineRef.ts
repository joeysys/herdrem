/** Shared "send selected lines to the agent" format for the file and diff
 *  previews: `<path>:<start>-<end>` on the first line, the comment below. */

export function formatLineRef(path: string, start: number, end: number): string {
  const a = Math.min(start, end)
  const b = Math.max(start, end)
  return a === b ? `${path}:${a}` : `${path}:${a}-${b}`
}

export function buildCommentText(path: string, start: number, end: number, comment: string): string {
  return `${formatLineRef(path, start, end)}\n${comment.trim()}`
}
