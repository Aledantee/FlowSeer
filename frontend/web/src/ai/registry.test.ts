// @vitest-environment happy-dom
import { afterEach, describe, expect, it, vi } from 'vitest'
import { AiStaleError, AiUnavailableError, createAiRegistry } from './registry'
import type { AiRegistry, AiViewport } from './registry'
import { installAiWindow } from './window'
import type { AiAnswer, AiHandler, AiSummary, AiTarget } from './types'

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

const sampleSummary: AiSummary = {
  type: 'summary',
  headline: 'All devices healthy',
  tone: 'ok',
  findings: [],
  metrics: [],
  next: [],
  sources: [],
}

const sampleAnswer: AiAnswer = {
  type: 'answer',
  text: 'Traffic is nominal.',
  refs: [],
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

  it('excludes targets inside a CSS-hidden ancestor across all target kinds', () => {
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
    expect(registry.highlight('a:dashboard:chart:traffic')).toBe(true)
    expect(registry.selection()?.target.id).toBe('a:dashboard:chart:traffic')

    card.style.display = 'none'

    expect(registry.selection()).toBeUndefined()
    expect(registry.list()).toEqual([])
    expect(registry.view('a:dashboard:chart:traffic')).toBeUndefined()
    expect(registry.idForElement(chart)).toBeUndefined()
    expect(registry.highlight('a:dashboard:chart:traffic')).toBe(false)

    expect(() =>
      registry.request(chartTarget, { action: 'ask', bound: true }),
    ).toThrow(AiStaleError)

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
    const first: AiHandler = async () => sampleAnswer
    const second: AiHandler = async () => sampleSummary
    const removeFirst = registry.onRequest(first)
    const removeSecond = registry.onRequest(second)

    removeFirst()
    expect(registry.hasHandler()).toBe(true)

    removeSecond()
    expect(registry.hasHandler()).toBe(false)
  })
})

function svgElement(): SVGElement {
  const node = document.createElementNS('http://www.w3.org/2000/svg', 'svg')
  document.body.append(node)
  return node
}

describe('selection attribute', () => {
  it('marks exactly the highlighted manual registration and clears it', () => {
    const registry = createAiRegistry()
    const first = element()
    const second = element()
    registry.register(first, target('a:devices:device:d1'))
    registry.register(second, target('a:devices:device:d2'))

    registry.highlight('a:devices:device:d1')
    expect(first.hasAttribute('data-ai-selected')).toBe(true)
    expect(second.hasAttribute('data-ai-selected')).toBe(false)

    registry.highlight('a:devices:device:d2')
    expect(first.hasAttribute('data-ai-selected')).toBe(false)
    expect(second.hasAttribute('data-ai-selected')).toBe(true)

    registry.clearHighlight()
    expect(second.hasAttribute('data-ai-selected')).toBe(false)
  })

  it('removes the attribute when the selected element unregisters', () => {
    const registry = createAiRegistry()
    const node = element()
    registry.register(node, target('a:devices:device:d1'))
    registry.highlight('a:devices:device:d1')

    registry.unregister(node)

    expect(node.hasAttribute('data-ai-selected')).toBe(false)
  })

  it('removes the attribute when the element moves to another id', () => {
    const registry = createAiRegistry()
    const node = element()
    registry.register(node, target('a:devices:device:d1'))
    registry.highlight('a:devices:device:d1')

    registry.register(node, target('a:devices:device:d2'))

    expect(registry.selection()).toBeUndefined()
    expect(node.hasAttribute('data-ai-selected')).toBe(false)
  })

  it('removes the attribute after a failed highlight', () => {
    const registry = createAiRegistry()
    const node = element()
    registry.register(node, target('a:devices:device:d1'))
    registry.highlight('a:devices:device:d1')

    expect(registry.highlight('missing')).toBe(false)

    expect(node.hasAttribute('data-ai-selected')).toBe(false)
  })

  it('removes the attribute once a viewport change hides the segment and refresh runs', () => {
    const { registry, set } = viewport({ wide: true, narrow: false })
    const node = element()
    registry.register(
      node,
      target('a:devices:device:desktop:d1', { segment: 'desktop' }),
    )
    registry.highlight('a:devices:device:desktop:d1')
    expect(node.hasAttribute('data-ai-selected')).toBe(true)

    set({ wide: false, narrow: true })
    registry.refresh()

    expect(node.hasAttribute('data-ai-selected')).toBe(false)
  })

  it('keeps the selection and stays quiet when an equal target object registers', () => {
    const registry = createAiRegistry()
    const node = element()
    registry.register(node, target('a:devices:device:d1'))
    registry.highlight('a:devices:device:d1')
    const listener = vi.fn()
    registry.subscribe(listener)

    registry.register(node, target('a:devices:device:d1'))

    expect(listener).not.toHaveBeenCalled()
    expect(node.hasAttribute('data-ai-selected')).toBe(true)
    expect(registry.selection()?.element).toBe(node)
  })
})

describe('stored target metadata', () => {
  it('notices an in-place edit of a registered target object', () => {
    const registry = createAiRegistry()
    const node = element()
    const edited = target('a:devices:device:d1', { context: { health: 'Up' } })
    registry.register(node, edited)
    const listener = vi.fn()
    registry.subscribe(listener)

    edited.label = 'renamed'
    edited.context.health = 'Down'
    registry.register(node, edited)

    expect(listener).toHaveBeenCalledOnce()
    expect(registry.list()[0]?.label).toBe('renamed')
    expect(registry.list()[0]?.context).toEqual({ health: 'Down' })
  })

  it('does not follow a later edit until the target registers again', () => {
    const registry = createAiRegistry()
    const edited = target('a:devices:device:d1', { context: { health: 'Up' } })
    registry.register(element(), edited)

    edited.context.health = 'Down'

    expect(registry.list()[0]?.context).toEqual({ health: 'Up' })
  })
})

describe('SVG targets', () => {
  it('lists, resolves, highlights, and unregisters an SVG element', () => {
    const registry = createAiRegistry()
    const chart = svgElement()
    registry.register(chart, target('a:devices:chart:t1', { kind: 'chart' }))

    expect(registry.list().map((item) => item.id)).toEqual([
      'a:devices:chart:t1',
    ])
    expect(registry.idForElement(chart)).toBe('a:devices:chart:t1')
    expect(registry.highlight('a:devices:chart:t1')).toBe(true)
    expect(chart.hasAttribute('data-ai-selected')).toBe(true)
    expect(registry.view('a:devices:chart:t1')?.element).toBe(chart)

    registry.unregister(chart)
    expect(registry.list()).toHaveLength(0)
    expect(chart.hasAttribute('data-ai-selected')).toBe(false)
  })

  it('hides an SVG target inside a hidden HTML ancestor', () => {
    const registry = createAiRegistry()
    const wrapper = element()
    const chart = svgElement()
    wrapper.append(chart)
    registry.register(chart, target('a:devices:chart:t1', { kind: 'chart' }))

    wrapper.hidden = true

    expect(registry.list()).toHaveLength(0)
  })
})

describe('request snapshots and AiRun', () => {
  it('rejects synchronously when no handler is installed', () => {
    const registry = createAiRegistry()
    registry.register(element(), target('a:devices:device:d1'))

    expect(() =>
      registry.request(target('a:devices:device:d1'), { action: 'ask' }),
    ).toThrow(AiUnavailableError)
  })

  it('normalizes a promise into a one-snapshot iterable and preserves snapshot order for iterables', async () => {
    const registry = createAiRegistry()
    const node = element()
    const t = target('a:devices:device:d1')
    registry.register(node, t)

    // Promise normalization
    registry.onRequest(async () => sampleAnswer)
    const run1 = registry.request(t, { action: 'ask' })
    const results1: AiAnswer[] = []
    for await (const s of run1.snapshots) {
      results1.push(s as AiAnswer)
    }
    expect(results1).toEqual([sampleAnswer])

    // Iterable snapshot order
    const snap1: AiSummary = { ...sampleSummary, headline: 'Snapshot 1' }
    const snap2: AiSummary = { ...sampleSummary, headline: 'Snapshot 2' }
    registry.onRequest(async function* () {
      yield snap1
      yield snap2
    })
    const run2 = registry.request(t, { action: 'summary' })
    const results2: AiSummary[] = []
    for await (const s of run2.snapshots) {
      results2.push(s as AiSummary)
    }
    expect(results2).toEqual([snap1, snap2])
  })

  it('ends the run with an error when an invalid snapshot is returned', async () => {
    const registry = createAiRegistry()
    const node = element()
    const t = target('a:devices:device:d1')
    registry.register(node, t)

    registry.onRequest(async () =>
      JSON.parse('{"type":"summary","headline":123}'),
    )
    const run = registry.request(t, { action: 'summary' })

    await expect(async () => {
      for await (const snapshot of run.snapshots) {
        void snapshot
      }
    }).rejects.toThrow('The AI returned a result FlowSeer cannot show.')
  })

  it('aborts the signal when stop is called', async () => {
    const registry = createAiRegistry()
    const node = element()
    const t = target('a:devices:device:d1')
    registry.register(node, t)

    let capturedSignal: AbortSignal | undefined
    registry.onRequest((request) => {
      capturedSignal = request.signal
      return new Promise<AiAnswer>(() => {})
    })

    const run = registry.request(t, { action: 'ask' })
    expect(capturedSignal?.aborted).toBe(false)
    run.stop()
    expect(capturedSignal?.aborted).toBe(true)
  })

  it('keeps a bound run alive across same-element context updates', async () => {
    const registry = createAiRegistry()
    const node = element()
    const t = target('a:devices:device:d1', { context: { peak: '100' } })
    registry.register(node, t)

    let resolveHandler: ((res: AiSummary) => void) | undefined
    registry.onRequest(
      () =>
        new Promise<AiSummary>((resolve) => {
          resolveHandler = resolve
        }),
    )

    const run = registry.request(t, { action: 'summary', bound: true })

    // Same element, updated context
    registry.register(node, { ...t, context: { peak: '200' } })

    resolveHandler?.(sampleSummary)

    const snapshots: AiSummary[] = []
    for await (const s of run.snapshots) {
      snapshots.push(s as AiSummary)
    }
    expect(snapshots).toEqual([sampleSummary])
  })

  it('ends a bound run as stale when its element unregisters', async () => {
    const registry = createAiRegistry()
    const node = element()
    const t = target('a:devices:device:d1')
    registry.register(node, t)

    let resolveHandler: ((res: AiSummary) => void) | undefined
    registry.onRequest(
      () =>
        new Promise<AiSummary>((resolve) => {
          resolveHandler = resolve
        }),
    )

    const run = registry.request(t, { action: 'summary', bound: true })
    registry.unregister(node)
    resolveHandler?.(sampleSummary)

    await expect(async () => {
      for await (const snapshot of run.snapshots) {
        void snapshot
      }
    }).rejects.toBeInstanceOf(AiStaleError)
  })

  it('ends a bound run on an SVG target as stale when another element takes its id', async () => {
    const registry = createAiRegistry()
    const chart = svgElement()
    const t = target('a:devices:chart:t1', { kind: 'chart' })
    registry.register(chart, t)

    let resolveHandler: ((res: AiSummary) => void) | undefined
    registry.onRequest(
      () =>
        new Promise<AiSummary>((resolve) => {
          resolveHandler = resolve
        }),
    )

    const run = registry.request(t, { action: 'summary', bound: true })
    registry.unregister(chart)
    registry.register(svgElement(), t)
    resolveHandler?.(sampleSummary)

    await expect(async () => {
      for await (const snapshot of run.snapshots) {
        void snapshot
      }
    }).rejects.toBeInstanceOf(AiStaleError)
  })

  it('allows an unbound run to survive unregistering', async () => {
    const registry = createAiRegistry()
    const node = element()
    const t = target('a:devices:device:d1')
    registry.register(node, t)

    let resolveHandler: ((res: AiAnswer) => void) | undefined
    registry.onRequest(
      () =>
        new Promise<AiAnswer>((resolve) => {
          resolveHandler = resolve
        }),
    )

    const run = registry.request(t, { action: 'ask', bound: false })
    registry.unregister(node)
    resolveHandler?.(sampleAnswer)

    const snapshots: AiAnswer[] = []
    for await (const s of run.snapshots) {
      snapshots.push(s as AiAnswer)
    }
    expect(snapshots).toEqual([sampleAnswer])
  })

  it('omits signal from AiRun.request and freezes the request at start', () => {
    const registry = createAiRegistry()
    const node = element()
    const t = target('a:devices:device:d1')
    registry.register(node, t)

    registry.onRequest(async () => sampleAnswer)
    const run = registry.request(t, { action: 'ask', prompt: 'test prompt' })

    expect('signal' in run.request).toBe(false)
    expect(Object.isFrozen(run.request)).toBe(true)
    expect(Object.isFrozen(run.request.targets)).toBe(true)
    expect(Object.isFrozen(run.request.targets[0])).toBe(true)
    expect(Object.isFrozen(run.request.targets[0]?.context)).toBe(true)
  })

  it('delivers feedback to onFeedback listeners and unsubscribes cleanly', () => {
    const registry = createAiRegistry()
    const listener = vi.fn()
    const unsubscribe = registry.onFeedback(listener)

    registry.feedback('req-1', 'up')
    expect(listener).toHaveBeenCalledTimes(1)
    expect(listener).toHaveBeenCalledWith({ requestId: 'req-1', rating: 'up' })

    unsubscribe()
    registry.feedback('req-2', 'down')
    expect(listener).toHaveBeenCalledTimes(1)
  })
})

describe('window API', () => {
  it('installs and removes the document contract including onFeedback', () => {
    const registry = createAiRegistry()
    const fake = {} as Window
    const remove = installAiWindow(registry, fake)
    registry.register(element(), target('a:devices:device:d1'))

    const api = fake.flowseerAi
    expect(api).toBeDefined()
    expect(api?.listTargets().map((item) => item.id)).toEqual([
      'a:devices:device:d1',
    ])

    const listener = vi.fn()
    const unsub = api?.onFeedback(listener)
    registry.feedback('req-10', 'up')
    expect(listener).toHaveBeenCalledWith({ requestId: 'req-10', rating: 'up' })
    unsub?.()

    remove()
    expect(fake.flowseerAi).toBeUndefined()
  })
})
