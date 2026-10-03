import { parse } from 'vue/compiler-sfc'

// The `vue-i18n` warnings a missing key, a missing format, or a missing
// parent scope prints. Tests spy on `console.warn` and pass its calls here.
export function i18nWarnings(calls: unknown[][]): string[] {
  const issues: string[] = []
  for (const args of calls) {
    const text = args
      .map((a) =>
        typeof a === 'string' ? a : a instanceof Error ? a.message : String(a),
      )
      .join(' ')
    if (
      (text.includes('[intlify]') &&
        (text.includes('Not found') || text.includes('Fall back to'))) ||
      text.includes('Not found parent scope')
    ) {
      issues.push(text)
    }
  }
  return issues
}

export interface TemplateLiteral {
  line: number
  kind: 'text' | 'attribute'
  // The attribute name, set when `kind` is `attribute`.
  name?: string
  text: string
}

// Static attributes that a person reads or hears.
const TEXT_ATTRIBUTES = new Set([
  'aria-label',
  'aria-description',
  'title',
  'placeholder',
  'alt',
  'label',
  'hint',
  'description',
  'detail',
  'text',
])

const ELEMENT = 1
const TEXT = 2
const ATTRIBUTE = 6

interface AstNode {
  type: number
  loc: { start: { line: number }; source: string }
  content?: string
  name?: string
  value?: { content: string }
  props?: AstNode[]
  children?: AstNode[]
}

// Reports each template text node that is not whitespace, and each static
// attribute in TEXT_ATTRIBUTES with a value. An interpolation, a bound
// attribute, and a literal in `<script>` or in a bound expression are not
// seen. The checker reads this repository's own files and does not defend
// against hostile input.
export function templateLiterals(source: string): TemplateLiteral[] {
  const { descriptor } = parse(source)
  const found: TemplateLiteral[] = []
  const walk = (node: AstNode) => {
    if (node.type === TEXT && node.content?.trim()) {
      // A text node starts right after the previous tag, so the first
      // visible character can sit on a later line.
      const leading = node.loc.source.slice(
        0,
        node.loc.source.length - node.loc.source.trimStart().length,
      )
      found.push({
        line: node.loc.start.line + (leading.match(/\n/g)?.length ?? 0),
        kind: 'text',
        text: node.content.trim(),
      })
    }
    if (node.type === ELEMENT) {
      for (const prop of node.props ?? []) {
        if (
          prop.type === ATTRIBUTE &&
          prop.name &&
          TEXT_ATTRIBUTES.has(prop.name) &&
          prop.value?.content
        ) {
          found.push({
            line: prop.loc.start.line,
            kind: 'attribute',
            name: prop.name,
            text: prop.value.content,
          })
        }
      }
    }
    for (const child of node.children ?? []) walk(child)
  }
  const root = descriptor.template?.ast as unknown as AstNode | undefined
  if (root) walk(root)
  return found
}
