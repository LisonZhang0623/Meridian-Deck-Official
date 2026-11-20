export interface Variable { name: string; value: string }
const begin = '# >>> Meridian Deck environment >>>'
const end = '# <<< Meridian Deck environment <<<'
const escapedQuote = "'\\''"
function assignment(row: Variable) {
  return "export " + row.name + "='" + row.value.replaceAll("'", escapedQuote) + "'"
}

export function parseEnvironment(source: string) {
  const markers = [...source.matchAll(/^# (?:>>> Meridian Deck environment >>>|<<< Meridian Deck environment <<<)\r?$/gm)]
  const rows: Variable[] = []
  if (!markers.length) return { rows, before: source, after: '', block: '' }
  const [first, last] = markers
  if (markers.length !== 2 || first[0].trimEnd() !== begin || last[0].trimEnd() !== end)
    throw new Error('配置块标记不完整，请先在文本编辑器中检查 .bashrc')
  const start = first.index!, finish = last.index! + last[0].length
  const block = source.slice(start, finish).replace(/\r$/, '')
  for (const line of block.split(/\r?\n/).slice(1, -1)) {
    const match = /^export ([A-Za-z_][A-Za-z_0-9]*)='(.*)'$/.exec(line)
    if (!match) throw new Error('配置块含有手写语句，请先在文本编辑器中检查')
    const row = { name: match[1], value: match[2].replaceAll(escapedQuote, "'") }
    if (assignment(row) !== line || rows.some(other => other.name === row.name))
      throw new Error('配置块含有重复变量或无法识别的引号，请先检查 .bashrc')
    rows.push(row)
  }
  return { rows, before: source.slice(0, start), after: source.slice(start + block.length), block }
}

export function updateEnvironment(source: string, rows: Variable[]) {
  const { before, after, block } = parseEnvironment(source)
  const seen = new Set<string>()
  for (const { name, value } of rows) {
    if (!/^[A-Za-z_][A-Za-z_0-9]*$/.test(name)) throw new Error('变量名需以字母或下划线开头，后续可包含数字')
    if (seen.has(name)) throw new Error('变量名重复：' + name)
    if (/[\0\r\n]/.test(value)) throw new Error('变量值必须是单行文本')
    seen.add(name)
  }
  if (!block && !rows.length) return source
  const nl = source.includes('\r\n') ? '\r\n' : '\n'
  const prefix = before && !before.endsWith('\n') ? before + nl : before
  return prefix + [begin, ...rows.map(assignment), end].join(nl) + (block ? after : nl)
}

export function environmentDiff(oldRows: Variable[], newRows: Variable[]) {
  const changed = (a: Variable[], b: Variable[]) => a.filter(row => !b.some(other => other.name === row.name && other.value === row.value))
  return [...changed(oldRows, newRows).map(row => '− ' + assignment(row)), ...changed(newRows, oldRows).map(row => '+ ' + assignment(row))]
}
