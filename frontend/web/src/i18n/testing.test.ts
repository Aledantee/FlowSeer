// @vitest-environment happy-dom
import { describe, expect, it } from 'vitest'
import { unmarkedIdentifiers } from './testing'

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

  it.each([
    ['berlin-gw-01 + Devices', 'berlin-gw-01'],
    ['Berlin Mitte edge', 'Berlin Mitte'],
    ['(Gateway, 10.20.0.1)', '10.20.0.1'],
    ['über berlin-ap-01', 'berlin-ap-01'],
    ['Could not reach berlin-gw-01.', 'berlin-gw-01'],
    ['Reached 10.20.0.1.', '10.20.0.1'],
  ])(
    'reports an unmarked identifier inside longer text: %s',
    (text, identifier) => {
      const container = document.createElement('div')
      container.innerHTML = `<p><span>${text}</span></p>`
      const found = unmarkedIdentifiers(container, [
        'berlin-gw-01',
        'Berlin Mitte',
        '10.20.0.1',
        'berlin-ap-01',
      ])

      expect(found).toEqual([
        {
          identifier,
          text,
          path: 'div > p > span',
        },
      ])
    },
  )

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

  it('matches only whole identifiers at boundaries', () => {
    const container = document.createElement('div')
    container.innerHTML = `
      <div>
        <span>berlin-gw-01-backup</span>
        <span>xberlin-gw-01</span>
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

    const second = document.createElement('div')
    second.innerHTML = '<span>berlin-gw-01-backup, berlin-gw-01</span>'
    expect(unmarkedIdentifiers(second, ['berlin-gw-01'])).toEqual([
      {
        identifier: 'berlin-gw-01',
        text: 'berlin-gw-01-backup, berlin-gw-01',
        path: 'div > span',
      },
    ])
  })
})
