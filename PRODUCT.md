# Product

<!-- impeccable:product-schema 1 -->

## Platform

web

## Users

The primary users are network operations (NOC) staff at m3connect, a managed
service provider running Wi-Fi and network infrastructure for hospitality
customers. From a central desk they watch many customer tenants, sub-tenants,
and sites at once, find the device that needs attention, and act on it without
losing track of which tenant and site they are working in.

On-site engineers and support staff are a secondary audience. They use a phone
at one site to check its health, find a failing device, and reach the relevant
quick action. They do not need feature parity with the desk.

## Product Purpose

FlowSeer abstracts network devices and their capabilities behind one typed
model, keeps a central inventory of devices and the integrations that reach
them, and carries a change to a device and back with evidence that it took
effect. Success is an operator who can answer "what is wrong, where, and did my
fix work" across every customer without switching between vendor consoles.

## Positioning

- **Vendor-neutral typed model.** One FlowSeer-owned device model spans vendor
  controllers, cloud tenants, and direct protocols (SNMP, NETCONF, RESTCONF,
  gNMI, SSH, syslog). The operator sees devices and capabilities, not
  protocols or vendor UIs.
- **Verified changes.** A typed change reaches a device through the
  local-network lane, and an observation verifies it took effect. Gating a
  mutation by projecting it onto a full-network view is decided but deferred.
- **Edge agents at sites.** Enrolled edge processes reach local networks,
  run packet captures, and run `netpen` L2/L3 audits on an operator's command.
- **Self-hosted, EU, owned.** m3connect owns the product and its data. Every
  dependency is open source or source-available, fully self-hostable, and
  deployable in the EU.

## Operating Context

- Multi-tenant by structure: tenant, sub-tenant, site, and integration scope
  frame every view. Tenancy is enforced by the backend, never only by the UI.
- Inventory vocabulary is fixed in `CONCEPTS.md`: Device, Integration, Edge,
  Binding, Integration Scope, Placement, Tag, Attribute Definition, Alarm,
  Syslog Record. The UI uses these terms with their defined meanings.
- Reachability (per Binding, heals on its own) and lifecycle (an operator
  action) are separate axes and must not be conflated in status displays.
- Desk use is dense and long-running; phone use is short, one site, one
  device, touch-first.

## Capabilities and Constraints

- The web console (`frontend/web/`, Vue 3 + Vite + TypeScript) runs on local
  fixtures with no backend. Connecting it to the control plane, sign-in, and
  authorization (OpenFGA is named, shape undecided) are open decisions.
- The console makes no requests to external services and bundles its fonts.
- Topology links and traffic are illustrative fixtures today. Large-fleet
  table performance, a topology renderer, and configurable OLAP dashboards
  need their own data contracts and load tests before they are built.
- Frontend work stays out of backend territory (`spec/`, `generated/`,
  `deploy/`, backend services).

## Brand Commitments

- FlowSeer is built for m3connect. The m3connect coral `#FF451D` and cyan
  `#5ECAD8` are binding brand anchors (see `frontend/web/design/README.md`).
- GT Standard, the m3connect website face, is a brand option once licensed
  files are supplied; Inter Variable is the interface font until then.
- Brand colors never carry status meaning. Health uses its own labeled
  states.
- Copy is product-facing: no decorative placeholder text or demo badges.

## Evidence on Hand

- Fixture tenants and sites in `frontend/web/src/domain/` (for example Aurora
  Hospitality, Aurora Germany, Berlin Mitte, Hamburg Hafen). These are demo
  data, not customers.
- Verified lab captures and integration-target dossiers under
  `docs/research/device-inventory/`.
- No customer testimonials, benchmarks, pricing, or release exist. Future
  work must not invent them.

## Product Principles

1. Scope is always visible. The operator never wonders which tenant or site
   an action targets.
2. Show what is true and how it is known. Status carries its source and age,
   and a change is done when it is observed, not when it is sent.
3. One model, many vendors. Surfaces speak FlowSeer's vocabulary, not a
   vendor's.
4. Desk depth, field brevity. The desk gets density; the phone gets the one
   answer and the one action.

## Accessibility & Inclusion

Palette tests enforce WCAG 2.2 contrast: 4.5:1 for normal text, 7:1 for
primary text, and 3:1 for control outlines, focus indicators, and topology
links. Controls use native semantics and visible keyboard focus, and
reduced-motion preferences skip transitions. Full WCAG conformance has not
been evaluated.
