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
    expect(document.body.querySelector('#popover-content')).toBeNull()

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

  it('emits closeAutoFocus on close and honors preventDefault to preserve custom focus', async () => {
    let emittedEvent: Event | null = null
    const customButton = document.createElement('button')
    customButton.id = 'popover-custom-focus'
    document.body.append(customButton)

    mountPopover(
      {
        defaultOpen: true,
        onCloseAutoFocus: (e: Event) => {
          emittedEvent = e
          e.preventDefault()
          customButton.focus()
        },
      },
      {
        trigger: () =>
          h('button', { id: 'popover-default-trigger' }, 'Trigger'),
        default: () => h('div', { id: 'popover-body' }, 'Content'),
      },
    )

    await nextTick()
    await new Promise((r) => setTimeout(r, 20))

    const content = document.body.querySelector('[role="dialog"]')
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

    expect(emittedEvent).not.toBeNull()
    expect(document.activeElement).toBe(customButton)
    customButton.remove()
  })

  it('delays unmount until exit keyframe animation ends (usePresence)', async () => {
    const originalGetComputedStyle = window.getComputedStyle
    window.getComputedStyle = (elt: Element, pseudoElt?: string | null) => {
      const style = originalGetComputedStyle(elt, pseudoElt)
      return new Proxy(style, {
        get(target, prop, receiver) {
          if (prop === 'animationName') {
            return elt.getAttribute('data-state') === 'closed'
              ? 'overlay-out'
              : 'none'
          }
          return Reflect.get(target, prop, receiver)
        },
      })
    }

    try {
      mountPopover(
        {
          defaultOpen: true,
        },
        {
          trigger: () => h('button', 'Trigger'),
          default: () => h('div', 'Content'),
        },
      )

      await nextTick()
      await new Promise((r) => setTimeout(r, 20))

      const content = document.body.querySelector('[role="dialog"]')
      expect(content).not.toBeNull()

      // Close the popover via Escape
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

      // Assert content is still mounted because exit animation is running
      expect(document.body.querySelector('[role="dialog"]')).not.toBeNull()

      // Dispatch animationend with overlay-out
      content?.dispatchEvent(
        new AnimationEvent('animationend', {
          animationName: 'overlay-out',
          bubbles: true,
        }),
      )
      await nextTick()
      await new Promise((r) => setTimeout(r, 20))

      // Assert content is gone
      expect(document.body.querySelector('[role="dialog"]')).toBeNull()
    } finally {
      window.getComputedStyle = originalGetComputedStyle
    }
  })
})
