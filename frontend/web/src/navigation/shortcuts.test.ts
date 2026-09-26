import { describe, expect, it } from 'vitest'
import { SHORTCUTS, dockTabShortcut, keysOf, matches } from './shortcuts'

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
