import { describe, expect, it } from 'vitest'
import { terminalDirectory } from '../src/renderer/src/terminal-directory'

describe('OSC 7 directory reports', () => {
  it('decodes UTF-8 paths and escaped spaces, punctuation and percent characters', () => {
    expect(terminalDirectory('file://test.example/work/%E8%AE%AD%E7%BB%83%20run%23%25')).toBe('/work/训练 run#%')
  })
  it('ignores non-file, malformed and control-containing reports', () => {
    for (const sequence of ['https://example.invalid/work', 'file:///bad%ZZ', 'file:///x%0Ay', 'file:///test#external', 'file:///test%00']) expect(terminalDirectory(sequence)).toBeUndefined()
  })
})
