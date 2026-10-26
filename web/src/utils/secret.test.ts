import { describe, expect, it } from 'vitest'
import { isSecretPrompt, lastNonEmptyLine, normalizeTail } from './secret'

describe('secret prompt line', () => {
  it('matches a short password, passphrase, or PIN prompt', () => {
    const yes = [
      '[sudo] password for testuser:',
      'Password:',
      'Enter passphrase for /home/user/.ssh/id_ed25519:',
      'Enter PIN:',
      '(current) UNIX password:',
      "testuser@box's password:",
      "dev@10.0.0.2's password:",
      'doas (testuser@box) password:',
      "Password for 'https://testuser@github.com':",
    ]
    for (const line of yes) expect(isSecretPrompt(line), line).toBe(true)
  })

  it('does not match prose that merely mentions a password', () => {
    expect(isSecretPrompt('the password is hunter2')).toBe(false)
    expect(isSecretPrompt('the secret password is in the file:')).toBe(false)
    expect(isSecretPrompt('a'.repeat(161))).toBe(false)
  })

  it('normalizes the last non-empty line the way the relay does', () => {
    const screen = 'hello\n\n  [sudo] password for testuser:  \n\n'
    expect(lastNonEmptyLine(screen)).toBe('[sudo] password for testuser:')
    expect(normalizeTail('  Password:  ')).toBe('Password:')
  })
})
