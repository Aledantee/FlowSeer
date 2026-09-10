# services

Backend services — the long-running processes that make up the FlowSeer control
plane. The device service, inventory, discovery, and the ingestion planes
described in `docs/architecture/` land here.

The device service is the first one built. `src/services/device/` holds its
journal, its edge and operator APIs, and the host that assembles them into a
process; its [README](device/README.md) describes what it owes an edge and
what a deployment has to put in front of it.

## Shape

One directory per service, each with its own `cmd/` entrypoint:

```
src/services/<service>/
  cmd/<service>/main.go   process entrypoint: flags, config load, host startup
  internal/               everything the service owns
```

A service is a host: it loads configuration, assembles the modules it needs,
starts them, and shuts them down. It should read as a list of choices, not as a
place where behavior is implemented.

Put everything in `internal/` first, including code you expect to share. It
moves to `src/modules/` when a second service actually needs it and it conforms
to that directory's contract — not before. `internal/` is the cheap place to be
wrong.

## Boundaries

- Protocol access goes through `src/protocol/`. A service never opens a socket
  to a device itself.
- Domain types come from `generated/go/proto`. Services consume the generated
  public API; they do not reach into a generator's internals.
- Edge applications — the ones that *can* run outside the control plane, like
  `netpen` — belong in `src/edge/`, not here. The split names the two roles a
  process is designed for: a service is control-plane infrastructure; an edge
  application is built to run at the edge, even though it may also be deployed
  centrally. A module, by contrast, is neither — it can be assembled into a
  service and into an edge application both.
