import { GFM, parser } from '@lezer/markdown'
import { h, type VNodeChild } from 'vue'

const markdown = parser.configure([GFM, {
  defineNodes: ['InlineMath', 'DisplayMath'],
  parseInline: [{
    name: 'Math', before: 'Escape',
    parse(cx, next, pos) {
      if (next !== 36 && next !== 92) return -1
      const opener = cx.slice(pos, pos + 2)
      const close = opener === '$$' ? '$$' : opener === '\\[' ? '\\]' : opener === '\\(' ? '\\)' : next === 36 ? '$' : ''
      if (!close) return -1
      const start = pos + close.length
      const single = close === '$'
      if (single && (cx.char(pos - 1) === 36 || /\s/.test(cx.slice(start, start + 1)))) return -1
      const limit = Math.min(cx.end, start + 8192)
      for (let i = start; i < limit; i++) {
        const ch = cx.char(i)
        if (single && (ch === 10 || ch === 13)) break
        if (cx.slice(i, i + close.length) === close) {
          if (i === start || single && (/\s/.test(cx.slice(i - 1, i)) || /[\d$]/.test(cx.slice(i + 1, i + 2)))) return -1
          if (single && /[\p{P}\p{S}]/u.test(cx.slice(i - 1, i)) && /[\p{L}\p{N}]/u.test(cx.slice(i + 1, i + 2))) return -1
          const type = close === '$$' || close === '\\]' ? 'DisplayMath' : 'InlineMath'
          return cx.addElement(cx.elt(type, pos, i + close.length))
        }
        if (ch === 92) i++ // A TeX escape consumes the next character, including a literal dollar.
      }
      return -1
    },
  }],
}])
type Node = ReturnType<typeof markdown.parse>['topNode']
const ignoredMarkers = new Set(['HeaderMark', 'EmphasisMark', 'StrikethroughMark', 'LinkMark', 'CodeMark', 'CodeInfo', 'QuoteMark', 'ListMark', 'TableDelimiter'])
const inlineTags: Record<string, string> = { StrongEmphasis: 'strong', Emphasis: 'em', Strikethrough: 'del' }
const blockTags: Record<string, string> = { Paragraph: 'p', Blockquote: 'blockquote', BulletList: 'ul', ListItem: 'li', TableRow: 'tr' }
const entities: Record<string, string> = { amp: '&', lt: '<', gt: '>', quot: '"', apos: "'", nbsp: '\u00a0' }

export interface MarkdownViews {
  math?(tex: string, display: boolean): VNodeChild
  code?(text: string, language: string): VNodeChild
  image?(reference: string, alt: string): VNodeChild
  link?(destination: string, label: VNodeChild[]): VNodeChild
}
export function renderMarkdown(source: string, views: MarkdownViews = {}): VNodeChild[] {
  if (source.length > 256 * 1024) throw new Error('Markdown 渲染仅支持 256 Ki 字符以内的内容')
  let remainingNodes = 8000
  const raw = (node: Node) => source.slice(node.from, node.to)
  const root = markdown.parse(source).topNode
  const references = new Map<string, string>()
  const refName = (name: string) => name.trim().replace(/\s+/g, ' ').toLowerCase()
  root.toTree().iterate({ enter(node) {
    if (node.name !== 'LinkReference') return
    const label = node.node.getChild('LinkLabel'), url = node.node.getChild('URL')
    if (label && url) {
      const key = refName(raw(label).slice(1, -1))
      if (!references.has(key)) references.set(key, raw(url))
    }
  } })
  const blockChildren = (node: Node, depth: number): VNodeChild[] => {
    const children: VNodeChild[] = []
    for (let child = node.firstChild; child; child = child.nextSibling) {
      if (!ignoredMarkers.has(child.name)) children.push(render(child, depth + 1))
    }
    return children
  }
  const inline = (node: Node, depth: number, start = node.from, end = node.to): VNodeChild[] => {
    const children: VNodeChild[] = []
    let position = start
    for (let child = node.firstChild; child; child = child.nextSibling) {
      if (child.from < start || child.to > end) continue
      if (position < child.from) children.push(source.slice(position, child.from))
      if (!ignoredMarkers.has(child.name)) children.push(render(child, depth + 1))
      position = child.to
    }
    if (position < end) children.push(source.slice(position, end))
    return children
  }
  const render = (node: Node, depth: number): VNodeChild => {
    if (--remainingNodes < 0 || depth > 32) throw new Error('Markdown 结构过于复杂，请使用编辑视图查看')
    if (ignoredMarkers.has(node.name)) return null
    const heading = /^(?:ATX|Setext)Heading([1-6])$/.exec(node.name)
    if (heading) return h(`h${heading[1]}`, null, inline(node, depth))
    const textTag = inlineTags[node.name]
    if (textTag) return h(textTag, null, inline(node, depth))
    const blockTag = blockTags[node.name]
    if (blockTag) return h(blockTag, null, node.name === 'Paragraph' ? inline(node, depth) : blockChildren(node, depth))
    switch (node.name) {
      case 'OrderedList': {
        const marker = node.firstChild?.getChild('ListMark')
        return h('ol', { start: marker ? Number.parseInt(raw(marker), 10) : 1 }, blockChildren(node, depth))
      }
      case 'HorizontalRule': return h('hr')
      case 'Table': {
        const heading = node.getChild('TableHeader')
        return h('table', null, [
          heading ? h('thead', null, [render(heading, depth + 1)]) : null,
          h('tbody', null, node.getChildren('TableRow').map(row => render(row, depth + 1))),
        ])
      }
      case 'TableHeader': return h('tr', null, node.getChildren('TableCell').map(cell => h('th', null, inline(cell, depth))))
      case 'TableCell': return h('td', null, inline(node, depth))
      case 'InlineMath': case 'DisplayMath': {
        const text = raw(node)
        const width = text.startsWith('$$') || text.startsWith('\\') ? 2 : 1
        return views.math ? views.math(text.slice(width, -width).trim(), node.name === 'DisplayMath') : text
      }
      case 'HardBreak': return h('br')
      case 'FencedCode': case 'CodeBlock': {
        const text = node.getChildren('CodeText').map(raw).join('\n')
        const info = node.getChild('CodeInfo')
        const language = info && raw(info).trim().split(/\s+/, 1)[0]
        if (language === 'math' && views.math) return views.math(text, true)
        return language && views.code ? views.code(text, language) : h('pre', null, h('code', null, text))
      }
      case 'InlineCode': {
        const marks = node.getChildren('CodeMark')
        if (marks.length !== 2) return h('code', null, raw(node))
        let code = source.slice(marks[0].to, marks[1].from).replace(/\r?\n/g, ' ')
        if (code.startsWith(' ') && code.endsWith(' ') && /\S/.test(code)) code = code.slice(1, -1)
        return h('code', null, code)
      }
      case 'Escape': return raw(node).slice(1)
      case 'Entity': {
        const entity = raw(node).slice(1, -1)
        const number = /^#(x[\da-f]+|\d+)$/i.exec(entity)?.[1]
        if (!number) return entities[entity] ?? raw(node)
        const code = number[0].toLowerCase() === 'x' ? Number.parseInt(number.slice(1), 16) : Number(number)
        return code > 0 && code <= 0x10ffff && (code < 0xd800 || code > 0xdfff) ? String.fromCodePoint(code) : '\ufffd'
      }
      case 'Link': case 'Image': {
        const marks = node.getChildren('LinkMark')
        const start = marks[0]?.to, end = marks.find(mark => raw(mark) === ']')?.from
        const label = start !== undefined && end !== undefined && start <= end ? inline(node, depth, start, end) : [raw(node)]
        const title = start !== undefined && end !== undefined ? source.slice(start, end) : raw(node)
        const target = node.getChild('URL'), ref = node.getChild('LinkLabel')
        const destination = (target ? raw(target) : references.get(refName(ref && raw(ref).slice(1, -1) || title)))?.replace(/^<|>$/g, '')
        if (destination && node.name === 'Image' && views.image) return views.image(destination, title)
        if (destination && node.name === 'Link' && views.link) return views.link(destination, label)
        return h('span', { class: 'markdown-link-label' }, node.name === 'Image' ? ['[图片：', ...label, ']'] : label)
      }
      case 'URL': case 'Autolink': {
        const url = raw(node).replace(/^<|>$/g, '')
        return views.link ? views.link(url, [url]) : h('span', { class: 'markdown-link-label' }, url)
      }
      case 'HTMLBlock': return h('pre', null, raw(node))
      case 'HTMLTag': return h('code', null, raw(node))
      case 'LinkReference': return null
      default: return raw(node)
    }
  }
  return blockChildren(root, 0)
}
