# Agent storage

The `flowseer.store.edge.v1` package holds the device access agent's own
deployment configuration: one operator-written prototext file the agent reads
at start. Nothing outside the agent reads it. It lives under `spec/proto`
because every message FlowSeer parses needs a schema someone can read in five
years, and under the `store` root because it is storage rather than a
boundary — no triad, no ref pair, and imported by nothing.

## Boundaries

Imports: nothing FlowSeer-owned

Imported by: nothing

Deliberately absent:

- Central URL, trust anchors, and setup key. Those belong in
  `flowseer.model.edge.v1.EdgeProvisioning`.
- Telemetry endpoints and queue bounds. Telemetry routes through the local leaf
  node and bounds default in code.

It is a separate package from `flowseer.store.device.v1` because it is a
separate process's file. The two deployments share no state and no lifetime:
central's configuration names listeners, a registry and credential mounts;
this one names a state directory and a provisioning file.

## What it does not carry, and why

**Where central is, how to trust it, and the key that joins.** All three are
in `flowseer.model.edge.v1.EdgeProvisioning`, the file an operator receives once
at issue time and writes into the device before it ships. `AgentConfig` names
its path. Restating those fields here would give the same three facts two
homes with nothing keeping them equal, and the failure mode is silent: an
agent that dials the configured URL while trusting the provisioned anchors
refuses every certificate central presents, and the file that is wrong is the
one nobody is looking at.

**A telemetry endpoint.** The agent exports to the loopback OTLP receiver it
binds for itself, which forwards over its leaf node to central. That address
is fresh on every start, so a configured endpoint is not a setting an operator
has not needed yet — it is a second answer to a question the deployment has
already answered, and the wrong one whenever the two differ.

**The lane's bounds.** Queue capacity, recovery poll interval and operation
timeout come from `src/modules/localnet/access`'s defaults. An operator
looking for one of them is looking for a knob that was not forgotten: it is
one field away when a deployment needs it, and until then it is one fewer
thing to keep correct.

## A working file

```prototext
state_dir: "/var/lib/flowseer/agent"
provisioning_path: "/etc/flowseer/provisioning.textproto"
```

Everything else defaults: thirty-second heartbeats, a dispatch backoff from
one second to thirty, the bus module's buffer bounds, and INFO logging.

## Two files, two protections

`state_dir` holds this edge's private key, which is the whole of what this
edge is — central holds only the public half, and an edge whose key is read
by someone else has to be retired and enrolled again. It is the thing to
protect for the life of the deployment.

`provisioning_path` holds a setup key, which is a secret with a much shorter
life: it is single-use and consumed at the first successful enrollment. Before
that, someone who reads it can enrol as this edge once, which the real edge's
own failed enrollment then makes visible. After it, the file is of no use to
anyone. Both facts are in the schema comments, because the operator choosing
the file's mode is the one reading them.
