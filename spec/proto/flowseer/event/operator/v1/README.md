# Operator Action Event

`flowseer.event.operator.v1` holds `OperatorActionEvent`, the durable audit
record of what an operator attempted and completed.

## Boundaries

Imports: model/capture, model/edge, model/identity

Imported by: nothing FlowSeer-owned

Deliberately absent:

- `OperatorActionConfig` and `OperatorActionState`. This package is a pure
  event stream; there is nothing here to configure and nothing to query as
  current state.
- A tenant. Tenancy is ambient; the tenant is a token in the message subject.

## Structure

Every event carries the same envelope — the event identifier, the call
identifier joining attempt and completion, when it occurred, the operator, the
action enum, and an optional object (`EdgeGlobalRef` or
`CaptureSessionGlobalRef`) — plus exactly one detail: `OperatorActionAttempted` or
`OperatorActionCompleted`.

`OperatorActionAttempted` carries no fields of its own.

`OperatorActionCompleted` carries the `OperatorActionOutcome` (succeeded,
denied, or failed) and, set if and only if outcome is not succeeded, the
`error_type` classification from telemetry.
