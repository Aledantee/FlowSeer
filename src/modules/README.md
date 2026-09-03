# modules

Reusable modules: units of behavior a host assembles rather than writes. A
device poller, an inventory reconciler, a syslog ingest pipeline, an auth
boundary — each configured, started, stopped, and observed the same way, so
building a host becomes a matter of choosing modules and wiring their
configuration.

A host is either a control-plane service (`src/services/`) or an edge
application (`src/edge/`), and a module may be assembled into both. That is the
point of the tier: the same syslog pipeline can run centrally in a service and
inside an agent at the edge, written once here.

**This directory is empty on purpose.** Nothing has been admitted yet, and
nothing should be until the contract below is written and a second service has
shown which seams are real.

## Why it is empty

A module library designed before the second service encodes guesses about what
varies. The seams only become visible when service #2 duplicates the wiring of
service #1 — at that point you are copying a fact, not predicting one.

There is a second failure mode this directory is one careless commit away from:
becoming the new `src/common/`. `src/common/` had no admission rule, so
everything shared drifted into it until it was 90% protocol libraries under a
name that said nothing. A directory named "modules" attracts the same drift even
harder, because almost any package can be described as a module.

## Admission

Write host #1 with everything in its own `internal/` — under
`src/services/<service>/` or `src/edge/<app>/`. When a second host needs the same
thing, write down the module contract first — one interface, one lifecycle,
covering at minimum:

- construction from a typed config value, with no ambient globals,
- explicit declaration of what the module needs from its host,
- start and stop with context, and defined behavior on a failed start,
- health and metrics surfaced through the host, not registered globally.

Then a package is admitted here only if it conforms. Anything that does not
stays in the owning host's `internal/`, where it costs nothing to move later.

Known first candidate: `src/common/snmpmap`, which already sits at the seam
between the SNMP library and the protobuf domain model.
