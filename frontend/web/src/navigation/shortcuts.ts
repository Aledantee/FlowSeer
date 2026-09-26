// Keyboard shortcuts for the workspace, defined once so the key handler and
// the tooltips that advertise them cannot drift apart. Keys are matched by
// physical code, so Option on macOS, which changes the typed character, still
// matches.
export interface Shortcut {
  code: string
  // Cmd on macOS, Ctrl elsewhere.
  mod?: boolean
  alt?: boolean
  shift?: boolean
}

export const SHORTCUTS = {
  search: { code: 'KeyK', mod: true },
  minimize: { code: 'KeyM', alt: true },
  dockPair: { code: 'KeyM', alt: true, shift: true },
  toggleSplit: { code: 'Backslash', mod: true },
  swap: { code: 'KeyS', alt: true },
  closeSide: { code: 'KeyW', alt: true },
  linkClicks: { code: 'KeyL', alt: true },
  focusMain: { code: 'ArrowLeft', alt: true },
  focusSide: { code: 'ArrowRight', alt: true },
} satisfies Record<string, Shortcut>

// Dock tabs one to nine open with Alt and their position, and beside the
// main page with Shift added.
export function dockTabShortcut(position: number, beside = false): Shortcut {
  return { code: `Digit${position}`, alt: true, shift: beside }
}

export function isMac(
  platform: string = globalThis.navigator?.platform ?? '',
): boolean {
  return /Mac|iPhone|iPad/.test(platform)
}

export function matches(
  event: Pick<
    KeyboardEvent,
    'code' | 'altKey' | 'shiftKey' | 'metaKey' | 'ctrlKey'
  >,
  shortcut: Shortcut,
  mac: boolean = isMac(),
): boolean {
  const mod = mac ? event.metaKey : event.ctrlKey
  const other = mac ? event.ctrlKey : event.metaKey
  return (
    event.code === shortcut.code &&
    event.altKey === Boolean(shortcut.alt) &&
    event.shiftKey === Boolean(shortcut.shift) &&
    mod === Boolean(shortcut.mod) &&
    !other
  )
}

const KEY_NAMES: Record<string, string> = {
  Backslash: '\\',
  ArrowLeft: '←',
  ArrowRight: '→',
  ArrowUp: '↑',
  ArrowDown: '↓',
  Enter: '↵',
  Escape: 'Esc',
}
function keyName(code: string): string {
  return KEY_NAMES[code] ?? code.replace(/^Key/, '').replace(/^Digit/, '')
}
// The keys to show, in each platform's customary modifier order.
export function keysOf(shortcut: Shortcut, mac: boolean = isMac()): string[] {
  const keys = mac
    ? [shortcut.alt && '⌥', shortcut.shift && '⇧', shortcut.mod && '⌘']
    : [shortcut.mod && 'Ctrl', shortcut.alt && 'Alt', shortcut.shift && 'Shift']
  return [
    ...keys.filter((key): key is string => Boolean(key)),
    keyName(shortcut.code),
  ]
}
