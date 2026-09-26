# Endpoint Entity

`flowseer.model.endpoint.v1` holds the Endpoint entity family: its UUID-keyed ref
pair, observed host state, lifecycle transitions, wireless roaming events, and
connection failures.

An endpoint represents any host seen through the network (wired, wireless, or
discovered via ARP/FDB walks). Endpoints are machine-observed and nobody
configures them directly; the family defines `EndpointState` and `EndpointEvent`
with deliberately absent `EndpointConfig`.

## Boundaries

Imports: net/addr, net/endpoint

Imported by: nothing

Deliberately absent:

- Admission to `EntityType`. `Endpoint` stays outside `EntityType` until its
  store and service land.
- `EndpointConfig`: endpoints are machine-observed hosts discovered through the
  network that nobody configures directly.
- Identity classification algorithms (a service concern).
- Randomized-MAC merge policies (a service concern).

## Contents

- `endpoint.proto` — `EndpointLocalRef`, `EndpointGlobalRef`, `EndpointLifecycle`,
  `EndpointState`, `EndpointLifecycleTransition`, `EndpointRoam`,
  `EndpointConnectionFailure`, and `EndpointEvent`.
