const editableLimit = 2 * 1024 * 1024

export function formatJson(source: string, encoding = 'UTF-8'): string {
  JSON.parse(source)
  // Preserve number literals, duplicate keys and ordering while changing whitespace.
  const tokens = source.match(/"(?:[^"\\]|\\.)*"|[\[\]{},:]|[^\s\[\]{},:]+/g) || []
  const parts: string[] = []
  let depth = 0, size = 0
  const add = (text: string) => {
    size += text.length
    if (size > editableLimit) throw new Error('格式化结果超过 2 MiB 编辑限制')
    parts.push(text)
  }
  const line = () => {
    if (depth > 256) throw new Error('JSON 嵌套层数过多')
    add('\n' + '  '.repeat(depth))
  }
  for (let i = 0; i < tokens.length; i++) {
    const token = tokens[i]
    switch (token) {
      case '{': case '[':
        add(token); depth++
        if (tokens[i + 1] !== (token === '{' ? '}' : ']')) line()
        break
      case '}': case ']':
        depth--
        if (tokens[i - 1] !== (token === '}' ? '{' : '[')) line()
        add(token)
        break
      case ',': add(','); line(); break
      case ':': add(': '); break
      default: add(token)
    }
  }
  if (/\r?\n$/.test(source)) add('\n')
  const result = parts.join('')
  const bytes = encoding.startsWith('UTF-16')
    ? result.length * 2 + 2
    : new TextEncoder().encode(result).byteLength + (encoding === 'UTF-8 BOM' ? 3 : 0)
  if (bytes > editableLimit) throw new Error('格式化结果超过 2 MiB 编辑限制')
  return result
}

export interface TablePreview { rows: string[][]; columns: number; truncated: boolean }

export function parseTable(source: string, delimiter: ',' | '\t'): TablePreview {
  const maxRows = 200, maxColumns = 50, maxCell = 2048
  const rows: string[][] = []
  let row: string[] = [], value = '', column = 0, columns = 0
  let quoted = false, closedQuote = false, truncated = false
  const append = (char: string) => {
    if (value.length < maxCell) value += char
    else truncated = true
  }
  const endCell = () => {
    if (column < maxColumns) row.push(value)
    else truncated = true
    column++
    value = ''
    closedQuote = false
  }
  const endRow = () => {
    endCell()
    columns = Math.max(columns, row.length)
    rows.push(row)
    row = []
    column = 0
  }
  for (let i = 0; i < source.length; i++) {
    const char = source[i]
    if (quoted) {
      if (char !== '"') append(char)
      else if (source[i + 1] === '"') { append('"'); i++ }
      else { quoted = false; closedQuote = true }
    } else if (char === delimiter) {
      endCell()
    } else if (char === '\n' || char === '\r') {
      if (char === '\r' && source[i + 1] === '\n') i++
      endRow()
      if (rows.length === maxRows) return { rows, columns, truncated: truncated || i < source.length - 1 }
    } else if (closedQuote || (char === '"' && value !== '')) {
      throw new Error(`第 ${rows.length + 1} 行的引号格式无效`)
    } else if (char === '"') {
      quoted = true
    } else {
      append(char)
    }
  }
  if (quoted) throw new Error(`第 ${rows.length + 1} 行的引号未闭合`)
  if (column > 0 || value !== '' || closedQuote) endRow()
  return { rows, columns, truncated }
}
