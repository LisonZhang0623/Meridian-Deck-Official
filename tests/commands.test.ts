import { describe, expect, it } from 'vitest'
import { mkdtemp, readFile, writeFile } from 'node:fs/promises'
import { join } from 'node:path'
import { tmpdir } from 'node:os'
import { CommandsStore, commandParameters, renderCommand } from '../src/main/commands'
import type { CommandSnippet } from '../src/shared/v2'

const sample: CommandSnippet = { id: '', name: '查看训练日志', group: '训练', profileId: '', command: 'tail -n {{lines}} -- {{path}}', description: '跟踪指定文件' }
async function store() { const directory = await mkdtemp(join(tmpdir(), 'sshtool-commands-')); const service = new CommandsStore(directory); await service.init(); return { directory, service } }

describe('saved commands', () => {
  it('starts with an empty independent collection and serializes concurrent changes without losing saved commands', async () => {
    const { directory, service } = await store()
    expect(service.list()).toEqual([])
    const [first, second] = await Promise.all([service.save(sample), service.save({ ...sample, name: '另一条', profileId: 'server-a' })])
    expect(first.id).not.toBe(second.id)
    const fresh = new CommandsStore(directory); await fresh.init()
    expect(fresh.list()).toHaveLength(2)
    expect(fresh.get(second.id).profileId).toBe('server-a')
    const snapshot = fresh.list(); snapshot[0].name = 'mutated'
    expect(fresh.get(first.id).name).toBe(sample.name)
    await fresh.save({ ...first, name: '已修改', command: 'pwd\nls -la' })
    await fresh.delete(second.id)
    const reload = new CommandsStore(directory); await reload.init()
    expect(reload.list()).toEqual([{ ...first, name: '已修改', command: 'pwd\nls -la' }])
    expect(await readFile(join(directory, 'commands.json'), 'utf8')).not.toContain('hasCredentials')
  })

  it('preserves corrupt and unsupported command files and does not create a replacement', async () => {
    const { directory } = await store()
    for (const content of ['{broken', JSON.stringify({ version: 2, commands: [] }), JSON.stringify({ version: 1, commands: [{ ...sample, id: 'duplicate' }, { ...sample, id: 'duplicate' }] })]) {
      await writeFile(join(directory, 'commands.json'), content)
      const broken = new CommandsStore(directory)
      await expect(broken.init()).rejects.toThrow('原文件未被覆盖')
      await expect(broken.save(sample)).rejects.toThrow('未成功加载')
      expect(await readFile(join(directory, 'commands.json'), 'utf8')).toBe(content)
    }
  })

  it('validates command length, scope, identity and terminal control characters without damaging existing entries', async () => {
    const { service } = await store()
    const saved = await service.save(sample)
    for (const patch of [
      { name: '' }, { name: 'x'.repeat(81) }, { command: '' }, { command: 'x'.repeat(16385) },
      { command: '\x1b[31m' }, { command: 'hello\rworld' }, { profileId: '../server' }, { id: '../id' }
    ]) await expect(service.save({ ...saved, ...patch })).rejects.toThrow()
    expect(service.list()).toEqual([saved])
    await expect(service.save({ ...saved, command: 'echo {{x}}tail' })).rejects.toThrow('独立')
    expect(() => service.render('missing-command', {})).toThrow('不存在')
  })
})

describe('command parameter rendering', () => {
  it('quotes each parameter as one literal shell word and supports repeated parameters, empty values and Unicode', () => {
    expect(commandParameters('printf "%s" {{path}} {{path}} {{other}}')).toEqual(['path', 'other'])
    const value = "/tmp/it's $(touch unsafe); *中文"
    expect(renderCommand('printf "%s" {{path}} {{other}}', { path: value, other: '' })).toBe('printf "%s" \'/tmp/it\'\\\'\'s $(touch unsafe); *中文\' \'\'')
    expect(renderCommand('pwd\nprintf "%s" {{path}}', { path: '结果 文件.txt' })).toBe('pwd\nprintf "%s" \'结果 文件.txt\'')
    expect(renderCommand('python -c \'print({"a":{}})\'', {})).toBe('python -c \'print({"a":{}})\'')
  })

  it('rejects quoted, concatenated, escaped, commented and ambiguous shell placeholders', () => {
    for (const source of [
      'echo "{{path}}"', "echo '{{path}}'", 'echo --path={{path}}', 'echo {{path}}/file',
      'echo \\{{path}}', 'echo x # {{path}}', 'echo $(printf {{path}})', 'echo `printf {{path}}`', 'echo ${x:- {{path}} }',
      'cat <<EOF\n{{path}}\nEOF', 'echo {{bad-name}}', 'echo {{name', 'echo {{1name}}'
    ]) expect(() => commandParameters(source)).toThrow()
  })

  it('rejects missing, unknown, oversized and terminal-control parameter values', () => {
    expect(() => renderCommand(sample.command, { lines: '20' })).toThrow('path')
    expect(() => renderCommand('pwd', { unexpected: 'x' })).toThrow('未知')
    for (const path of ['a\nb', 'a\rb', 'a\tb', '\x1b[0m', 'x'.repeat(4097)]) expect(() => renderCommand(sample.command, { lines: '20', path })).toThrow()
    expect(() => renderCommand('echo {{x}}', Object.create({ x: 'inherited' }))).toThrow('请填写')
    expect(() => renderCommand('echo {{x}}', [] as unknown as Record<string, string>)).toThrow('格式')
  })
})
