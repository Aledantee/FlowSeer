import { describe, expect, it } from 'vitest'
import { devices } from './fleet'
import { clientsOf } from './clients'
import { searchAll } from './search'

describe('global search', () => {
  it('finds every kind of object from one query box', () => {
    const client = clientsOf(devices)[0]
    if (!client) throw new Error('Missing fixture')
    expect(searchAll('aurora', devices)[0]?.kind).toBe('tenant')
    expect(searchAll('hamburg hafen', devices)[0]?.kind).toBe('site')
    expect(searchAll('berlin-sw-01', devices)[0]).toMatchObject({
      kind: 'device',
      title: 'berlin-sw-01',
    })
    expect(
      searchAll(client.mac, devices).some(
        (result) => result.kind === 'client' && result.id === client.id,
      ),
    ).toBe(true)
    expect(
      searchAll('berlin-sw-01 ge-0/0/1', devices).find(
        (result) => result.kind === 'interface',
      ),
    ).toMatchObject({ port: 'ge-0/0/1' })
  })
  it('ranks an exact name above a partial one', () => {
    const names = searchAll('berlin-ap-01', devices)
      .filter((result) => result.kind === 'device')
      .map((result) => result.title)
    expect(names[0]).toBe('berlin-ap-01')
  })
  it('returns nothing for an empty query', () => {
    expect(searchAll('   ', devices)).toEqual([])
  })
  it('returns only what identifies and titles a result', () => {
    const kinds = new Set<string>()
    for (const query of ['aurora', 'hamburg', 'berlin-sw-01', 'ge-0/0/1']) {
      for (const result of searchAll(query, devices)) {
        kinds.add(result.kind)
        expect(Object.keys(result).sort()).toEqual(
          result.kind === 'interface'
            ? ['id', 'kind', 'port', 'title']
            : ['id', 'kind', 'title'],
        )
      }
    }
    expect([...kinds].sort()).toEqual(['device', 'interface', 'site', 'tenant'])
  })
})
