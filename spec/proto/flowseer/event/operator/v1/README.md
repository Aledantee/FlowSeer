# Operator Action Event

`flowseer.event.operator.v1` holds `OperatorActionEvent`, the durable audit
record of what an operator attempted and completed.

## Boundaries

Imports: model/capture, model/edge, model/identity

Imported by: nothing FlowSeer-owned

Deliberately absent:

- `OperatorActionConfig` and `OperatorActionState`. This package is a pure
  event stream. There is nothing here to configure and nothing to query as
  current state.
- A tenant. Tenancy is ambient: the tenant is a token in the message subject.

## Structure

Every event carries the same envelope: the event identifier, the call
identifier joining attempt and completion, when it occurred, the operator, the
action enum, and an optional object. Objects name an edge, capture session,
tenant, member, role, role assignment, partner, or full-payload grant. Role,
partner, and full-payload objects carry the relations or expiry that would be
lost when the record changes. A role object carries one to four relations for
every action except `ROLE_DELETE`, which names the role alone. The event also
carries exactly one detail:
`OperatorActionAttempted` or `OperatorActionCompleted`.

`OperatorActionAttempted` carries no fields of its own.

`OperatorActionCompleted` carries the `OperatorActionOutcome` (succeeded,
denied, or failed) and, set if and only if outcome is not succeeded, the
`error_type` classification from telemetry.

The action enum includes tenant creation, member enrollment and removal, role
creation, deletion, assignment, and unassignment, partner connection and
disconnection, and full-payload grant and revocation. Tenancy remains ambient
in the message subject, so no event field duplicates it.
