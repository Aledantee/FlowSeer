// @vitest-environment happy-dom
import { afterEach, describe, expect, it, vi } from 'vitest'
import { createApp, h, nextTick, ref } from 'vue'
import UiTable from './UiTable.vue'
import UiTableHeader from './UiTableHeader.vue'
import UiTableBody from './UiTableBody.vue'
import UiTableRow from './UiTableRow.vue'
import UiTableHead from './UiTableHead.vue'
import UiTableCell from './UiTableCell.vue'
import UiTableEmpty from './UiTableEmpty.vue'
import { createWebI18n, type WebLocale } from '../../i18n'
import en from '../../i18n/locales/en.json'

let dispose = () => {}
afterEach(() => {
  dispose()
  document.body.replaceChildren()
  vi.restoreAllMocks()
})

let currentRender = () => h('div')

function overrideEnglish(
  i18n: ReturnType<typeof createWebI18n>,
  messages: Record<string, unknown>,
) {
  // mergeLocaleMessage writes into the catalog object every createWebI18n
  // shares, so the merge targets a private copy.
  i18n.global.setLocaleMessage('en', structuredClone(en))
  i18n.global.mergeLocaleMessage('en', messages)
}

function mountView(
  renderFn: () => unknown,
  locale: WebLocale = 'en',
  messages?: Record<string, unknown>,
) {
  const host = document.createElement('div')
  document.body.append(host)
  currentRender = renderFn as () => ReturnType<typeof h>
  const app = createApp({
    render() {
      return currentRender()
    },
  })
  const i18n = createWebI18n(locale)
  if (messages) overrideEnglish(i18n, messages)
  app.use(i18n)
  app.mount(host)
  dispose = () => app.unmount()
  return host
}

describe('UiTableHead', () => {
  it('renders sort button when sortable is true and sets aria-sort="ascending" / aria-sort="descending"', () => {
    const host = mountView(() =>
      h('table', [
        h('thead', [
          h('tr', [
            h(
              UiTableHead,
              { sortable: true, sortDirection: 'ascending' },
              () => 'Name',
            ),
            h(
              UiTableHead,
              { sortable: true, sortDirection: 'descending' },
              () => 'Status',
            ),
            h(UiTableHead, { sortable: false }, () => 'Actions'),
          ]),
        ]),
      ]),
    )

    const ths = host.querySelectorAll('th')
    expect(ths[0]?.getAttribute('aria-sort')).toBe('ascending')
    expect(ths[0]?.querySelector('button')).not.toBeNull()
    expect(ths[0]?.textContent).toContain('↑')

    expect(ths[1]?.getAttribute('aria-sort')).toBe('descending')
    expect(ths[1]?.querySelector('button')).not.toBeNull()
    expect(ths[1]?.textContent).toContain('↓')

    expect(ths[2]?.getAttribute('aria-sort')).toBeNull()
    expect(ths[2]?.querySelector('button')).toBeNull()
  })

  it('renders custom ascendingMark and descendingMark overrides', () => {
    const host = mountView(() =>
      h('table', [
        h('thead', [
          h('tr', [
            h(
              UiTableHead,
              {
                sortable: true,
                sortDirection: 'ascending',
                ascendingMark: ' [ASC]',
              },
              () => 'Name',
            ),
            h(
              UiTableHead,
              {
                sortable: true,
                sortDirection: 'descending',
                descendingMark: ' [DESC]',
              },
              () => 'Status',
            ),
          ]),
        ]),
      ]),
    )

    const ths = host.querySelectorAll('th')
    expect(ths[0]?.textContent).toContain(' [ASC]')
    expect(ths[1]?.textContent).toContain(' [DESC]')
  })

  it('takes the sort marks from the catalog', () => {
    const host = mountView(
      () =>
        h('table', [
          h('thead', [
            h('tr', [
              h(
                UiTableHead,
                { sortable: true, sortDirection: 'ascending' },
                () => 'Name',
              ),
              h(
                UiTableHead,
                { sortable: true, sortDirection: 'descending' },
                () => 'Status',
              ),
            ]),
          ]),
        ]),
      'en',
      {
        ui: { tableHead: { ascendingMark: '[up]', descendingMark: '[down]' } },
      },
    )

    const ths = host.querySelectorAll('th')
    expect(ths[0]?.querySelector('[aria-hidden="true"]')?.textContent).toBe(
      '[up]',
    )
    expect(ths[1]?.querySelector('[aria-hidden="true"]')?.textContent).toBe(
      '[down]',
    )
  })

  it('clicking a sortable header emits the sort event', async () => {
    const onSort = vi.fn()
    const host = mountView(() =>
      h('table', [
        h('thead', [
          h('tr', [h(UiTableHead, { sortable: true, onSort }, () => 'Name')]),
        ]),
      ]),
    )

    const button = host.querySelector('th button') as HTMLButtonElement
    expect(button).not.toBeNull()
    button.click()
    await nextTick()
    expect(onSort).toHaveBeenCalledTimes(1)
  })
})

describe('UiTable context and layout', () => {
  it('dense mode cascades down to adjust cell padding classes', () => {
    const host = mountView(() =>
      h(UiTable, { dense: true }, () => [
        h(UiTableHeader, () => [
          h(UiTableRow, () => [h(UiTableHead, () => 'Header')]),
        ]),
        h(UiTableBody, () => [
          h(UiTableRow, () => [h(UiTableCell, () => 'Cell')]),
        ]),
      ]),
    )

    const th = host.querySelector('th')
    const td = host.querySelector('td')
    expect(th?.className).toContain('py-2')
    expect(th?.className).toContain('px-3')
    expect(td?.className).toContain('py-2')
    expect(td?.className).toContain('px-3')
  })

  it('selected row sets data-state="selected"', () => {
    const host = mountView(() =>
      h(UiTable, () => [
        h(UiTableBody, () => [
          h(UiTableRow, { selected: true }, () => [
            h(UiTableCell, () => 'Row 1'),
          ]),
          h(UiTableRow, { selected: false }, () => [
            h(UiTableCell, () => 'Row 2'),
          ]),
        ]),
      ]),
    )

    const rows = host.querySelectorAll('tbody tr')
    expect(rows[0]?.getAttribute('data-state')).toBe('selected')
    expect(rows[1]?.getAttribute('data-state')).toBeNull()
  })

  it('row order remains unchanged in DOM when cell values are mutated if keys are stable', async () => {
    const items = ref([
      { id: 'd-1', name: 'Alpha', throughput: 100 },
      { id: 'd-2', name: 'Beta', throughput: 200 },
    ])

    const host = mountView(() =>
      h(UiTable, () => [
        h(UiTableBody, () =>
          items.value.map((item) =>
            h(UiTableRow, { key: item.id }, () => [
              h(UiTableCell, () => item.name),
              h(UiTableCell, () => `${item.throughput} Mbps`),
            ]),
          ),
        ),
      ]),
    )

    const rowsBefore = host.querySelectorAll('tbody tr')
    const firstRowEl = rowsBefore[0]
    const secondRowEl = rowsBefore[1]

    expect(firstRowEl?.textContent).toContain('Alpha')
    expect(firstRowEl?.textContent).toContain('100 Mbps')
    expect(secondRowEl?.textContent).toContain('Beta')
    expect(secondRowEl?.textContent).toContain('200 Mbps')

    items.value[0]!.throughput = 500
    await nextTick()

    const rowsAfter = host.querySelectorAll('tbody tr')
    expect(rowsAfter[0]).toBe(firstRowEl)
    expect(rowsAfter[1]).toBe(secondRowEl)
    expect(rowsAfter[0]?.textContent).toContain('500 Mbps')
  })

  it('UiTableEmpty renders full colSpan with text', () => {
    const host = mountView(() =>
      h(UiTable, () => [
        h(UiTableBody, () => [
          h(UiTableEmpty, { colSpan: 4 }, () => 'No data found'),
        ]),
      ]),
    )

    const td = host.querySelector('td')
    expect(td?.getAttribute('colspan')).toBe('4')
    expect(td?.textContent).toBe('No data found')
  })
})
