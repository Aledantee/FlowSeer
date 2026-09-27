// The addressable side of the console: a stable target an agent (or the
// built-in action layer) can name, and the request snapshot handed to a
// handler. Nothing here talks to a service; the application has no AI
// backend, so an absent handler is a first-class outcome.

// A CSS-only copy of the same entity is addressable under its own segment.
// The registry hides the copy whose layout the viewport does not show.
export type AiTargetSegment = 'mobile' | 'desktop'

export interface AiTarget {
  id: string
  kind: string
  label: string
  context: Record<string, string>
  segment?: AiTargetSegment
}

export type AiRequestKind = 'ask' | 'summary'

export interface AiRequest {
  requestId: string
  kind: AiRequestKind
  targetId: string
  label: string
  context: Record<string, string>
  prompt?: string
}

export type AiHandler = (request: AiRequest) => Promise<string> | string

export interface AiTargetView {
  target: AiTarget
  element: HTMLElement
}
