import { describe, expect, it } from 'vitest'
import { parseGitLog } from './gitLog'

const H1 = 'e277d858999bd071b6b0cdd19fbfc737490b09f1'
const H2 = 'ab3096838acf28c8309e6c87635138f1b72396e6'

describe('parseGitLog', () => {
  it('oneline: hash, decoration and subject', () => {
    const lines = parseGitLog(`${H1} (HEAD -> main, origin/main) merge side\n${H2} side work\n`, { oneline: true, graph: false })
    expect(lines).toEqual([
      { graph: '', hash: H1, keyword: false, decor: ' (HEAD -> main, origin/main)', text: ' merge side' },
      { graph: '', hash: H2, keyword: false, decor: undefined, text: ' side work' },
    ])
  })

  it('oneline --graph: the drawing stays apart from the hash, connector lines are graph only', () => {
    const lines = parseGitLog(`*   ${H1} (HEAD -> main) merge side\n|\\  \n| * ${H2} (side) side work\n|/  \n`, { oneline: true, graph: true })
    expect(lines[0]).toMatchObject({ graph: '*   ', hash: H1, decor: ' (HEAD -> main)' })
    expect(lines[1]).toEqual({ graph: '|\\  ', text: '' })
    expect(lines[2]).toMatchObject({ graph: '| * ', hash: H2, text: ' side work' })
    expect(lines[3].hash).toBeUndefined()
  })

  it('medium: only "commit <hash>" lines link; a hash quoted in the message does not', () => {
    const out = [
      `commit ${H1} (tag: v1)`,
      'Author: A <a@x>',
      '',
      `    revert ${H2}`,
      '    * bullet keeps its star',
    ].join('\n')
    const lines = parseGitLog(out, { oneline: false, graph: false })
    expect(lines[0]).toMatchObject({ hash: H1, keyword: true, decor: ' (tag: v1)', text: '' })
    expect(lines[3]).toEqual({ graph: '', text: `    revert ${H2}` })
    expect(lines[4]).toEqual({ graph: '', text: '    * bullet keeps its star' })
  })

  it('medium --graph', () => {
    const lines = parseGitLog(`* commit ${H1}\n| Author: A <a@x>\n`, { oneline: false, graph: true })
    expect(lines[0]).toMatchObject({ graph: '* ', hash: H1, keyword: true })
    expect(lines[1]).toEqual({ graph: '| ', text: 'Author: A <a@x>' })
  })

  // Bug: the body-line graph match was greedy over spaces and took git's
  // message indent (and a message's own "*") as drawing; octopus "-" / "."
  // connectors leaked into the text.
  it('medium --graph keeps the message indent and bullets as text', () => {
    const out = `*   commit ${H1}\n|\\  Merge: a b\n| | Author: A <a@x>\n| |\n| |     subject stays indented\n| |     * bullet\n| |     - dash\n|\\-.  \n| | \\ \n`
    const lines = parseGitLog(out, { oneline: false, graph: true })
    expect(lines[1]).toEqual({ graph: '|\\', text: '  Merge: a b' })
    expect(lines[2]).toEqual({ graph: '| | ', text: 'Author: A <a@x>' })
    expect(lines[3]).toEqual({ graph: '| |', text: '' })
    expect(lines[4]).toEqual({ graph: '| | ', text: '    subject stays indented' })
    expect(lines[5]).toEqual({ graph: '| | ', text: '    * bullet' })
    expect(lines[6]).toEqual({ graph: '| | ', text: '    - dash' })
    expect(lines[7]).toEqual({ graph: '|\\-.  ', text: '' })
    expect(lines[8]).toEqual({ graph: '| | \\ ', text: '' })
  })

  it('empty output is no lines', () => {
    expect(parseGitLog('', { oneline: true, graph: true })).toEqual([])
  })
})
