import { AiUnavailableError } from './registry'
import type { AiHandler, AiRequest } from './types'

// A stand-in provider for the design preview. The console has no model
// backend, so this answers summaries from the target's own context after a
// pause long enough to show the pending state, and leaves Ask unavailable
// rather than inventing answers to free-form questions.

const pendingMs = 1800

function summarize({ label, context }: AiRequest): string {
  if (context.health === undefined)
    return `${label}: ${Object.entries(context)
      .map(([key, value]) => `${key} ${value}`)
      .join(', ')}.`
  const lines = [
    `${context.scope}: ${context.devices} devices, ${context.health}.`,
  ]
  lines.push(
    context.attention
      ? `Needs attention: ${context.attention}.`
      : 'Nothing needs attention.',
  )
  if (context.peak) lines.push(`Traffic peaked at ${context.peak}.`)
  return lines.join(' ')
}

export function createMockAiHandler(delay = pendingMs): AiHandler {
  return (request) => {
    if (request.kind !== 'summary')
      return Promise.reject(new AiUnavailableError())
    return new Promise((resolve) =>
      setTimeout(() => resolve(summarize(request)), delay),
    )
  }
}
