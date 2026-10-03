// @vitest-environment happy-dom
import { describe, expect, it } from 'vitest'
import { fixtureIdentifiers, unmarkedIdentifiers } from './testing'

describe('fixtureIdentifiers', () => {
  it('collects names and addresses from fixtures without locations or kinds', () => {
    const identifiers = fixtureIdentifiers()

    expect(identifiers.has('berlin-gw-01')).toBe(true)
    expect(identifiers.has('10.20.0.1')).toBe(true)
    expect(identifiers.has('Berlin Mitte')).toBe(true)
    expect(identifiers.has('Aurora Hospitality')).toBe(true)

    // Locations and kinds are excluded from fixture identifiers
    expect(identifiers.has('Berlin, DE')).toBe(false)
    expect(identifiers.has('Gateway')).toBe(false)
    expect(identifiers.has('Core switch')).toBe(false)
  })
})

describe('unmarkedIdentifiers', () => {
  it('reports an unmarked identifier', () => {
    const container = document.createElement('div')
    container.innerHTML = '<p class="title"><span>berlin-gw-01</span></p>'
    const found = unmarkedIdentifiers(container, ['berlin-gw-01'])

    expect(found).toEqual([
      {
        identifier: 'berlin-gw-01',
        text: 'berlin-gw-01',
        path: 'div > p.title > span',
      },
    ])
  })

  it('ignores an identifier marked directly with translate="no"', () => {
    const container = document.createElement('div')
    container.innerHTML = '<span translate="no">berlin-gw-01</span>'

    expect(unmarkedIdentifiers(container, ['berlin-gw-01'])).toEqual([])
  })

  it('ignores an identifier inside an ancestor marked with translate="no"', () => {
    const container = document.createElement('div')
    container.innerHTML =
      '<div translate="no"><p><span>berlin-gw-01</span></p></div>'

    expect(unmarkedIdentifiers(container, ['berlin-gw-01'])).toEqual([])
  })

  it('ignores plain message text', () => {
    const container = document.createElement('div')
    container.innerHTML = '<p><span>Alle Standorte</span></p>'

    expect(
      unmarkedIdentifiers(container, ['berlin-gw-01', 'Berlin Mitte']),
    ).toEqual([])
  })

  it('ignores identifiers inside an exempt container selector', () => {
    const container = document.createElement('div')
    container.innerHTML = '<div role="tooltip"><span>berlin-gw-01</span></div>'

    expect(
      unmarkedIdentifiers(container, ['berlin-gw-01'], {
        exemptSelectors: ['[role="tooltip"]'],
      }),
    ).toEqual([])
  })

  it('matches only whole identifiers at boundaries', () => {
    const container = document.createElement('div')
    container.innerHTML = `
      <div>
        <span>berlin-gw-01-backup</span>
        <span>10.20.0.100</span>
        <span>Berlin, DE</span>
        <span>3c:22:00:00:00:00:11</span>
      </div>
    `
    const identifiers = [
      'berlin-gw-01',
      '10.20.0.1',
      'Berlin Mitte',
      '3c:22:00:00:00:00',
    ]

    expect(unmarkedIdentifiers(container, identifiers)).toEqual([])
  })
})
