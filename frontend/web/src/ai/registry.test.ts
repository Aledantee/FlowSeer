// @vitest-environment happy-dom
import { afterEach, describe, expect, it, vi } from 'vitest'
import { AiStaleError, AiUnavailableError, createAiRegistry } from './registry'
import type { AiRegistry, AiViewport } from './registry'
import { installAiWindow } from './window'
import type { AiRequest, AiTarget } from './types'

function target(id: string, extra: Partial<AiTarget> = {}): AiTarget {
  return {
    id,
    kind: 'device',
    label: id,
    context: { entity: id },
    ...extra,
  }
}

function element(): HTMLElement {
  const node = document.createElement('div')
  document.body.append(node)
  return node
}

function viewport(initial: AiViewport): {
  registry: AiRegistry
  set: (next: AiViewport) => void
} {
  let current = initial
  const registry = createAiRegistry({ viewport: () => current })
  return {
    registry,
    set: (next) => {
      current = next
    },
  }
}

afterEach(() => {
  document.body.replaceChildren()
})

describe('target registry', () => {
  it('lists mounted targets in stable sorted order', () => {
    const registry = createAiRegistry()
    registry.register(element(), target('b:devices:device:d2'))
    registry.register(element(), target('a:devices:device:d1'))

    expect(registry.list().map((item) => item.id)).toEqual([
      'a:devices:device:d1',
      'b:devices:device:d2',
    ])
  })

  it('keeps the first registration of a duplicate id and warns', () => {
    const warn = vi.spyOn(console, 'warn').mockImplementation(() => {})
    const registry = createAiRegistry()
    const first = element()
    const second = element()
    registry.register(first, target('a:devices:device:d1', { label: 'first' }))
    registry.register(
      second,
      target('a:devices:device:d1', { label: 'second' }),
    )

    expect(registry.list()).toHaveLength(1)
    expect(registry.list()[0]?.label).toBe('first')
    expect(warn).toHaveBeenCalledOnce()
    warn.mockRestore()
  })

  it('replaces a target when its element updates to a new id', () => {
    const registry = createAiRegistry()
    const node = element()
    registry.register(node, target('a:devices:device:d1'))
    registry.register(node, target('a:devices:device:d2'))

    expect(registry.list().map((item) => item.id)).toEqual([
      'a:devices:device:d2',
    ])
  })

  it('exposes only the mounted copy a viewport shows', () => {
    const { registry, set } = viewport({ wide: true, narrow: false })
    registry.register(
      element(),
      target('a:devices:device:desktop:d1', { segment: 'desktop' }),
    )
    registry.register(
      element(),
      target('a:devices:device:mobile:d1', { segment: 'mobile' }),
    )

    expect(registry.list().map((item) => item.id)).toEqual([
      'a:devices:device:desktop:d1',
    ])

    set({ wide: false, narrow: true })

    expect(registry.list().map((item) => item.id)).toEqual([
      'a:devices:device:mobile:d1',
    ])
  })

  it('drops a target whose element leaves the document', () => {
    const registry = createAiRegistry()
    const node = element()
    registry.register(node, target('a:devices:device:d1'))
    node.remove()

    expect(registry.list()).toHaveLength(0)
  })

  it('scrolls the exact mounted element into view on highlight', () => {
    const registry = createAiRegistry()
    const scrollArea = element()
    const pane = document.createElement('div')
    const row = document.createElement('div')
    scrollArea.append(pane)
    pane.append(row)

    const scrollSpy = vi.spyOn(row, 'scrollIntoView')
    registry.register(
      row,
      target('a:devices:device:desktop:dev-13', { segment: 'desktop' }),
    )

    expect(registry.highlight('a:devices:device:desktop:dev-13')).toBe(true)
    expect(scrollSpy).toHaveBeenCalledTimes(1)
    expect(scrollSpy).toHaveBeenCalledWith({
      block: 'nearest',
      inline: 'nearest',
    })
    expect(registry.selection()?.element).toBe(row)
  })

  it('clears an old selection and does not scroll when highlighting an unknown id', () => {
    const registry = createAiRegistry()
    const node = element()
    const scrollSpy = vi.spyOn(node, 'scrollIntoView')
    registry.register(
      node,
      target('a:devices:device:desktop:d1', { segment: 'desktop' }),
    )
    expect(registry.highlight('a:devices:device:desktop:d1')).toBe(true)
    expect(scrollSpy).toHaveBeenCalledWith({
      block: 'nearest',
      inline: 'nearest',
    })
    expect(registry.selection()?.target.id).toBe('a:devices:device:desktop:d1')

    scrollSpy.mockClear()
    expect(registry.highlight('a:devices:device:missing')).toBe(false)
    expect(scrollSpy).not.toHaveBeenCalled()
    expect(registry.selection()).toBeUndefined()
  })

  it('returns false and does not scroll when highlighting a hidden segment', () => {
    const { registry } = viewport({ wide: true, narrow: false })
    const node = element()
    const scrollSpy = vi.spyOn(node, 'scrollIntoView')
    registry.register(
      node,
      target('a:devices:device:mobile:d1', { segment: 'mobile' }),
    )

    expect(registry.highlight('a:devices:device:mobile:d1')).toBe(false)
    expect(scrollSpy).not.toHaveBeenCalled()
  })

  it('excludes targets inside a CSS-hidden ancestor across all target kinds', async () => {
    const registry = createAiRegistry()
    const card = element()
    const chart = document.createElement('div')
    card.append(chart)

    const chartTarget = target('a:dashboard:chart:traffic', {
      kind: 'chart',
      label: 'Traffic',
    })
    registry.register(chart, chartTarget)

    expect(registry.list().map((item) => item.id)).toEqual([
      'a:dashboard:chart:traffic',
    ])
    expect(registry.view('a:dashboard:chart:traffic')).toBeDefined()
    expect(registry.idForElement(chart)).toBe('a:dashboard:chart:traffic')

    card.style.display = 'none'

    expect(registry.list()).toEqual([])
    expect(registry.view('a:dashboard:chart:traffic')).toBeUndefined()
    expect(registry.idForElement(chart)).toBeUndefined()
    expect(registry.highlight('a:dashboard:chart:traffic')).toBe(false)
    expect(registry.selection()).toBeUndefined()

    await expect(
      registry.request(chartTarget, { kind: 'ask' }),
    ).rejects.toBeInstanceOf(AiStaleError)

    card.style.display = ''
    const scrollSpy = vi.spyOn(chart, 'scrollIntoView')
    expect(registry.list().map((item) => item.id)).toEqual([
      'a:dashboard:chart:traffic',
    ])
    expect(registry.highlight('a:dashboard:chart:traffic')).toBe(true)
    expect(scrollSpy).toHaveBeenCalledWith({
      block: 'nearest',
      inline: 'nearest',
    })
    expect(registry.idForElement(chart)).toBe('a:dashboard:chart:traffic')
  })

  it('excludes targets hidden by CSS visibility or the hidden attribute', () => {
    const registry = createAiRegistry()
    const parent = element()
    const child = document.createElement('div')
    parent.append(child)

    registry.register(child, target('a:view:card:stats', { kind: 'card' }))

    parent.style.visibility = 'hidden'
    expect(registry.list()).toEqual([])
    expect(registry.highlight('a:view:card:stats')).toBe(false)

    parent.style.visibility = ''
    expect(registry.list()).toHaveLength(1)

    child.hidden = true
    expect(registry.list()).toEqual([])
    expect(registry.highlight('a:view:card:stats')).toBe(false)
  })

  it('drops a stale selection when its element unmounts', () => {
    const registry = createAiRegistry()
    const node = element()
    registry.register(
      node,
      target('a:devices:device:desktop:d1', { segment: 'desktop' }),
    )
    registry.highlight('a:devices:device:desktop:d1')

    registry.unregister(node)

    expect(registry.selection()).toBeUndefined()
    expect(registry.list()).toHaveLength(0)
  })

  it('replaces and unsubscribes a request handler without clearing a newer one', () => {
    const registry = createAiRegistry()
    const first = vi.fn(async () => 'first')
    const second = vi.fn(async () => 'second')
    const removeFirst = registry.onRequest(first)
    const removeSecond = registry.onRequest(second)

    removeFirst()
    expect(registry.hasHandler()).toBe(true)

    removeSecond()
    expect(registry.hasHandler()).toBe(false)
  })
})

describe('request snapshots', () => {
  it('rejects when no handler is installed', async () => {
    const registry = createAiRegistry()
    registry.register(element(), target('a:devices:device:d1'))

    await expect(
      registry.request(target('a:devices:device:d1'), { kind: 'ask' }),
    ).rejects.toBeInstanceOf(AiUnavailableError)
  })

  it('hands the handler a snapshot with a unique id and the caller prompt', async () => {
    const registry = createAiRegistry()
    const node = element()
    registry.register(node, target('a:devices:device:d1', { label: 'd1' }))
    const seen: AiRequest[] = []
    registry.onRequest((request) => {
      seen.push(request)
      return 'answer'
    })

    const first = await registry.request(target('a:devices:device:d1'), {
      kind: 'ask',
      prompt: 'Why offline?',
    })
    const second = await registry.request(target('a:devices:device:d1'), {
      kind: 'summary',
    })

    expect(first).toBe('answer')
    expect(second).toBe('answer')
    expect(seen).toHaveLength(2)
    expect(seen[0]).toMatchObject({
      kind: 'ask',
      targetId: 'a:devices:device:d1',
      label: 'd1',
      context: { entity: 'a:devices:device:d1' },
      prompt: 'Why offline?',
    })
    expect(seen[1]?.prompt).toBeUndefined()
    expect(seen[0]?.requestId).not.toBe(seen[1]?.requestId)
  })

  it('discards an answer when the registration unmounts before it resolves', async () => {
    const registry = createAiRegistry()
    const node = element()
    registry.register(
      node,
      target('a:devices:device:desktop:d1', { segment: 'desktop' }),
    )
    let release: ((value: string) => void) | undefined
    registry.onRequest(
      () =>
        new Promise<string>((resolve) => {
          release = resolve
        }),
    )

    const pending = registry.request(
      target('a:devices:device:desktop:d1', { segment: 'desktop' }),
      {
        kind: 'ask',
      },
    )
    registry.unregister(node)
    release?.('late answer')

    await expect(pending).rejects.toBeInstanceOf(AiStaleError)
  })

  it('discards an answer when the target becomes CSS-hidden before resolution', async () => {
    const registry = createAiRegistry()
    const parent = element()
    const child = document.createElement('div')
    parent.append(child)
    registry.register(
      child,
      target('a:dashboard:chart:traffic', { kind: 'chart' }),
    )

    let release: ((value: string) => void) | undefined
    registry.onRequest(
      () =>
        new Promise<string>((resolve) => {
          release = resolve
        }),
    )

    const pending = registry.request(
      target('a:dashboard:chart:traffic', { kind: 'chart' }),
      { kind: 'ask' },
    )
    parent.style.display = 'none'
    release?.('late answer')

    await expect(pending).rejects.toBeInstanceOf(AiStaleError)
  })
})

describe('window API', () => {
  it('installs and removes the document contract', () => {
    const registry = createAiRegistry()
    const fake = {} as Window
    const remove = installAiWindow(registry, fake)
    registry.register(element(), target('a:devices:device:d1'))

    const api = (fake as { flowseerAi?: { listTargets: () => AiTarget[] } })
      .flowseerAi
    expect(api?.listTargets().map((item) => item.id)).toEqual([
      'a:devices:device:d1',
    ])

    remove()
    expect((fake as { flowseerAi?: unknown }).flowseerAi).toBeUndefined()
  })
})
