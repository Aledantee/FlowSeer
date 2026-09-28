# Domain Model

## Identity

The `model/` root holds everything in FlowSeer that carries or names identity:
entities with their UUID ref pairs, Config/State/Event triads, and lifecycle
enums, the device-access operation vocabulary, and the identity, policy, and
credential leaves.

## Admission

A package belongs in `model/` if its messages define domain identity, entity
lifecycle, or shared operation vocabulary needed across service boundaries.
`model/edge` passes because it defines the Edge entity, its lifecycle, and its
assertion header. `api/edge` fails admission because it defines Connect RPC
services; services live in service roots (`api/`, `edge/`).

## Boundaries

Imports: net/addr, net/capture, net/endpoint, net/interface, net/key, net/measure, net/phy, net/switching, net/system, net/wlan

Imported by: api/capture, api/device, api/edge, edge/attach, edge/capture, edge/dispatch, event/access, event/log, store/device

Packages in `model/` may import `net/` primitives and sibling `model/` leaves.
`model/` packages never import service packages (`api/`), event packages
(`event/`), or stores (`store/`), and never declare a Connect service.

## Packages

- `access/v1/`: Operation vocabulary, phases, intents, and observations shared across device-access boundaries.
- `alarm/v1/`: Alarm entity, perceived severity, resource targeting, state, and clear transitions.
- `capture/v1/`: CaptureSession entity, authorization, lifecycle, and shared packet chunk frames.
- `credential/v1/`: Typed credential material for device authentication (SNMPv3, SSH).
- `edge/v1/`: Edge entity, keys, proof of possession, assertion headers, and provisioning.
- `endpoint/v1/`: Endpoint entity, lifecycle, state, roam, connection failure, and events.
- `identity/v1/`: Operator and tenant identity.
- `inventory/v1/`: Hardware, component, integration, and topology entities and ref pairs.
- `policy/v1/`: Access policy, credential, and host-trust handles.
- `wireless/v1/`: Wireless LAN entity, SSID configuration, broadcast state, and radio associations.
