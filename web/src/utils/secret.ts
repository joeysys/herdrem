// Secret prompts are recognized on the client. The relay only compares tails.

const ANSI_RE = /\x1b(?:\[[0-?]*[ -/]*[@-~]|\][^\x07\x1b]*(?:\x07|\x1b\\)|.)/g

// A short English prompt, optionally prefixed by a bracketed tag such as
// [sudo] or (current), then password / passphrase / PIN, then an optional
// "for …" clause, and a trailing colon. Also matches OpenSSH
// (`user@host's password:`), doas (`doas (user@host) password:`), and a
// git HTTPS prompt whose for-clause contains colons.
const SECRET_RE =
  /^(?:[\[(][^)\]]+[\])]\s*)?(?:(?:[A-Za-z0-9._+-]+@[A-Za-z0-9._:-]+'s|[A-Za-z]+|\([^)]{1,80}\))\s+){0,6}(?:password|passphrase|pin)(?:\s+for\b.{0,120})?:\s*$/i

const SECRET_MAX = 160

export function stripAnsi(text: string): string {
  return text.replace(ANSI_RE, '')
}

// Same shape as herdr.NormalizeTail: ANSI off, whitespace collapsed.
export function normalizeTail(line: string): string {
  return stripAnsi(line).split(/\s+/).filter((part) => part !== '').join(' ')
}

export function lastNonEmptyLine(screen: string): string {
  const lines = stripAnsi(screen).replace(/\r\n/g, '\n').replace(/\r/g, '\n').split('\n')
  for (let i = lines.length - 1; i >= 0; i--) {
    if (lines[i].trim() !== '') return normalizeTail(lines[i])
  }
  return ''
}

export function isSecretPrompt(line: string): boolean {
  if (!line || line.length > SECRET_MAX) return false
  return SECRET_RE.test(line)
}
