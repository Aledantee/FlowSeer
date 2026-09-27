// @vitest-environment happy-dom
import { afterEach, describe, expect, it } from 'vitest'
import {
  SHORTCUTS,
  dockTabShortcut,
  keysOf,
  matches,
  modalOpen,
  typingIn,
} from './shortcuts'

const key = (
  code: string,
  modifiers: Partial<
    Record<'altKey' | 'shiftKey' | 'metaKey' | 'ctrlKey', boolean>
  > = {},
) => ({
  code,
  altKey: false,
  shiftKey: false,
  metaKey: false,
  ctrlKey: false,
  ...modifiers,
})

afterEach(() => {
  document.body.replaceChildren()
})

describe('workspace shortcuts', () => {
  it('matches Cmd on macOS and Ctrl elsewhere for the platform modifier', () => {
    expect(
      matches(key('Backslash', { metaKey: true }), SHORTCUTS.toggleSplit, true),
    ).toBe(true)
    expect(
      matches(key('Backslash', { ctrlKey: true }), SHORTCUTS.toggleSplit, true),
    ).toBe(false)
    expect(
      matches(
        key('Backslash', { ctrlKey: true }),
        SHORTCUTS.toggleSplit,
        false,
      ),
    ).toBe(true)
  })
  it('requires the exact modifiers, so Alt-M and Alt-Shift-M differ', () => {
    expect(
      matches(key('KeyM', { altKey: true }), SHORTCUTS.minimize, true),
    ).toBe(true)
    expect(
      matches(
        key('KeyM', { altKey: true, shiftKey: true }),
        SHORTCUTS.minimize,
        true,
      ),
    ).toBe(false)
    expect(
      matches(
        key('KeyM', { altKey: true, shiftKey: true }),
        SHORTCUTS.dockPair,
        true,
      ),
    ).toBe(true)
  })
  it('shows keys in each platform’s modifier order', () => {
    expect(keysOf(SHORTCUTS.dockPair, true)).toEqual(['⌥', '⇧', 'M'])
    expect(keysOf(SHORTCUTS.dockPair, false)).toEqual(['Alt', 'Shift', 'M'])
    expect(keysOf(dockTabShortcut(3, true), true)).toEqual(['⌥', '⇧', '3'])
    expect(keysOf(SHORTCUTS.toggleSplit, true)).toEqual(['⌘', '\\'])
  })
})

describe('typingIn', () => {
  it('identifies text-editing targets including contenteditable elements', () => {
    expect(typingIn(null)).toBe(false)
    expect(typingIn(document.createElement('div'))).toBe(false)
    expect(typingIn(document.createElement('button'))).toBe(false)

    expect(typingIn(document.createElement('input'))).toBe(true)
    expect(typingIn(document.createElement('textarea'))).toBe(true)
    expect(typingIn(document.createElement('select'))).toBe(true)

    const editable = document.createElement('div')
    editable.contentEditable = 'true'
    expect(typingIn(editable)).toBe(true)

    const nonEditable = document.createElement('div')
    nonEditable.contentEditable = 'false'
    expect(typingIn(nonEditable)).toBe(false)
  })
})

describe('modalOpen', () => {
  it('detects open dialogs and alert dialogs, excluding nonmodal poppers', () => {
    expect(modalOpen()).toBe(false)

    const dialog = document.createElement('div')
    dialog.setAttribute('role', 'dialog')
    dialog.dataset.state = 'open'
    document.body.append(dialog)
    expect(modalOpen()).toBe(true)

    dialog.dataset.state = 'closed'
    expect(modalOpen()).toBe(false)

    dialog.dataset.state = 'open'
    const popper = document.createElement('div')
    popper.dataset.rekaPopperContentWrapper = ''
    document.body.append(popper)
    popper.append(dialog)
    expect(modalOpen()).toBe(false)

    const alertDialog = document.createElement('div')
    alertDialog.setAttribute('role', 'alertdialog')
    alertDialog.dataset.state = 'open'
    document.body.append(alertDialog)
    expect(modalOpen()).toBe(true)
  })
})
