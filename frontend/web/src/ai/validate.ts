import type {
  AiAnswer,
  AiCause,
  AiConfidence,
  AiEntityKind,
  AiEntityRef,
  AiFinding,
  AiImpact,
  AiMetric,
  AiNextStep,
  AiResult,
  AiSeverity,
  AiSummary,
  AiTone,
} from './types'

export const AI_VALIDATION_ERROR_MESSAGE =
  'The AI returned a result FlowSeer cannot show.'

const VALID_TONES = new Set<AiTone>(['ok', 'warning', 'critical', 'unknown'])
const VALID_SEVERITIES = new Set<AiSeverity>(['info', 'warning', 'critical'])
const VALID_CONFIDENCES = new Set<AiConfidence>(['low', 'medium', 'high'])
const VALID_ENTITY_KINDS = new Set<AiEntityKind>([
  'device',
  'site',
  'client',
  'link',
  'chart',
])

function fail(): never {
  throw new Error(AI_VALIDATION_ERROR_MESSAGE)
}

function isObject(value: unknown): value is Record<string, unknown> {
  return typeof value === 'object' && value !== null && !Array.isArray(value)
}

function isString(value: unknown): value is string {
  return typeof value === 'string'
}

export function validateEntityRef(value: unknown): AiEntityRef {
  if (!isObject(value)) fail()
  if (
    !isString(value.kind) ||
    !VALID_ENTITY_KINDS.has(value.kind as AiEntityKind)
  ) {
    fail()
  }
  if (!isString(value.id) || !isString(value.label)) {
    fail()
  }
  return value as unknown as AiEntityRef
}

function validateFinding(value: unknown): AiFinding {
  if (!isObject(value)) fail()
  if (
    !isString(value.severity) ||
    !VALID_SEVERITIES.has(value.severity as AiSeverity)
  ) {
    fail()
  }
  if (!isString(value.title)) fail()
  if (value.detail !== undefined && !isString(value.detail)) fail()
  if (!Array.isArray(value.refs)) fail()
  value.refs.forEach(validateEntityRef)
  return value as unknown as AiFinding
}

function validateCause(value: unknown): AiCause {
  if (!isObject(value)) fail()
  if (!isString(value.text)) fail()
  if (
    !isString(value.confidence) ||
    !VALID_CONFIDENCES.has(value.confidence as AiConfidence)
  ) {
    fail()
  }
  if (!Array.isArray(value.refs)) fail()
  value.refs.forEach(validateEntityRef)
  return value as unknown as AiCause
}

function validateImpact(value: unknown): AiImpact {
  if (!isObject(value)) fail()
  if (!isString(value.text)) fail()
  if (!Array.isArray(value.refs)) fail()
  value.refs.forEach(validateEntityRef)
  return value as unknown as AiImpact
}

function validateMetric(value: unknown): AiMetric {
  if (!isObject(value)) fail()
  if (!isString(value.label) || !isString(value.value)) fail()
  if (
    value.tone !== undefined &&
    (!isString(value.tone) || !VALID_TONES.has(value.tone as AiTone))
  ) {
    fail()
  }
  return value as unknown as AiMetric
}

function validateNextStep(value: unknown): AiNextStep {
  if (!isObject(value)) fail()
  if (!isString(value.label)) fail()
  if (value.ref !== undefined) validateEntityRef(value.ref)
  return value as unknown as AiNextStep
}

export function validateAiSummary(value: unknown): AiSummary {
  if (!isObject(value)) fail()
  if (value.type !== 'summary') fail()
  if (!isString(value.headline)) fail()
  if (!isString(value.tone) || !VALID_TONES.has(value.tone as AiTone)) fail()

  if (!Array.isArray(value.findings)) fail()
  value.findings.forEach(validateFinding)

  if (value.cause !== undefined) validateCause(value.cause)
  if (value.impact !== undefined) validateImpact(value.impact)

  if (!Array.isArray(value.metrics)) fail()
  value.metrics.forEach(validateMetric)

  if (!Array.isArray(value.next)) fail()
  value.next.forEach(validateNextStep)

  if (!Array.isArray(value.sources)) fail()
  value.sources.forEach(validateEntityRef)

  return value as unknown as AiSummary
}

export function validateAiAnswer(value: unknown): AiAnswer {
  if (!isObject(value)) fail()
  if (value.type !== 'answer') fail()
  if (!isString(value.text)) fail()
  if (!Array.isArray(value.refs)) fail()
  value.refs.forEach(validateEntityRef)

  if (value.summary !== undefined) {
    validateAiSummary(value.summary)
  }

  return value as unknown as AiAnswer
}

export function validateAiResult(value: unknown): AiResult {
  if (!isObject(value)) fail()
  if (value.type === 'summary') return validateAiSummary(value)
  if (value.type === 'answer') return validateAiAnswer(value)
  fail()
}
