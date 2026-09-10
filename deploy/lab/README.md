# Lab deployment files

The four files one lab run needs, with placeholders in every position that
takes a real value. Nothing here is a secret and no address here resolves.

| File | What it is |
| --- | --- |
| `central.textproto` | `DeviceServiceConfig` — the device service's own deployment |
| `registry.textproto` | `DeviceRegistry` — the one switch central serves, and the policy it resolves |
| `agent.textproto` | `AgentConfig` — where the agent's state lives and which provisioning file it reads |
| `provisioning.textproto` | `EdgeProvisioning` — the shape of what central hands you at issue time |

Four rather than three, because `AgentConfig` names a provisioning file
instead of restating where central is, how to trust it, and the key that
joins. That was deliberate: copying those into the agent's own configuration
would make two sources of truth for the same three facts with nothing keeping
them equal.

The provisioning file is the only one that ever holds a credential, and its
placeholders are invalid on purpose — the setup key fails its schema pattern
and the trust anchor is the wrong length — so an agent given the file
unedited refuses it at load rather than starting and failing later somewhere
less obvious. A file that fails loudly when unfilled is worth more than one
that looks filled.

`registry.textproto` fails the same way for the two positions that describe a
device rather than the deployment: the management address and
`ssh_host_key_sha256`. Both ship as placeholders and `write-registry.sh`
refuses to render the shipped file while either is still there. Neither
failure is one a reader would diagnose from what it produces — an unfilled
address makes the agent log a timed-out identity probe, which is what it also
logs when the switch is off, and an unfilled digest is a pin that fails at the
moment a mutation opens its shell.

Read [the runbook](../../docs/runbooks/lab-icx7150-first-write.md) before
using any of this. Two values here cannot be copied from a document because
they are measurements — the delayed-apply horizon and the switch's SSH host
key — and one of them decides more than its name suggests.
