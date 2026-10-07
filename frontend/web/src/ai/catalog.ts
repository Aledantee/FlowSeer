import { isPagePath } from '../navigation/page'
import type { AiUiNode } from './types'
import { validateEntityRef } from './validate'

export const AI_UI_ERROR_MESSAGE =
  'FlowSeer cannot show this part of the answer.'
export const AI_UI_MAX_NODES = 64
export const AI_UI_MAX_DEPTH = 4
export const AI_UI_MAX_STRING_LENGTH = 500

type AiUiProps = Record<string, unknown>

export interface AiUiCatalogEntry {
  children: boolean
  validate: (props: AiUiProps) => void
}

const CATALOG_PROP_KEYS = {
  UiCard: ['as'],
  UiBadge: ['text', 'variant', 'size'],
  UiStatusBadge: ['status', 'label', 'size'],
  UiMetricCard: ['label', 'value', 'unit'],
  UiMeter: ['label', 'value', 'min', 'max', 'unit', 'detail', 'tone'],
  UiProgress: [
    'modelValue',
    'max',
    'size',
    'variant',
    'ariaLabel',
    'valueText',
  ],
  UiSeparator: ['orientation', 'decorative'],
  UiEmptyState: ['title', 'description'],
  UiAiEntityChip: ['entity', 'size'],
  UiButton: ['text', 'intent', 'variant', 'size'],
} as const satisfies Record<string, readonly string[]>

const ENTITY_KEYS = ['kind', 'id', 'label'] as const
const INTENT_KEYS = ['type', 'target'] as const
const TARGET_KEYS = ['path', 'query'] as const

function fail(): never {
  throw new Error(AI_UI_ERROR_MESSAGE)
}

function isPlainObject(value: unknown): value is AiUiProps {
  if (typeof value !== 'object' || value === null || Array.isArray(value)) {
    return false
  }
  const prototype = Object.getPrototypeOf(value)
  return prototype === Object.prototype || prototype === null
}

function hasOnlyKeys(value: object, allowed: readonly string[]): boolean {
  const allowedKeys = new Set(allowed)
  return Reflect.ownKeys(value).every(
    (key) => typeof key === 'string' && allowedKeys.has(key),
  )
}

function isBoundedString(value: unknown): value is string {
  return typeof value === 'string' && value.length <= AI_UI_MAX_STRING_LENGTH
}

function isFiniteNumber(value: unknown): value is number {
  return typeof value === 'number' && Number.isFinite(value)
}

function isOneOf(value: unknown, choices: readonly string[]): value is string {
  return typeof value === 'string' && choices.includes(value)
}

function optionalString(value: unknown): void {
  if (value !== undefined && !isBoundedString(value)) fail()
}

function optionalFiniteNumber(value: unknown): void {
  if (value !== undefined && !isFiniteNumber(value)) fail()
}

function catalogEntry(
  component: keyof typeof CATALOG_PROP_KEYS,
  children: boolean,
  validateValues: (props: AiUiProps) => void,
): AiUiCatalogEntry {
  const allowedKeys = CATALOG_PROP_KEYS[component]
  return {
    children,
    validate: (props) => {
      if (!isPlainObject(props) || !hasOnlyKeys(props, allowedKeys)) fail()
      validateValues(props)
    },
  }
}

function validateEntity(value: unknown): void {
  if (!isPlainObject(value) || !hasOnlyKeys(value, ENTITY_KEYS)) fail()
  try {
    validateEntityRef(value)
  } catch {
    fail()
  }
  if (
    !isBoundedString(value.kind) ||
    !isBoundedString(value.id) ||
    !isBoundedString(value.label)
  ) {
    fail()
  }
  if (value.kind === 'device' && !isPagePath(`/devices/${value.id}`)) {
    fail()
  }
}

function validateQuery(value: unknown): void {
  if (!isPlainObject(value)) fail()
  for (const key of Reflect.ownKeys(value)) {
    const queryValue = typeof key === 'string' ? value[key] : undefined
    if (
      typeof key !== 'string' ||
      !isBoundedString(key) ||
      !isBoundedString(queryValue)
    ) {
      fail()
    }
  }
}

function validateIntent(value: unknown): void {
  if (!isPlainObject(value) || !hasOnlyKeys(value, INTENT_KEYS)) fail()
  if (value.type !== 'navigate' || !isPlainObject(value.target)) fail()
  if (!hasOnlyKeys(value.target, TARGET_KEYS)) fail()

  const hasPath = Object.hasOwn(value.target, 'path')
  const hasQuery = Object.hasOwn(value.target, 'query')
  if (!hasPath && !hasQuery) fail()
  const path = value.target.path
  if (hasPath) {
    if (!isBoundedString(path) || !isPagePath(path)) fail()
  }
  if (hasQuery) validateQuery(value.target.query)
}

export const AI_UI_CATALOG: Record<string, AiUiCatalogEntry> = {
  UiCard: catalogEntry('UiCard', true, (props) => {
    optionalString(props.as)
    if (
      props.as !== undefined &&
      !isOneOf(props.as, ['div', 'article', 'section'])
    ) {
      fail()
    }
  }),
  UiBadge: catalogEntry('UiBadge', false, (props) => {
    if (!isBoundedString(props.text)) fail()
    optionalString(props.variant)
    if (
      props.variant !== undefined &&
      !isOneOf(props.variant, [
        'default',
        'outline',
        'primary',
        'accent',
        'success',
        'warning',
        'danger',
        'info',
      ])
    ) {
      fail()
    }
    optionalString(props.size)
    if (props.size !== undefined && !isOneOf(props.size, ['sm', 'md'])) fail()
  }),
  UiStatusBadge: catalogEntry('UiStatusBadge', false, (props) => {
    if (!isOneOf(props.status, ['Healthy', 'Degraded', 'Offline'])) fail()
    optionalString(props.label)
    optionalString(props.size)
    if (props.size !== undefined && !isOneOf(props.size, ['sm', 'md'])) fail()
  }),
  UiMetricCard: catalogEntry('UiMetricCard', false, (props) => {
    if (!isBoundedString(props.label) || !isFiniteNumber(props.value)) fail()
    optionalString(props.unit)
  }),
  UiMeter: catalogEntry('UiMeter', false, (props) => {
    if (!isBoundedString(props.label) || !isFiniteNumber(props.value)) fail()
    optionalFiniteNumber(props.min)
    optionalFiniteNumber(props.max)
    optionalString(props.unit)
    optionalString(props.detail)
    optionalString(props.tone)
    if (
      props.tone !== undefined &&
      !isOneOf(props.tone, ['normal', 'warning', 'critical', 'auto'])
    ) {
      fail()
    }
  }),
  UiProgress: catalogEntry('UiProgress', false, (props) => {
    optionalFiniteNumber(props.modelValue)
    optionalFiniteNumber(props.max)
    optionalString(props.size)
    if (props.size !== undefined && !isOneOf(props.size, ['sm', 'md', 'lg'])) {
      fail()
    }
    optionalString(props.variant)
    if (
      props.variant !== undefined &&
      !isOneOf(props.variant, [
        'default',
        'accent',
        'success',
        'warning',
        'danger',
      ])
    ) {
      fail()
    }
    optionalString(props.ariaLabel)
    optionalString(props.valueText)
  }),
  UiSeparator: catalogEntry('UiSeparator', false, (props) => {
    optionalString(props.orientation)
    if (
      props.orientation !== undefined &&
      !isOneOf(props.orientation, ['horizontal', 'vertical'])
    ) {
      fail()
    }
    if (
      props.decorative !== undefined &&
      typeof props.decorative !== 'boolean'
    ) {
      fail()
    }
  }),
  UiEmptyState: catalogEntry('UiEmptyState', false, (props) => {
    if (!isBoundedString(props.title)) fail()
    optionalString(props.description)
  }),
  UiAiEntityChip: catalogEntry('UiAiEntityChip', false, (props) => {
    if (!Object.hasOwn(props, 'entity')) fail()
    validateEntity(props.entity)
    optionalString(props.size)
    if (props.size !== undefined && !isOneOf(props.size, ['sm', 'md'])) fail()
  }),
  UiButton: catalogEntry('UiButton', false, (props) => {
    if (!isBoundedString(props.text) || !Object.hasOwn(props, 'intent')) fail()
    validateIntent(props.intent)
    optionalString(props.variant)
    if (
      props.variant !== undefined &&
      !isOneOf(props.variant, ['primary', 'secondary', 'ghost', 'danger'])
    ) {
      fail()
    }
    optionalString(props.size)
    if (props.size !== undefined && !isOneOf(props.size, ['sm', 'md'])) fail()
  }),
}

function defineValue(
  target: AiUiProps,
  key: PropertyKey,
  value: unknown,
): void {
  Object.defineProperty(target, key, {
    configurable: true,
    enumerable: true,
    value,
    writable: true,
  })
}

function copyEntityValue(value: unknown): unknown {
  if (!isPlainObject(value) || !hasOnlyKeys(value, ENTITY_KEYS)) fail()
  const kind = value.kind
  const id = value.id
  const label = value.label
  return { kind, id, label }
}

function copyQueryValue(value: unknown): unknown {
  if (!isPlainObject(value)) fail()
  const keys = Reflect.ownKeys(value)
  const copy = Object.create(Object.getPrototypeOf(value)) as AiUiProps
  for (const key of keys) {
    const queryValue = Reflect.get(value, key)
    defineValue(copy, key, queryValue)
  }
  return copy
}

function copyIntentValue(value: unknown): unknown {
  if (!isPlainObject(value) || !hasOnlyKeys(value, INTENT_KEYS)) fail()
  const type = value.type
  const targetValue = value.target
  if (!isPlainObject(targetValue) || !hasOnlyKeys(targetValue, TARGET_KEYS)) {
    fail()
  }
  const target = Object.create(Object.getPrototypeOf(targetValue)) as AiUiProps
  if (Object.hasOwn(targetValue, 'path')) {
    const path = targetValue.path
    defineValue(target, 'path', path)
  }
  if (Object.hasOwn(targetValue, 'query')) {
    const query = targetValue.query
    defineValue(target, 'query', copyQueryValue(query))
  }
  const copy = Object.create(Object.getPrototypeOf(value)) as AiUiProps
  defineValue(copy, 'type', type)
  defineValue(copy, 'target', target)
  return copy
}

function copyProps(component: string, props: AiUiProps): AiUiProps {
  const copy = Object.create(Object.getPrototypeOf(props)) as AiUiProps
  for (const key of CATALOG_PROP_KEYS[
    component as keyof typeof CATALOG_PROP_KEYS
  ]) {
    if (!Object.hasOwn(props, key)) continue
    let value = props[key]
    if (component === 'UiAiEntityChip' && key === 'entity') {
      value = copyEntityValue(value)
    } else if (component === 'UiButton' && key === 'intent') {
      value = copyIntentValue(value)
    }
    defineValue(copy, key, value)
  }
  return copy
}

function validateNode(
  value: unknown,
  depth: number,
  count: { value: number },
): AiUiNode {
  if (++count.value > AI_UI_MAX_NODES || depth > AI_UI_MAX_DEPTH) fail()
  if (
    !isPlainObject(value) ||
    !hasOnlyKeys(value, ['component', 'props', 'children'])
  ) {
    fail()
  }
  const component = value.component
  if (!isBoundedString(component)) fail()
  const catalogEntryValue = Object.hasOwn(AI_UI_CATALOG, component)
    ? AI_UI_CATALOG[component]
    : undefined
  if (!catalogEntryValue || !Object.hasOwn(value, 'props')) fail()
  const props = value.props
  if (!isPlainObject(props)) fail()
  if (
    !hasOnlyKeys(
      props,
      CATALOG_PROP_KEYS[component as keyof typeof CATALOG_PROP_KEYS],
    )
  ) {
    fail()
  }
  const copiedProps = copyProps(component, props)
  catalogEntryValue.validate(copiedProps)

  const copy: AiUiNode = {
    component,
    props: copiedProps,
  }
  const hasChildren = Object.hasOwn(value, 'children')
  const children = hasChildren ? value.children : undefined
  if (hasChildren) {
    if (!catalogEntryValue.children || !Array.isArray(children)) fail()
    copy.children = validateNodes(children, depth + 1, count)
  }
  return copy
}

// Reads by index, so an empty slot reads `undefined` and rejects the tree.
function validateNodes(
  values: unknown[],
  depth: number,
  count: { value: number },
): AiUiNode[] {
  const copy: AiUiNode[] = []
  const length = values.length
  for (let index = 0; index < length; index++) {
    copy.push(validateNode(values[index], depth, count))
  }
  return copy
}

export function validateAiUiTree(value: unknown): AiUiNode[] {
  if (!Array.isArray(value)) fail()
  return validateNodes(value, 1, { value: 0 })
}
