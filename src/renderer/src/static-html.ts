import { rasterData, relativeRemotePath, type PreviewResources } from './preview-resources'

const tags = new Set(('html head body title style main article section nav header footer aside div p span h1 h2 h3 h4 h5 h6 ' +
  'pre code samp kbd figure figcaption img table thead tbody tfoot tr th td caption col colgroup ul ol li dl dt dd ' +
  'b strong i em u s del ins mark small sup sub blockquote q hr br wbr a abbr time details summary address ' +
  'bdi bdo cite var meter progress button label form fieldset legend input textarea select option canvas').split(' '))
const attributes = new Set('id class style title lang dir width height colspan rowspan scope span start reversed open align value min max step type checked selected disabled placeholder name for role tabindex'.split(' '))
const omit = new Set('noscript template iframe frame object embed math audio video base meta'.split(' '))
const svgNamespace = 'http://www.w3.org/2000/svg'
const svgTags = new Set(('svg g defs symbol use title desc style path rect circle ellipse line polyline polygon text tspan textPath ' +
  'linearGradient radialGradient stop pattern clipPath mask marker filter feGaussianBlur feOffset feColorMatrix feComposite ' +
  'feBlend feFlood feMerge feMergeNode feDropShadow').split(' '))
const svgAttributes = new Set(('viewBox preserveAspectRatio transform x y x1 y1 x2 y2 cx cy r rx ry fx fy fr dx dy ' +
  'd points pathLength fill fill-rule fill-opacity stroke stroke-width stroke-linecap stroke-linejoin stroke-dasharray ' +
  'stroke-dashoffset stroke-opacity stroke-miterlimit opacity color visibility display vector-effect ' +
  'gradientUnits gradientTransform spreadMethod offset stop-color stop-opacity ' +
  'patternUnits patternContentUnits patternTransform clip-path clip-rule clipPathUnits mask maskUnits maskContentUnits ' +
  'marker-start marker-mid marker-end markerWidth markerHeight markerUnits orient refX refY ' +
  'filter filterUnits primitiveUnits in in2 result stdDeviation mode operator values flood-color flood-opacity ' +
  'font-family font-size font-style font-weight text-anchor dominant-baseline letter-spacing textLength lengthAdjust').split(' '))
const policy = "default-src 'none'; script-src 'none'; style-src 'unsafe-inline'; img-src data:; form-action 'none'; base-uri 'none'; connect-src 'none'; frame-src 'none'; object-src 'none'"

export async function prepareHtmlPreview(source: string, assets?: PreviewResources, interactive = false) {
  if (source.length > 512 * 1024) throw new Error('网页预览最多支持 512 Ki 字符')
  // Template contents have an inert owner document, with no browsing context.
  // Both trees stay there, including while relative assets are being read.
  const inert = document.createElement('template').content.ownerDocument
  const input = inert.createElement('html')
  input.innerHTML = source
  let count = 0, resources = 0, hasScripts = false
  const reads: Promise<void>[] = []
  function copy(node: Node, depth: number): Node | undefined {
    if (++count > 16000 || depth > 100) throw new Error('网页结构过于复杂，请使用源码视图')
    if (node.nodeType === Node.TEXT_NODE) return inert.createTextNode(node.textContent || '')
    if (node.nodeType !== Node.ELEMENT_NODE) return
    const element = node as Element, tag = element.localName
    const svg = element.namespaceURI === svgNamespace
    if (svg ? !svgTags.has(tag) : element.namespaceURI !== 'http://www.w3.org/1999/xhtml' || omit.has(tag)) return
    if (!svg && tag === 'script') {
      const type = (element.getAttribute('type') ?? '').trim().toLowerCase()
      const executable = !element.hasAttribute('nomodule') && (!type || type === 'module' ||
        /^(?:(?:application|text)\/(?:x-)?(?:java|ecma)script|text\/(?:javascript1\.[0-5]|jscript|livescript))$/.test(type))
      const src = element.getAttribute('src')
      if (executable && (!interactive || src === null)) {
        hasScripts ||= src === null ? !!element.textContent?.trim() : !!assets && /\.(?:js|mjs)$/i.test(relativeRemotePath('/', src) ?? '')
      }
      if (!interactive) return
      const script = inert.createElement('script')
      for (const attr of ['type', 'defer', 'async', 'nomodule', 'id']) {
        if (element.hasAttribute(attr)) script.setAttribute(attr, element.getAttribute(attr)!)
      }
      if (!element.hasAttribute('src')) script.textContent = element.textContent
      else if (assets && resources++ < 32) {
        reads.push(assets.read(element.getAttribute('src')!, 'script').then(js => {
          if (js) {
            script.src = 'data:text/javascript;charset=utf-8,' + encodeURIComponent(js)
            if (executable && js.trim()) hasScripts = true
          }
        }))
      }
      return script
    }
    const stylesheet = tag === 'link' && element.getAttribute('rel')?.toLowerCase().split(/\s+/).includes('stylesheet')
    if (tag === 'link' && !stylesheet) return
    const target = svg ? inert.createElementNS(svgNamespace, tag) : inert.createElement(stylesheet ? 'style' : tags.has(tag) ? tag : 'span')
    for (const attr of element.attributes) {
      const eventHandler = /^on[a-z]+$/.test(attr.name) && attr.name in target
      if (eventHandler && attr.value.trim()) hasScripts = true
      if (attributes.has(attr.name) || attr.name.startsWith('data-') || attr.name.startsWith('aria-') ||
          (svg && svgAttributes.has(attr.name)) || (interactive && /^on[a-z]+$/.test(attr.name))) {
        target.setAttribute(attr.name, attr.value)
      } else if (svg && attr.localName === 'href' && /^#[^\s]+$/.test(attr.value)) {
        target.setAttribute('href', attr.value)
      }
    }
    if (tag === 'style' || stylesheet) {
      const setCss = (css: string) => { target.textContent = css.replace(/<\/style/gi, '<\\/style') }
      const media = element.getAttribute('media')
      if (media) target.setAttribute('media', media)
      if (tag === 'style') setCss(element.textContent || '')
      else if (assets && resources++ < 32) reads.push(assets.read(element.getAttribute('href') || '', 'css').then(css => { if (css) setCss(css) }))
      return target
    }
    if (tag === 'img') {
      target.setAttribute('alt', element.getAttribute('alt') || '')
      const src = element.getAttribute('src') || ''
      if (rasterData(src)) target.setAttribute('src', src)
      else if (assets && resources++ < 32) reads.push(assets.read(src, 'image').then(data => { if (data) target.setAttribute('src', data) }))
    }
    for (const child of node.childNodes) {
      const safe = copy(child, depth + 1)
      if (safe) target.appendChild(safe)
    }
    return target
  }
  const html = copy(input, 0) as HTMLElement
  const csp = inert.createElement('meta')
  csp.httpEquiv = 'Content-Security-Policy'
  await Promise.all(reads)
  csp.content = interactive && hasScripts ? policy.replace("script-src 'none'", "script-src 'unsafe-inline' data:") : policy
  html.querySelector('head')!.prepend(csp)
  return { html: '<!doctype html>' + html.outerHTML, hasScripts }
}
