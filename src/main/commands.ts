import { randomUUID } from 'node:crypto'
import { mkdir, readFile, rename, writeFile } from 'node:fs/promises'
import { join } from 'node:path'
import type { CommandSnippet } from '../shared/v2'
import { id, shellQuote, text } from './validation'

const MAX_COMMANDS = 500
const PARAMETER = /^[A-Za-z_][A-Za-z0-9_]{0,31}$/

// Substitution is deliberately limited to complete, unquoted shell arguments.
// Reject ambiguous shell contexts instead of treating a template as a shell parser.
export function commandParameters(command: string): string[] {
  const names = new Set<string>()
  let quote = '', escaped = false, comment = false
  if (command.includes('{{') && (/`|\$[({]|<</.test(command))) throw new Error('含参数的命令不支持命令替换、变量展开、反引号或 here-document')
  for (let i = 0; i < command.length; i++) {
    const char = command[i]
    if (char === '{' && command[i + 1] === '{') {
      const end = command.indexOf('}}', i + 2)
      const name = end < 0 ? '' : command.slice(i + 2, end)
      if (!PARAMETER.test(name)) throw new Error('参数格式应为 {{name}}，名称使用字母、数字和下划线，并以字母或下划线开头')
      if (quote || escaped || comment || i > 0 && !/\s/.test(command[i - 1]) || end + 2 < command.length && !/\s/.test(command[end + 2])) throw new Error('参数必须是独立、未加引号的参数，例如 tail -n {{lines}} -- {{path}}')
      names.add(name); i = end + 1
      if (names.size > 24) throw new Error('每条命令最多使用 24 个参数')
      continue
    }
    if (comment) { if (char === '\n') comment = false; continue }
    if (escaped) { escaped = false; continue }
    if (char === '\\' && quote !== "'") { escaped = true; continue }
    if (quote) { if (char === quote) quote = ''; continue }
    if (char === "'" || char === '"') quote = char
    else if (char === '#' && (i === 0 || /\s/.test(command[i - 1]))) comment = true
  }
  return [...names]
}

function validate(input: CommandSnippet): CommandSnippet {
  if (!input || typeof input !== 'object' || Array.isArray(input)) throw new Error('无效的命令收藏')
  const name = text(input.name, '命令名称', 80).trim()
  const command = text(input.command, '命令内容', 16384).replace(/\r\n/g, '\n')
  if (!name || !command.trim()) throw new Error('请填写命令名称和内容')
  if (/[\x00-\x08\x0b-\x1f\x7f]/.test(command)) throw new Error('命令内容不能包含终端控制字符')
  commandParameters(command)
  return {
    id: input.id ? id(input.id) : randomUUID(), name,
    group: text(input.group || '', '命令分组', 80).trim(),
    profileId: input.profileId ? id(input.profileId) : '', command,
    description: text(input.description || '', '命令说明', 500).trim()
  }
}

export function renderCommand(command: string, parameters: Record<string, string>): string {
  const names = commandParameters(command)
  if (!parameters || typeof parameters !== 'object' || Array.isArray(parameters) || Object.keys(parameters).length > 24) throw new Error('命令参数格式不正确')
  for (const name of Object.keys(parameters)) if (!names.includes(name)) throw new Error(`未知参数：${name}`)
  const values: Record<string, string> = Object.create(null)
  for (const name of names) {
    if (!Object.prototype.hasOwnProperty.call(parameters, name)) throw new Error(`请填写参数：${name}`)
    const value = text(parameters[name], `参数 ${name}`, 4096)
    if (/[\x00-\x1f\x7f]/.test(value)) throw new Error(`参数 ${name} 不能包含换行或控制字符`)
    values[name] = shellQuote(value)
  }
  const output = command.replace(/\{\{([A-Za-z_][A-Za-z0-9_]{0,31})\}\}/g, (_match, name: string) => values[name])
  if (output.length > 65536) throw new Error('展开后的命令过长')
  return output
}

export class CommandsStore {
  private commands: CommandSnippet[] = []
  private queue: Promise<unknown> = Promise.resolve()
  private ready = false
  constructor(private directory: string) {}
  async init(): Promise<void> {
    this.ready = false
    await mkdir(this.directory, { recursive: true })
    try {
      const raw = JSON.parse(await readFile(join(this.directory, 'commands.json'), 'utf8'))
      if (raw.version !== 1 || !Array.isArray(raw.commands) || raw.commands.length > MAX_COMMANDS) throw new Error('无效的命令收藏格式')
      const commands = raw.commands.map(validate) as CommandSnippet[]
      if (new Set(commands.map(command => command.id)).size !== commands.length) throw new Error('重复的命令标识符')
      this.commands = commands
    } catch (error) {
      if ((error as NodeJS.ErrnoException).code !== 'ENOENT') throw new Error('读取 commands.json 失败，原文件未被覆盖。', { cause: error })
    }
    this.ready = true
  }
  list(): CommandSnippet[] { this.assertReady(); return this.commands.map(command => ({ ...command })) }
  get(commandId: string): CommandSnippet {
    this.assertReady()
    const command = this.commands.find(item => item.id === id(commandId))
    if (!command) throw new Error('命令收藏不存在')
    return { ...command }
  }
  async save(input: CommandSnippet): Promise<CommandSnippet> {
    this.assertReady()
    const command = validate(input)
    await this.change(previous => {
      const index = previous.findIndex(item => item.id === command.id)
      if (index < 0) { if (previous.length >= MAX_COMMANDS) throw new Error('最多保存 500 条命令'); previous.push(command) }
      else previous[index] = command
      return previous
    })
    return { ...command }
  }
  async delete(commandId: string): Promise<void> {
    this.assertReady()
    id(commandId)
    await this.change(previous => previous.filter(item => item.id !== commandId))
  }
  render(commandId: string, parameters: Record<string, string>): string { return renderCommand(this.get(commandId).command, parameters) }
  private assertReady(): void { if (!this.ready) throw new Error('命令收藏未成功加载，请检查 commands.json；原文件不会被覆盖。') }
  private async change(update: (commands: CommandSnippet[]) => CommandSnippet[]): Promise<void> {
    const work = this.queue.catch(() => {}).then(async () => {
      const next = update(this.list())
      const file = join(this.directory, 'commands.json')
      await writeFile(file + '.tmp', JSON.stringify({ version: 1, commands: next }, null, 2), { encoding: 'utf8', mode: 0o600 })
      await rename(file + '.tmp', file)
      this.commands = next
    })
    this.queue = work
    await work
  }
}
