import { describe, expect, it } from 'vitest'
import { createMockAiHandler } from './mock'
import type { AiRequest, AiResult, AiSummary } from './types'

const dashboardRequest: AiRequest = {
  requestId: 'r1',
  action: 'Summarize this dashboard',
  targets: [
    {
      id: 'a:dashboard:view:all',
      kind: 'view',
      view: 'dashboard',
      label: 'Dashboard · all sites',
      context: {
        scope: 'All sites',
        devices: '16',
        health: '1 offline, 1 degraded, 14 healthy',
        attention: 'cologne-ap-02 (offline), hamburg-ap-01 (degraded)',
        peak: '2642 Mbps at 13:00',
      },
    },
  ],
  history: [],
  signal: new AbortController().signal,
}

describe('createMockAiHandler', () => {
  it('yields two snapshots for a summary verb', async () => {
    const handler = createMockAiHandler(0)
    const iterable = handler(dashboardRequest) as AsyncIterable<AiSummary>
    const snapshots: AiSummary[] = []

    for await (const snapshot of iterable) {
      snapshots.push(snapshot)
    }

    expect(snapshots).toHaveLength(2)

    // Snapshot 1: headline and tone, empty findings and metrics
    expect(snapshots[0]?.type).toBe('summary')
    expect(snapshots[0]?.headline).toContain('Dashboard · all sites')
    expect(snapshots[0]?.tone).toBe('critical')
    expect(snapshots[0]?.findings).toEqual([])
    expect(snapshots[0]?.metrics).toEqual([])

    // Snapshot 2: findings and metrics filled from context
    expect(snapshots[1]?.type).toBe('summary')
    expect(snapshots[1]?.headline).toBe(snapshots[0]?.headline)
    expect(snapshots[1]?.findings.length).toBeGreaterThan(0)
    expect(snapshots[1]?.metrics.length).toBeGreaterThan(0)
    expect(snapshots[1]?.cause?.confidence).toBe('medium')
  })

  it('answers ask with a placeholder answer', async () => {
    const handler = createMockAiHandler(0)
    const askRequest: AiRequest = {
      ...dashboardRequest,
      action: 'ask',
      prompt: 'Why is traffic spiking?',
    }
    const iterable = handler(askRequest) as AsyncIterable<AiResult>
    const snapshots = []
    for await (const snapshot of iterable) {
      snapshots.push(snapshot)
    }

    expect(snapshots).toHaveLength(1)
    expect(snapshots[0]?.type).toBe('answer')
    if (snapshots[0]?.type === 'answer') {
      expect(snapshots[0].text).toContain('placeholder answer')
    }
  })
})
