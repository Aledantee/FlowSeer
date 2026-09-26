// @vitest-environment happy-dom
import { afterEach, describe, expect, it } from 'vitest'
import { createApp, h } from 'vue'
import UiMetricCard from './UiMetricCard.vue'

let dispose = () => {}
afterEach(() => {
  dispose()
  document.body.replaceChildren()
})

describe('UiMetricCard', () => {
  it('renders label, value with tabular figures, icon slot, and note slot content', () => {
    const host = document.createElement('div')
    document.body.append(host)
    const app = createApp({
      render() {
        return h(
          UiMetricCard,
          {
            label: 'Total Bandwidth',
            value: 120,
            unit: 'Gbps',
          },
          {
            icon: () => h('span', { class: 'custom-icon' }, 'ICON'),
            default: () => 'Steady over 24h',
          },
        )
      },
    })
    app.mount(host)
    dispose = () => app.unmount()

    expect(host.textContent).toContain('Total Bandwidth')
    expect(host.textContent).toContain('120')
    expect(host.textContent).toContain('Gbps')
    expect(host.textContent).toContain('ICON')
    expect(host.textContent).toContain('Steady over 24h')

    const valueEl = host.querySelector('.metric-value')
    expect(valueEl).not.toBeNull()
    expect(valueEl?.className).toContain('font-mono')
    expect(valueEl?.className).toContain('tabular-nums')

    const iconEl = host.querySelector('.custom-icon')
    expect(iconEl).not.toBeNull()
  })
})
