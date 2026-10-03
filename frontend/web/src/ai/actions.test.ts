import { describe, expect, it } from 'vitest'
import { aiActions } from './actions'

describe('aiActions', () => {
  it('returns verbs for an offline device', () => {
    const target = {
      kind: 'device',
      entity: { kind: 'device' as const, id: 'd1', label: 'sw-1' },
      context: { health: 'Offline' },
    }
    expect(aiActions(target)).toEqual([
      'Why is this offline?',
      'Summarize this device',
      'Ask about this…',
    ])
  })

  it('returns verbs for a degraded device', () => {
    const target = {
      kind: 'device',
      entity: { kind: 'device' as const, id: 'd2', label: 'sw-2' },
      context: { health: 'Degraded' },
    }
    expect(aiActions(target)).toEqual([
      'Why is this degraded?',
      'Summarize this device',
      'Ask about this…',
    ])
  })

  it('returns verbs for an online/healthy device', () => {
    const target = {
      kind: 'device',
      entity: { kind: 'device' as const, id: 'd3', label: 'sw-3' },
      context: { health: 'Healthy' },
    }
    expect(aiActions(target)).toEqual([
      'Summarize this device',
      'Ask about this…',
    ])
  })

  it('returns verbs for a site', () => {
    const target = {
      kind: 'site',
      entity: { kind: 'site' as const, id: 's1', label: 'Berlin' },
      context: {},
    }
    expect(aiActions(target)).toEqual([
      'Summarize this site',
      'Ask about this…',
    ])
  })

  it('returns verbs for a chart', () => {
    const target = {
      kind: 'chart',
      entity: { kind: 'chart' as const, id: 'c1', label: 'Traffic' },
      context: {},
    }
    expect(aiActions(target)).toEqual([
      'Explain this traffic',
      'Ask about this…',
    ])
  })

  it('returns verbs for a client', () => {
    const target = {
      kind: 'client',
      entity: { kind: 'client' as const, id: 'cli-1', label: 'Workstation' },
      context: {},
    }
    expect(aiActions(target)).toEqual([
      "Why is this client's signal weak?",
      'Ask about this…',
    ])
  })

  it('returns verbs for a dashboard view target', () => {
    const target = {
      kind: 'view',
      view: 'dashboard',
      context: {},
    }
    expect(aiActions(target)).toEqual([
      'Summarize this dashboard',
      'Ask about this…',
    ])
  })

  it('returns verbs for a device view target', () => {
    const target = {
      kind: 'view',
      view: 'device',
      context: {},
    }
    expect(aiActions(target)).toEqual([
      'Summarize this device',
      'Ask about this…',
    ])
  })

  it('returns Ask about this… for an unknown target or target without specific verbs', () => {
    const target = {
      kind: 'link',
      entity: { kind: 'link' as const, id: 'l1', label: 'Link 1' },
      context: {},
    }
    expect(aiActions(target)).toEqual(['Ask about this…'])
  })
})
