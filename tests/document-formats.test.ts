import { createSSRApp, h } from 'vue'
import { renderToString } from 'vue/server-renderer'
import { describe, expect, it } from 'vitest'
import { formatJson, parseTable } from '../src/renderer/src/document-formats'
import { renderMarkdown } from '../src/renderer/src/markdown-preview'

describe('document preview formats', () => {
  it('formats JSON without rounding integers or removing repeated keys', () => {
    expect(formatJson('{"z":9007199254740993,"z":1e999,"x":["a\\\"b",{}]}')).toBe([
      '{', '  "z": 9007199254740993,', '  "z": 1e999,', '  "x": [', '    "a\\\"b",', '    {}', '  ]', '}',
    ].join('\n'))
    expect(() => formatJson('{"x":}')).toThrow()
  })

  it('renders quoted fields, embedded delimiters and line breaks in tables', () => {
    expect(parseTable('name,value\r\n"one,two","said ""hi""\nagain"\r\n', ',')).toEqual({
      rows: [['name', 'value'], ['one,two', 'said "hi"\nagain']], columns: 2, truncated: false,
    })
    expect(parseTable('name\tscore\t\nA\t7\t', '\t').rows).toEqual([['name', 'score', ''], ['A', '7', '']])
    expect(() => parseTable('unclosed,"field', ',')).toThrow('引号未闭合')
  })

  it('bounds rows and columns in the rendered table', () => {
    const result = parseTable(Array(201).fill(Array(51).fill('value').join(',')).join('\n'), ',')
    expect(result.rows).toHaveLength(200)
    expect(result.rows[0]).toHaveLength(50)
    expect(result.truncated).toBe(true)
  })

  it('renders Markdown without active markup, links or remote image requests', async () => {
    const source = '# Heading\n\n**bold** [label](javascript:alert) ![remote](https://example.test/tracker)\n\n<script>alert(1)</script>\n\n| A | B |\n|---|---|\n| 1 | 2 |'
    const html = await renderToString(createSSRApp({ render: () => h('article', renderMarkdown(source)) }))
    expect(html).toContain('<h1> Heading</h1>')
    expect(html).toContain('<strong>bold</strong>')
    expect(html).toContain('&lt;script&gt;')
    expect(html).toContain('<th>A</th>')
    expect(html).toContain('label')
    expect(html).not.toMatch(/<(?:script|img|a)\b|javascript:|https:\/\/example\.test/)
  })
})
