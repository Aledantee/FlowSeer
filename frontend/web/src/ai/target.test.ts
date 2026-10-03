import { describe, expect, it } from 'vitest'
import { aiTarget, resolveTargetEntity } from './target'

describe('aiTarget and resolveTargetEntity', () => {
  it('maps device-related kinds to entity device with entityId', () => {
    const kinds = ['device', 'attention-device', 'role-device', 'downlink']
    for (const kind of kinds) {
      const entity = resolveTargetEntity(kind, 'dev-1', 'Switch 1')
      expect(entity).toEqual({
        kind: 'device',
        id: 'dev-1',
        label: 'Switch 1',
      })

      const target = aiTarget({
        slot: 'a',
        view: 'devices',
        kind,
        entityId: 'dev-1',
        label: 'Switch 1',
        context: {},
      })
      expect(target.view).toBe('devices')
      expect(target.entity).toEqual({
        kind: 'device',
        id: 'dev-1',
        label: 'Switch 1',
      })
    }
  })

  it('maps client kind to entity client', () => {
    const target = aiTarget({
      slot: 'a',
      view: 'clients',
      kind: 'client',
      entityId: 'cli-10',
      label: 'Laptop',
      context: {},
    })
    expect(target.entity).toEqual({
      kind: 'client',
      id: 'cli-10',
      label: 'Laptop',
    })
  })

  it('maps site kind to entity site', () => {
    const target = aiTarget({
      slot: 'standalone',
      view: 'sites',
      kind: 'site',
      entityId: 'site-berlin',
      label: 'Berlin Mitte',
      context: {},
    })
    expect(target.entity).toEqual({
      kind: 'site',
      id: 'site-berlin',
      label: 'Berlin Mitte',
    })
  })

  it('maps link kind to entity link', () => {
    const target = aiTarget({
      slot: 'b',
      view: 'topology',
      kind: 'link',
      entityId: 'link-1-2',
      label: 'Uplink 1',
      context: {},
    })
    expect(target.entity).toEqual({
      kind: 'link',
      id: 'link-1-2',
      label: 'Uplink 1',
    })
  })

  it('maps chart kind to entity chart', () => {
    const target = aiTarget({
      slot: 'a',
      view: 'dashboard',
      kind: 'chart',
      entityId: 'traffic-chart',
      label: 'Traffic Throughput',
      context: {},
    })
    expect(target.entity).toEqual({
      kind: 'chart',
      id: 'traffic-chart',
      label: 'Traffic Throughput',
    })
  })

  it('maps view kind to no entity', () => {
    const target = aiTarget({
      slot: 'a',
      view: 'dashboard',
      kind: 'view',
      entityId: 'all',
      label: 'Dashboard view',
      context: {},
    })
    expect(target.view).toBe('dashboard')
    expect(target.entity).toBeUndefined()
  })
})
