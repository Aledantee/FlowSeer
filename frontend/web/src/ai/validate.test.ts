import { describe, expect, it } from 'vitest'
import { AI_VALIDATION_ERROR_MESSAGE, validateAiResult } from './validate'
import type { AiAnswer, AiSummary } from './types'

const validSummary: AiSummary = {
  type: 'summary',
  headline: 'Core switch healthy',
  tone: 'ok',
  findings: [
    {
      severity: 'info',
      title: 'Link established',
      detail: 'Port 1 up',
      refs: [{ kind: 'device', id: 'd1', label: 'core-1' }],
    },
  ],
  cause: {
    text: 'Normal operations',
    confidence: 'high',
    refs: [{ kind: 'device', id: 'd1', label: 'core-1' }],
  },
  impact: {
    text: 'Full throughput available',
    refs: [{ kind: 'device', id: 'd1', label: 'core-1' }],
  },
  metrics: [{ label: 'Bandwidth', value: '10 Gbps', tone: 'ok' }],
  next: [
    {
      label: 'Inspect port',
      ref: { kind: 'device', id: 'd1', label: 'core-1' },
    },
  ],
  sources: [{ kind: 'site', id: 's1', label: 'HQ' }],
}

const validAnswer: AiAnswer = {
  type: 'answer',
  text: 'Traffic is within normal thresholds.',
  refs: [{ kind: 'chart', id: 'c1', label: 'Throughput' }],
  summary: validSummary,
}

describe('validateAiResult', () => {
  it('accepts a valid AiSummary and AiAnswer', () => {
    expect(validateAiResult(validSummary)).toEqual(validSummary)
    expect(validateAiResult(validAnswer)).toEqual(validAnswer)
  })

  it('accepts an answer with UI for render-time validation', () => {
    const answer: AiAnswer = {
      type: 'answer',
      text: validAnswer.text,
      refs: validAnswer.refs,
      ui: [{ component: 'div', props: {} }],
    }

    expect(validateAiResult(answer)).toEqual(answer)
  })

  it('rejects a missing type or invalid type', () => {
    expect(() => validateAiResult(null)).toThrow(AI_VALIDATION_ERROR_MESSAGE)
    expect(() => validateAiResult(undefined)).toThrow(
      AI_VALIDATION_ERROR_MESSAGE,
    )
    expect(() => validateAiResult(123)).toThrow(AI_VALIDATION_ERROR_MESSAGE)
    expect(() => validateAiResult('some string')).toThrow(
      AI_VALIDATION_ERROR_MESSAGE,
    )
    expect(() => validateAiResult({})).toThrow(AI_VALIDATION_ERROR_MESSAGE)
    expect(() => validateAiResult({ type: 'unknown' })).toThrow(
      AI_VALIDATION_ERROR_MESSAGE,
    )
  })

  it('rejects an invalid tone or wrong type for tone in AiSummary', () => {
    expect(() =>
      validateAiResult({ ...validSummary, tone: 'invalid-tone' }),
    ).toThrow(AI_VALIDATION_ERROR_MESSAGE)
    expect(() => validateAiResult({ ...validSummary, tone: 42 })).toThrow(
      AI_VALIDATION_ERROR_MESSAGE,
    )
    expect(() =>
      validateAiResult({ ...validSummary, tone: undefined }),
    ).toThrow(AI_VALIDATION_ERROR_MESSAGE)
  })

  it('rejects wrong type for headline in AiSummary', () => {
    expect(() => validateAiResult({ ...validSummary, headline: 100 })).toThrow(
      AI_VALIDATION_ERROR_MESSAGE,
    )
    expect(() =>
      validateAiResult({ ...validSummary, headline: undefined }),
    ).toThrow(AI_VALIDATION_ERROR_MESSAGE)
  })

  it('rejects wrong type or invalid enum in findings', () => {
    expect(() =>
      validateAiResult({ ...validSummary, findings: 'none' }),
    ).toThrow(AI_VALIDATION_ERROR_MESSAGE)
    expect(() =>
      validateAiResult({
        ...validSummary,
        findings: [{ severity: 'catastrophic', title: 'Boom', refs: [] }],
      }),
    ).toThrow(AI_VALIDATION_ERROR_MESSAGE)
    expect(() =>
      validateAiResult({
        ...validSummary,
        findings: [{ severity: 'warning', title: 123, refs: [] }],
      }),
    ).toThrow(AI_VALIDATION_ERROR_MESSAGE)
    expect(() =>
      validateAiResult({
        ...validSummary,
        findings: [{ severity: 'warning', title: 'T', detail: 123, refs: [] }],
      }),
    ).toThrow(AI_VALIDATION_ERROR_MESSAGE)
    expect(() =>
      validateAiResult({
        ...validSummary,
        findings: [{ severity: 'warning', title: 'T', refs: 'invalid' }],
      }),
    ).toThrow(AI_VALIDATION_ERROR_MESSAGE)
  })

  it('rejects wrong type or invalid enum in cause', () => {
    expect(() =>
      validateAiResult({
        ...validSummary,
        cause: { text: 123, confidence: 'high', refs: [] },
      }),
    ).toThrow(AI_VALIDATION_ERROR_MESSAGE)
    expect(() =>
      validateAiResult({
        ...validSummary,
        cause: { text: 'Cause', confidence: 'absolute', refs: [] },
      }),
    ).toThrow(AI_VALIDATION_ERROR_MESSAGE)
    expect(() =>
      validateAiResult({
        ...validSummary,
        cause: { text: 'Cause', confidence: 'medium', refs: 'none' },
      }),
    ).toThrow(AI_VALIDATION_ERROR_MESSAGE)
  })

  it('rejects wrong type in impact', () => {
    expect(() =>
      validateAiResult({
        ...validSummary,
        impact: { text: 999, refs: [] },
      }),
    ).toThrow(AI_VALIDATION_ERROR_MESSAGE)
    expect(() =>
      validateAiResult({
        ...validSummary,
        impact: { text: 'Impact', refs: 'none' },
      }),
    ).toThrow(AI_VALIDATION_ERROR_MESSAGE)
  })

  it('rejects wrong type or invalid tone in metrics', () => {
    expect(() => validateAiResult({ ...validSummary, metrics: null })).toThrow(
      AI_VALIDATION_ERROR_MESSAGE,
    )
    expect(() =>
      validateAiResult({
        ...validSummary,
        metrics: [{ label: 1, value: '10' }],
      }),
    ).toThrow(AI_VALIDATION_ERROR_MESSAGE)
    expect(() =>
      validateAiResult({
        ...validSummary,
        metrics: [{ label: 'L', value: 10 }],
      }),
    ).toThrow(AI_VALIDATION_ERROR_MESSAGE)
    expect(() =>
      validateAiResult({
        ...validSummary,
        metrics: [{ label: 'L', value: '10', tone: 'bad' }],
      }),
    ).toThrow(AI_VALIDATION_ERROR_MESSAGE)
  })

  it('rejects wrong type in next steps', () => {
    expect(() => validateAiResult({ ...validSummary, next: 'none' })).toThrow(
      AI_VALIDATION_ERROR_MESSAGE,
    )
    expect(() =>
      validateAiResult({
        ...validSummary,
        next: [{ label: 42 }],
      }),
    ).toThrow(AI_VALIDATION_ERROR_MESSAGE)
  })

  it('rejects wrong type or unknown kind in refs and sources', () => {
    expect(() =>
      validateAiResult({
        ...validSummary,
        sources: [{ kind: 'unknown_kind', id: '1', label: 'L' }],
      }),
    ).toThrow(AI_VALIDATION_ERROR_MESSAGE)
    expect(() =>
      validateAiResult({
        ...validSummary,
        sources: [{ kind: 'device', id: 1, label: 'L' }],
      }),
    ).toThrow(AI_VALIDATION_ERROR_MESSAGE)
    expect(() =>
      validateAiResult({
        ...validSummary,
        sources: [{ kind: 'device', id: '1', label: 2 }],
      }),
    ).toThrow(AI_VALIDATION_ERROR_MESSAGE)
  })

  it('rejects wrong type in AiAnswer text and refs', () => {
    expect(() => validateAiResult({ ...validAnswer, text: 123 })).toThrow(
      AI_VALIDATION_ERROR_MESSAGE,
    )
    expect(() => validateAiResult({ ...validAnswer, refs: 'none' })).toThrow(
      AI_VALIDATION_ERROR_MESSAGE,
    )
    expect(() =>
      validateAiResult({
        ...validAnswer,
        summary: { ...validSummary, tone: 'invalid' },
      }),
    ).toThrow(AI_VALIDATION_ERROR_MESSAGE)
  })
})
