import { describe, expect, it, vi } from 'vitest'
import { h, createSSRApp } from 'vue'
import { renderToString } from 'vue/server-renderer'
import { renderMarkdown } from '../src/renderer/src/markdown-preview'
import { externalWebUrl, previewResources, relativeRemotePath } from '../src/renderer/src/preview-resources'
import { openTensorBoard } from '../src/renderer/src/tensorboard'
import type { Api } from '../src/shared/types'

describe('remote document previews', () => {
  it('resolves references in the remote document directory and opens only absolute web links', () => {
    expect(relativeRemotePath('/work/logs/doc.md', '../fig/a%20b.png?v=2')).toBe('/work/fig/a b.png')
    for (const ref of ['//example.test/x.png','/etc/x.png','https://example.test/a.png','data:image/png,aaa','%2Fabsolute.png','C:%5Cpic.png','#section']) {
      expect(relativeRemotePath('/work/doc.md', ref)).toBeUndefined()
    }
    expect(externalWebUrl('https://example.test/path')).toBe('https://example.test/path')
    expect(externalWebUrl('file:///tmp/x')).toBeUndefined()
    expect(externalWebUrl('https://name:password@example.test/')).toBeUndefined()
  })
  it('resolves reference-style images and keeps code and link labels as text', async () => {
    const image = vi.fn(() => h('span', 'diagram'))
    const code = vi.fn((text: string) => h('code', text))
    const link = vi.fn(() => h('span', 'website'))
    const source = ['![Figure][fig]', '', '[fig]: <plots/my chart.png>', '', '<https://example.test/>', '', '```python', 'if a < b:', '    pass', '```'].join('\n')
    const output = await renderToString(createSSRApp({ render: () => h('main', renderMarkdown(source, { image, code, link })) }))
    expect(image).toHaveBeenCalledWith('plots/my chart.png', 'Figure')
    expect(code).toHaveBeenCalledWith('if a < b:\n    pass', 'python')
    expect(link).toHaveBeenCalledWith('https://example.test/', ['https://example.test/'])
    expect(output).toContain('if a &lt; b:')
  })
  it('typesets inline and display math while keeping prices, escapes and code literal', async () => {
    const math = vi.fn((tex: string, display: boolean) => h(display ? 'div' : 'span', tex))
    const source = [
      String.raw`Price \$20 or $30 and $40. **$x_i^2$** and \(y_1\)`, '',
      '$$', String.raw`\frac{1}{2}`, '$$', '', String.raw`\[z^2\]`, '',
      '`$raw$`', '', '```js', 'const x = "$raw$"', '```', '', '```math', 'x+y', '```',
    ].join('\n')
    const html = await renderToString(createSSRApp({ render: () => h('main', renderMarkdown(source, { math })) }))
    expect(math.mock.calls).toEqual([['x_i^2', false], ['y_1', false], ['\\frac{1}{2}', true], ['z^2', true], ['x+y', true]])
    expect(html).toContain('Price $20 or $30 and $40.')
    expect(html).toContain('<code>$raw$</code>')
  })
  it('deduplicates same-session image reads, rejects external assets and stops on disposal', async () => {
    const data = 'data:image/png;base64,AAAA'
    const preview = vi.fn().mockResolvedValue({ ok: true, value: { content: data, kind: 'image', size: 3 } })
    const assets = previewResources({ preview }, 'session-a', '/reports/doc.md')
    expect(await Promise.all([assets.read('../img.png', 'image'), assets.read('../img.png', 'image')])).toEqual([data, data])
    expect(preview).toHaveBeenCalledExactlyOnceWith('session-a', '/img.png')
    expect(await assets.read('https://example.test/img.png', 'image')).toBeUndefined()
    const queued = assets.read('second.png', 'image')
    assets.dispose()
    expect(await queued).toBeUndefined()
    expect(preview).toHaveBeenCalledTimes(1)
  })
})

describe('TensorBoard shortcut', () => {
  const config = { name: 'TensorBoard', mode: 'local', localHost: '127.0.0.1', localPort: 0, remoteHost: '127.0.0.1', remotePort: 6012 }
  function setup(list: object[]) {
    const ok = (value: unknown) => Promise.resolve({ ok: true, value })
    return { listForwards: vi.fn(() => ok(list)), startForward: vi.fn(() => ok({ id: 'new' })), openForward: vi.fn(() => ok(undefined)), stopForward: vi.fn(() => ok(undefined)) }
  }
  it('allocates a loopback listener and opens its returned id', async () => {
    const api = setup([{ ...config, status: 'listening', id: 'other', sessionId: 'other' }])
    await openTensorBoard(api as unknown as Api, 'ssh', 6012, () => true)
    expect(api.startForward).toHaveBeenCalledExactlyOnceWith('ssh', config)
    expect(api.openForward).toHaveBeenCalledExactlyOnceWith('new')
  })
  it('closes a newly allocated listener when its session changes before browser handoff', async () => {
    const api = setup([])
    const current = vi.fn().mockReturnValueOnce(true).mockReturnValue(false)
    await openTensorBoard(api as unknown as Api, 'ssh', 6012, current)
    expect(api.stopForward).toHaveBeenCalledExactlyOnceWith('new')
    expect(api.openForward).not.toHaveBeenCalled()
  })
  it('reuses a compatible listening forward and ignores obsolete requests', async () => {
    const api = setup([{ ...config, status: 'listening', id: 'existing', sessionId: 'ssh', localPort: 32345 }])
    await openTensorBoard(api as unknown as Api, 'ssh', 6012, () => true)
    await openTensorBoard(api as unknown as Api, 'ssh', 6012, () => false)
    await expect(openTensorBoard(api as unknown as Api, 'ssh', 0, () => true)).rejects.toThrow()
    expect(api.openForward).toHaveBeenCalledExactlyOnceWith('existing')
    expect(api.startForward).not.toHaveBeenCalled()
  })
})
