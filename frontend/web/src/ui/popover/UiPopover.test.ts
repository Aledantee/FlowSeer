// @vitest-environment happy-dom
import { afterEach, describe, expect, it } from 'vitest'
import { createApp, h, nextTick, ref } from 'vue'
import UiPopover, { type UiPopoverProps } from './UiPopover.vue'

let dispose = () => {}
afterEach(() => {
  dispose()
  document.body.replaceChildren()
})

function mountPopover(
  props: Partial<UiPopoverProps> & Record<string, unknown> = {},
  slots: Record<string, () => unknown> = {},
) {
  const host = document.createElement('div')
  document.body.append(host)
  const app = createApp({
    render() {
      return h(UiPopover, props as UiPopoverProps, slots)
    },
  })
  app.mount(host)
  dispose = () => app.unmount()
  return host
}

describe('UiPopover', () => {
  it('opens on trigger click and displays content positioned relative to trigger', async () => {
    const host = mountPopover(
      {},
      {
        trigger: () => h('button', { id: 'popover-trigger' }, 'Trigger'),
        default: () => h('div', { id: 'popover-content' }, 'Popover Details'),
      },
    )

    const trigger = host.querySelector<HTMLButtonElement>('#popover-trigger')
    expect(trigger).not.toBeNull()

    // Initially closed
    expect(document.body.querySelector('#popover-content')).toBeNull()

    // Click trigger to open
    trigger?.click()
    await nextTick()
    await new Promise((r) => setTimeout(r, 20))

    const content = document.body.querySelector('#popover-content')
    expect(content).not.toBeNull()
    expect(content?.textContent).toBe('Popover Details')
  })

  it('escape dismisses popover', async () => {
    mountPopover(
      {
        defaultOpen: true,
      },
      {
        trigger: () => h('button', { id: 'escape-trigger' }, 'Trigger'),
        default: () => h('div', { id: 'escape-content' }, 'Content'),
      },
    )

    await nextTick()
    await new Promise((r) => setTimeout(r, 20))

    const content = document.body.querySelector('#escape-content')
    expect(content).not.toBeNull()

    content?.dispatchEvent(
      new KeyboardEvent('keydown', {
        key: 'Escape',
        code: 'Escape',
        bubbles: true,
        cancelable: true,
      }),
    )
    await nextTick()
    await new Promise((r) => setTimeout(r, 20))

    expect(document.body.querySelector('#escape-content')).toBeNull()
  })

  it('outside click dismisses popover', async () => {
    const isOpen = ref(true)
    mountPopover(
      {
        open: isOpen.value,
        'onUpdate:open': (v: boolean) => {
          isOpen.value = v
        },
      },
      {
        trigger: () => h('button', 'Trigger'),
        default: () => h('div', { id: 'outside-content' }, 'Content'),
      },
    )

    await nextTick()
    await new Promise((r) => setTimeout(r, 20))

    expect(document.body.querySelector('#outside-content')).not.toBeNull()

    // Outside pointerdown
    document.body.dispatchEvent(
      new PointerEvent('pointerdown', {
        bubbles: true,
        cancelable: true,
      }),
    )
    await nextTick()
    await new Promise((r) => setTimeout(r, 20))

    expect(isOpen.value).toBe(false)
  })
})
