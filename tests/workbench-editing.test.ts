import { describe, expect, it } from 'vitest'
import { mkdtempSync, rmSync, writeFileSync, existsSync } from 'node:fs'
import { tmpdir } from 'node:os'
import { join } from 'node:path'
import { spawnSync } from 'node:child_process'
import { codeExtension, draftReplica } from '../src/renderer/src/editor-sync'
import { parseEnvironment, updateEnvironment } from '../src/renderer/src/bash-environment'
import { useFileSelection } from '../src/renderer/src/file-selection'
import type { RemoteEntry } from '../src/shared/types'

describe('workbench text and file interactions', () => {
  it('selects code only by its final suffix', () => {
    expect(codeExtension('config/Main.PY')).toBe('py')
    for (const path of ['README', '.bashrc', '.sh', 'main.py.txt', 'test.log', 'dir.py/run']) expect(codeExtension(path)).toBeUndefined()
  })
  it('preserves concurrent edits and pending keystrokes in both windows', () => {
    const host = draftReplica(true), child = draftReplica(false)
    const original = 'abc\r\n'
    const a = host.change(original, 'Xabc\r\n'), b = child.change(original, 'Yabc\r\n')
    const c = child.change('Yabc\r\n', 'YZabc\r\n')
    const h = host.receive(b, 'Xabc\r\n')
    const merged = host.receive(c, h)
    expect(child.receive(a, 'YZabc\r\n')).toBe(merged)
    expect(merged).toBe('XYZabc\r\n')
    host.acknowledge(); child.acknowledge(); child.acknowledge()
    expect(host.receive(child.change(merged, ''), merged)).toBe('')
  })
  it('limits range and select-all to visible siblings and prunes removed files', () => {
    const rows = (names: string[]) => names.map(path => ({ path }) as RemoteEntry)
    const tree = { root: rows(['a', 'b', 'c', 'd']), b: rows(['e']) }
    const selection = useFileSelection(dir => tree[dir as keyof typeof tree] || [])
    selection.select('root', 'a'); selection.select('root', 'c', true)
    expect([...selection.paths.value]).toEqual(['a','c'])
    selection.select('root', 'a', false, true)
    expect([...selection.paths.value]).toEqual(['a','b','c'])
    selection.select('b', 'e', true)
    expect([...selection.paths.value]).toEqual(['e'])
    selection.all('root'); tree.root.pop(); selection.prune()
    expect([...selection.paths.value]).toEqual(['a','b','c'])
  })
})

describe('managed Bash environment', () => {
  const value = "space ' double\" $literal `literal`"
  it('preserves existing shell text and CRLF around a managed block', () => {
    const original = '# user\r\nmy_function() { :; }\r\n'
    const created = updateEnvironment(original, [{ name: 'EXAMPLE', value }]) + '# tail\r\n'
    expect(parseEnvironment(created).rows).toEqual([{ name: 'EXAMPLE', value }])
    const removed = updateEnvironment(created, [])
    expect(removed.startsWith(original)).toBe(true)
    expect(removed.endsWith('\r\n# tail\r\n')).toBe(true)
    expect(removed).not.toContain('EXAMPLE')
    expect(() => updateEnvironment(created.replace('export EXAMPLE', 'readonly EXAMPLE'), [])).toThrow()
    expect(() => updateEnvironment(created, [{ name: 'x-y', value }])).toThrow()
    expect(() => updateEnvironment(created, Array(2).fill({ name: 'X', value }))).toThrow()
  })
  const bash = process.platform === 'win32' ? join(process.env.ProgramFiles || 'C:/Program Files', 'Git/bin/bash.exe') : '/bin/bash'
  it.skipIf(!existsSync(bash))('loads quoted values unchanged in a new interactive Bash', () => {
    const dir = mkdtempSync(join(tmpdir(), 'meridian-bash-'))
    try {
      writeFileSync(join(dir, '.bashrc'), updateEnvironment('# user settings\n', [{ name: 'MERIDIAN_SAMPLE', value }]))
      const run = spawnSync(bash, ['--noprofile', '--rcfile', '.bashrc', '-ic', 'printf %s "$MERIDIAN_SAMPLE"'], {
        cwd: dir, encoding: 'utf8', timeout: 8000, env: { ...process.env, HOME: dir, HISTFILE: '/dev/null' }
      })
      expect(run.status, run.stderr).toBe(0)
      expect(run.stdout).toBe(value)
    } finally { rmSync(dir, { recursive: true, force: true }) }
  })
})
