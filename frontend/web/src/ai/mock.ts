import type { AiFinding, AiHandler, AiMetric, AiRequest, AiTone } from './types'
import { isPagePath } from '../navigation/page'

// A stand-in provider for the design preview. The console has no model
// backend, so this answers summaries from the target's own context after a
// pause long enough to show the pending state, and answers ask with a
// placeholder answer.

const pendingMs = 1800

type AiUiStatus = 'Healthy' | 'Degraded' | 'Offline'

function uiStatus(value: string | undefined): AiUiStatus | undefined {
  if (value === 'Healthy' || value === 'Degraded' || value === 'Offline') {
    return value
  }
  return undefined
}

export function createMockAiHandler(delay = pendingMs): AiHandler {
  return async function* (request: AiRequest) {
    if (request.action === 'ask') {
      if (delay > 0) {
        await new Promise((resolve) => setTimeout(resolve, delay))
      }
      const target = request.targets[0]
      const entity = target?.entity
      const status = uiStatus(target?.context.health)
      const ui =
        entity?.kind === 'device' && isPagePath(`/devices/${entity.id}`)
          ? [
              {
                component: 'UiCard',
                props: {},
                children: [
                  ...(status
                    ? [
                        {
                          component: 'UiStatusBadge',
                          props: { status },
                        },
                      ]
                    : []),
                  {
                    component: 'UiButton',
                    props: {
                      text: 'Open device',
                      intent: {
                        type: 'navigate',
                        target: { path: `/devices/${entity.id}` },
                      },
                    },
                  },
                ],
              },
            ]
          : undefined
      yield {
        type: 'answer',
        text: 'This is a placeholder answer.',
        refs: entity ? [entity] : [],
        ui,
      }
      return
    }

    const target = request.targets[0]
    const label = target?.label ?? 'Target'
    const context = target?.context ?? {}

    let tone: AiTone = 'ok'
    const health = (context.health ?? '').toLowerCase()
    if (health.includes('offline') || health.includes('critical')) {
      tone = 'critical'
    } else if (health.includes('degraded') || health.includes('warning')) {
      tone = 'warning'
    }

    const headline = context.health
      ? `${label}: ${context.health}`
      : `${label} operational`

    // Snapshot 1: headline
    yield {
      type: 'summary',
      headline,
      tone,
      findings: [],
      metrics: [],
      next: [],
      sources: target?.entity ? [target.entity] : [],
    }

    if (delay > 0) {
      await new Promise((resolve) => setTimeout(resolve, delay))
    }

    // Snapshot 2: the rest
    const findings: AiFinding[] = []
    if (context.attention) {
      findings.push({
        severity: tone === 'critical' ? 'critical' : 'warning',
        title: 'Attention required',
        detail: context.attention,
        refs: target?.entity ? [target.entity] : [],
      })
    }

    const metrics: AiMetric[] = []
    if (context.devices) {
      metrics.push({ label: 'Devices', value: context.devices })
    }
    if (context.peak) {
      metrics.push({ label: 'Peak traffic', value: context.peak })
    }

    yield {
      type: 'summary',
      headline,
      tone,
      findings,
      cause:
        tone !== 'ok'
          ? {
              text: context.attention ?? 'Service disruption',
              confidence: 'medium',
              refs: target?.entity ? [target.entity] : [],
            }
          : undefined,
      impact:
        tone !== 'ok'
          ? {
              text: 'Client traffic may be affected',
              refs: target?.entity ? [target.entity] : [],
            }
          : undefined,
      metrics,
      next: [
        {
          label: 'Investigate target',
          ref: target?.entity,
        },
      ],
      sources: target?.entity ? [target.entity] : [],
    }
  }
}
