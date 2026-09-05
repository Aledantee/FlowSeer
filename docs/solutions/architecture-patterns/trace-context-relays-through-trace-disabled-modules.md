---
title: Trace Context Relays Through Trace-Disabled Modules
date: 2026-09-05
category: architecture-patterns
module: src/common/service
problem_type: architecture_pattern
component: service_layer
severity: high
applies_when:
  - "adding or changing a telemetry helper in src/common/service that starts a span or extracts trace context"
  - "deciding whether a module with traces disabled may drop the traceparent or tracestate of an inbound envelope"
  - "choosing between a parent-child span and a span link at a durable delivery or event fan-out boundary"
  - "reading process-global OpenTelemetry providers or propagators instead of the module telemetryView"
  - "debugging a trace that breaks at a hop where only one module has traces enabled"
related_components:
  - observability
  - messaging
  - testing_framework
tags: [opentelemetry, trace-propagation, w3c-trace-context, span-links, telemetry-policy, telemetry-view, message-bus, durable-delivery]
---

# Trace Context Relays Through Trace-Disabled Modules

## Context

The service runtime gained per-module signal policy in `a2d78b2e` (feat(service): scope telemetry by module) and the relay behavior in `9c508c50` (feat(service): preserve trace context across disabled hops). Each `Module` declares `Telemetry TelemetryPolicy` with a tri-state `TelemetryDeclaration` per signal (`src/common/service/telemetry_config.go:37-58`): inherit, enabled, or disabled. `resolveTelemetryPolicies` walks the module tree, starting each signal from the parent's effective value, overriding it with the module declaration, then with the strict `true`/`false` environment override `<PREFIX>_<MODULE>_TELEMETRY_{LOGS,METRICS,TRACES}_ENABLED`, and rejects an explicit enable when no backing exists (`telemetry_config.go:609-630`). A supervisor snapshots the resolved policy into a `telemetryView` once per child generation (`src/common/service/supervisor.go:125`), and the view is what lifecycle recording, message publication, and delivery all receive (`supervisor.go:450-468`, `supervisor.go:607-640`).

The obvious way to "disable traces" for a module is to stop touching trace context in that module. That silently breaks every chain in which an enabled module publishes through a disabled one to another enabled module: the downstream module would extract nothing from the envelope and start a fresh root trace, so an operator following a request through a relay would see two unrelated traces and no way to join them. The plan's Alternatives Considered (`docs/plans/2026-09-04-1504-feat-service-managed-opentelemetry-plan.md:354-356`) rejects three approaches that bear on this pattern:

- Drop trace context when spans are disabled. Breaks the enabled-to-disabled-to-enabled relay that operators were promised.
- Filter disabled modules in the Collector. Disabled instrumentation would still allocate, queue, and fail locally, and the runtime contract would depend on external configuration.
- One provider pipeline per module. Multiplies exporters and shutdown work; a shared provider with a module-scoped context policy is enough.

The settled decision is that disablement is resolved at the module context boundary, and that propagation is independent of span creation (plan lines 53, 60, and the Key Technical Decisions at lines 191-194).

## Guidance

The pattern has five parts. All code cited is at the current tree.

**1. Extract before applying policy.** `deliveryTrace` always builds a carrier from the envelope's `traceparent` and `tracestate`, clears whatever span the local context carried, and extracts. Only after extraction does it consult the module's trace policy (`src/common/service/delivery.go:469-500`):

```go
func (r *messageRuntime) deliveryTrace(ctx context.Context, envelope *servicev1.Message, delivered uint64, telemetry telemetryView) (context.Context, trace.Span) {
	carrier := caseInsensitiveHeaderCarrier(nats.Header{})
	if envelope.HasTraceparent() {
		carrier.Set("traceparent", envelope.GetTraceparent())
	}
	if envelope.HasTracestate() {
		carrier.Set("tracestate", envelope.GetTracestate())
	}
	ctx = trace.ContextWithSpanContext(ctx, trace.SpanContext{})
	propagator := telemetry.propagator
	if propagator == nil {
		propagator = propagation.TraceContext{}
	}
	extracted := propagator.Extract(ctx, carrier)
	spanContext := trace.SpanContextFromContext(extracted)
	if !telemetry.policy.traces {
		return extracted, trace.SpanFromContext(extracted)
	}
	// ... start a consumer span (below)
}
```

The `ContextWithSpanContext(ctx, trace.SpanContext{})` line at `delivery.go:477` is deliberate: the delivery loop runs inside the module's attempt span, and that local span must not survive into the handler context, where it would otherwise be injected into every outbound message (see part 5).

**2. Disabled means "keep the non-recording context", never "start a span" and never "strip the carrier".** With traces off, `deliveryTrace` returns the extracted context and `trace.SpanFromContext(extracted)`, which is a non-recording span wrapping the remote span context (`delivery.go:484-486`). On the publish side, `startPublicationTrace` does the mirror image (`src/common/service/message.go:217-236`):

```go
func (b *MessageBus) startPublicationTrace(ctx context.Context, kind servicev1.MessageKind) (context.Context, trace.Span) {
	if !b.telemetry.policy.traces {
		ctx = contextWithoutRecordingSpan(ctx)
		return ctx, trace.SpanFromContext(ctx)
	}
	// ... start a producer span
}
```

`contextWithoutRecordingSpan` keeps only the `SpanContext` (`src/common/service/context.go:64-68`), so whatever was extracted on delivery is exactly what `envelope` later injects (`message.go:291-296`, written to the envelope at `message.go:308-313`). The relayed envelope therefore carries the same `traceparent` and `tracestate` as the inbound one. `telemetryView.context` applies the same stripping for lifecycle records (`src/common/service/telemetry_policy.go:117-122`). Redeliveries go through the same `deliveryTrace` call with a higher attempt count (`delivery.go:261`), so a retry in a disabled module also relays unchanged.

**3. A durable hop is a link, not a parent.** With traces on, `deliveryTrace` starts a `CONSUMER` span whose parent is the cleared local context and which links to the extracted context when it is valid (`delivery.go:496-499`):

```go
	if spanContext.IsValid() {
		options = append(options, trace.WithLinks(trace.Link{SpanContext: spanContext}))
	}
	return telemetry.tracer.Start(ctx, deliverySpanName, options...)
```

Delivery cannot be a child of publication. The message is persisted in JetStream and may be delivered seconds, minutes, or a restart later, may be redelivered under at-least-once semantics, and an event fans out to every admitted subscriber. Parenting each of those under the publisher span would stretch the publisher's apparent duration to cover work it never waited for, and would put N independent consumer operations under one parent as if they were one request. Links keep the causal join for the operator while letting each delivery be its own trace with honest timing. This is the same rule `docs/conventions/observability.md:240-245` states for asynchronous work after a durable boundary. On the publish side the distinction is by message kind (`message.go:229-235`): an event publication links to the current span and detaches from it because fan-out makes any one delivery a poor child; a command or reply publication parents under the caller because it is one addressed operation within the handler's work.

**4. Helpers take a `telemetryView`, never a global.** `telemetryView` is an immutable per-generation snapshot of `policy`, logger, tracer, meter, providers, propagator, and the shared instruments (`telemetry_policy.go:16-26`); `telemetryOwner.view` swaps in no-op tracer and meter when the policy turns a signal off, but always keeps the propagator (`telemetry_policy.go:58-92`). `deliveryTrace`, `runDeliveryWithTelemetry` (`delivery.go:54`), `capabilityWithTelemetry` (`message.go:104`), and `recordMessage` (`src/common/service/telemetry.go:314-330`) all read the view they were handed, so a `MessageBus` built for module A can never emit under module B's policy. `recordMessage` shows the per-signal split: the counter increments only when `v.policy.metrics`, while the debug record always goes to `v.logger`, which is the local logger when OTLP log export is off. The unscoped `capability` helper still exists for tests and resolves against `availableSignals` (`message.go:97-102`); production wiring goes through the supervisor's slot view (`supervisor.go:611`, `supervisor.go:636`).

**5. An invalid carrier yields an invalid span context, not the local one.** Because step 1 cleared the local span before extraction, a malformed `traceparent` leaves `trace.SpanContextFromContext(extracted)` invalid. The enabled path then starts a span with no link; the disabled path returns a context with no span at all. Either way the module's attempt span cannot leak through a bad envelope into the outbound carrier. `TestTraceDisabledRelayPreservesUnsampledContextAndIgnoresInvalidCarrier` asserts exactly this at `src/common/service/delivery_test.go:1096-1103`.

## Why This Matters

- Continuity for operators. A trace that enters a disabled module still reaches the enabled consumer as a link back to the original publication, so "turn traces off for the noisy relay" does not cost the ability to follow a request end to end.
- Honest disablement. A disabled module creates no span, no link event, and no exporter work. The view hands it a no-op tracer, so there is nothing to allocate or queue, which the Collector-side filtering alternative could not promise.
- Sampling decisions are honored. The relay copies the extracted flags and `tracestate` verbatim, so an unsampled upstream trace stays unsampled through a disabled hop instead of being resampled as a new root by the next enabled module (`delivery_test.go:1065-1094` covers flags zero plus a vendor `tracestate` entry).
- Privacy. Clearing the local span before extraction means the module attempt span, which is internal runtime detail, never appears in an envelope. The only trace identifiers that cross the bus are the ones that arrived in one.

## When to Apply

- Adding a new publish or deliver path, or a new message kind: call `startPublicationTrace` and `deliveryTrace` (or reproduce their ordering: clear local span, extract, then consult policy) rather than starting spans directly.
- Adding a new signal helper on the runtime: take a `telemetryView` parameter and gate on `v.policy.<signal>`; do not read `telemetry` run fields or process globals.
- Writing module code that forwards a message: pass the handler's `ctx` through to `Bus(ctx)` unchanged; the runtime already carries the relayed span context in it.
- Debugging a broken trace across modules: check that the downstream delivery span has a link whose trace ID matches the publication span, and that `traceparent` is byte-equal across the relay envelope. A missing link with a valid carrier points at a helper that started a span from a stale context.
- Deciding parent versus link anywhere else in FlowSeer: if the work can run later than the caller returns, can run more than once, or can run for several consumers, it is a link.

## Examples

`TestTraceContextRelayAcrossDisabledModule` (`src/common/service/delivery_test.go:983-1052`) runs the pattern for command, reply, and event. It builds two views from one run telemetry, one with traces on and one with traces off (`:993-994`), publishes from an enabled publisher inside an `upstream` span (`:997-1004`), then delivers that envelope through a disabled relay under a local `relay attempt` span. The assertions are the contract: the disabled delivery returns a non-recording span and a context equal to the publication context marked remote (`:1007-1013`); the disabled publication is non-recording and the relayed envelope's `traceparent` and `tracestate` equal the inbound ones (`:1015-1025`); redeliveries 1 and 2 preserve the same context (`:1026-1031`); the enabled downstream delivery records a span in a different trace than the publication (`:1034-1040`) whose single link is the publication context (`:1042-1045`); and exactly four spans ended, upstream, publication, relay attempt, downstream, so the relay contributed none (`:1047-1049`).

`TestTraceContextLinksPublicationToDelivery` (`delivery_test.go:911-981`) is the two-module base case over a real bus: the delivery span carries one link whose trace ID equals the publication span's.

`TestManagedTelemetryDisabledTraceRelay` (`src/common/service/test/integration/otel_test.go:452-586`) proves it through a managed OTLP pipeline and a real Collector: three modules, `relay` declared with `Telemetry: service.TelemetryPolicy{Traces: service.TelemetryDisabled}` (`:487`), publisher emits an event, relay forwards it as a command. The Collector must contain no span with the relay's `flowseer.module.path` (`:563-566`) and the downstream `flowseer.message.deliver` span must link to the publisher's `flowseer.message.publish` trace (`:574-585`).

## Related

- `docs/plans/2026-09-04-1504-feat-service-managed-opentelemetry-plan.md`: Key Decisions "Propagate trace context through trace-disabled modules" and "Resolve signal policy at the module context boundary", the Key Technical Decisions on backing versus emission policy, per-generation views, and propagation independent of span creation, and Alternatives Considered.
- `src/common/service/README.md` "Instrumentation" (`:71-245`): operator-facing tri-state policy, environment override names, and the generation snapshot rule.
- `docs/conventions/observability.md` "Name and connect spans" (`:224-245`): extract before an inbound span, inject before an outbound one, links after a durable boundary.
- `docs/solutions/architecture-patterns/local-bus-durability-is-a-runtime-setting-not-storage-identity.md`: the bus is at-least-once, and redelivery is one of the reasons delivery must link rather than parent.
