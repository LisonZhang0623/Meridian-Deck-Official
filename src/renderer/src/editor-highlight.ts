import { HighlightStyle } from '@codemirror/language'
import { tags } from '@lezer/highlight'

export const editorHighlight = HighlightStyle.define([
  { tag: [tags.keyword, tags.operatorKeyword], color: 'var(--syntax-keyword)', fontWeight: '600' },
  { tag: [tags.string, tags.regexp], color: 'var(--syntax-string)' },
  { tag: [tags.number, tags.bool, tags.null, tags.atom], color: 'var(--syntax-number)' },
  { tag: [tags.typeName, tags.className, tags.namespace], color: 'var(--syntax-type)' },
  { tag: [tags.function(tags.variableName), tags.function(tags.propertyName), tags.macroName], color: 'var(--syntax-function)' },
  { tag: [tags.propertyName, tags.attributeName], color: 'var(--syntax-property)' },
  { tag: [tags.comment, tags.processingInstruction], color: 'var(--syntax-comment)' },
  { tag: [tags.operator, tags.punctuation], color: 'var(--syntax-punctuation)' },
  { tag: [tags.escape, tags.special(tags.string)], color: 'var(--syntax-number)' },
  { tag: [tags.heading, tags.strong], fontWeight: 'bold' },
  { tag: tags.emphasis, fontStyle: 'italic' },
  { tag: [tags.url, tags.link], color: 'var(--syntax-function)', textDecoration: 'underline' },
  { tag: tags.invalid, color: 'var(--danger)', textDecoration: 'underline wavy' },
])
