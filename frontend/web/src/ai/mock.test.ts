import { afterEach, describe, expect, it, vi } from 'vitest'
import { createMockAiHandler } from './mock'
import { AiUnavailableError } from './registry'
import type { AiRequest } from './types'

const dashboard: AiRequest = {
  requestId: 'r1',
  kind: 'summary',
  targetId: 'a:dashboard:view:all',
  label: 'Dashboard · all sites',
  context: {
    scope: 'All sites',
    devices: '16',
    health: '1 offline, 1 degraded, 14 healthy',
    attention: 'cologne-ap-02 (offline), hamburg-ap-01 (degraded)',
    peak: '2642 Mbps at 13:00',
  },
}

afterEach(() => {
  vi.useRealTimers()
})

describe('createMockAiHandler', () => {
  it('answers a summary from the target context after the pending delay', async () => {
    vi.useFakeTimers()
    let answer: string | undefined
    void Promise.resolve(createMockAiHandler(500)(dashboard)).then((value) => {
      answer = value
    })

    await vi.advanceTimersByTimeAsync(499)
    expect(answer).toBeUndefined()
    await vi.advanceTimersByTimeAsync(1)
    expect(answer).toBe(
      'All sites: 16 devices, 1 offline, 1 degraded, 14 healthy. ' +
        'Needs attention: cologne-ap-02 (offline), hamburg-ap-01 (degraded). ' +
        'Traffic peaked at 2642 Mbps at 13:00.',
    )
  })

  it('leaves Ask unavailable', async () => {
    await expect(
      createMockAiHandler(0)({ ...dashboard, kind: 'ask', prompt: 'Why?' }),
    ).rejects.toBeInstanceOf(AiUnavailableError)
  })
})
