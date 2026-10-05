// The addressable side of the console: a stable target an agent (or the
// built-in action layer) can name, and the request snapshot handed to a
// handler. Nothing here talks to a service; the application has no AI
// backend, so an absent handler is a first-class outcome.

// A CSS-only copy of the same entity is addressable under its own segment.
// The registry hides the copy whose layout the viewport does not show.
export type AiTargetSegment = 'mobile' | 'desktop'

export type AiTone = 'ok' | 'warning' | 'critical' | 'unknown'
export type AiSeverity = 'info' | 'warning' | 'critical'
export type AiConfidence = 'low' | 'medium' | 'high'
export type AiEntityKind = 'device' | 'site' | 'client' | 'link' | 'chart'

export interface AiEntityRef {
  kind: AiEntityKind
  id: string
  label: string
}

export type AiRef = AiEntityRef

export interface AiFinding {
  severity: AiSeverity
  title: string
  detail?: string
  refs: AiEntityRef[]
}

export interface AiCause {
  text: string
  confidence: AiConfidence
  refs: AiEntityRef[]
}

export interface AiImpact {
  text: string
  refs: AiEntityRef[]
}

export interface AiMetric {
  label: string
  value: string
  tone?: AiTone
}

export interface AiNextStep {
  label: string
  ref?: AiEntityRef
}

export interface AiSummary {
  type: 'summary'
  headline: string
  tone: AiTone
  findings: AiFinding[]
  cause?: AiCause
  impact?: AiImpact
  metrics: AiMetric[]
  next: AiNextStep[]
  sources: AiEntityRef[]
}

export interface AiAnswer {
  type: 'answer'
  text: string
  refs: AiEntityRef[]
  summary?: AiSummary
  // The renderer validates this untrusted tree with validateAiUiTree.
  ui?: unknown
}

export type AiResult = AiSummary | AiAnswer

export interface AiUiNode {
  component: string
  props: Record<string, unknown>
  children?: AiUiNode[]
}

export interface AiUiNavigateIntent {
  type: 'navigate'
  target: {
    path?: string
    query?: Record<string, string>
  }
}

export type AiUiIntent = AiUiNavigateIntent

export interface AiTarget {
  id: string
  kind: string
  view?: string
  label: string
  context: Record<string, string>
  entity?: AiEntityRef
  segment?: AiTargetSegment
}

export interface AiTargetSnapshot {
  id: string
  kind: string
  view: string
  label: string
  entity?: AiEntityRef
  context: Record<string, string>
}

export type AiTurn =
  { role: 'user'; prompt: string } | { role: 'assistant'; result: AiResult }

export interface AiSeed {
  targets: AiTargetSnapshot[]
  turns: AiTurn[]
}

export interface AiRequest {
  requestId: string
  action: string
  kind?: AiRequestKind
  targetId?: string
  context?: Record<string, string>
  targets: readonly AiTargetSnapshot[]
  prompt?: string
  history: readonly AiTurn[]
  signal: AbortSignal
}

export interface AiRun {
  requestId: string
  request: Omit<AiRequest, 'signal'>
  snapshots: AsyncIterable<AiResult>
  stop(): void
}

export type AiHandler = (
  request: AiRequest,
) => Promise<AiResult | string> | AsyncIterable<AiResult> | AiResult | string

// The DOM node a target stands for. An SVG root such as a sparkline is as
// valid an anchor as an HTML element.
export type AiTargetElement = HTMLElement | SVGElement

export interface AiTargetView {
  target: AiTarget
  element: AiTargetElement
}

export type AiRequestKind = 'ask' | 'summary'
