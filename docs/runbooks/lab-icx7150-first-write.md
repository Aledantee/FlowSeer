# First live write: the lab ICX7150

This is the first time FlowSeer changes a real device. It sets one interface
description on one lab switch and reads it back.

Everything up to the write has been proven against fixtures: an agent enrolls,
attaches, onboards a device from central's registry, and an operator's change
travels down and comes back as an observation. What no fixture proves is the
device — the SSH prompts, the SNMP answers, the time the switch takes to make
a change visible. That is what this run is for, and it is why the assembled
end-to-end came first: it found two defects that would have reached the switch
as an interface changed on real hardware with the system unable to say whether
it had changed.

## Before any of it: the tools, and how commands here are written

Everything an operator does to central is a Connect RPC, and `buf curl` makes
those without a client. Central serves no reflection, so the schema comes from
this repository; it serves a certificate it generated for itself, so that
certificate is what you trust.

There is no credential in any of these calls, and that is worth meeting here
rather than later. `DeviceService` and `EdgeAdminService` are mounted without
the assertion middleware — an operator holds no edge key, so a check that
verified one would refuse every operator call. The convenience of a call that
needs no authentication and the hazard of step 6 are the same fact: anyone who
reaches this port can make these calls, which is why the port does not leave
the host.

**Every command block below is one of three kinds**, and the difference
matters because two of them are run in different places:

- `sh` — run on the host where central runs. These are executed by the
  repository's test suite exactly as printed, so a command here has been run.
- `cli` — typed at the switch's own command line, over SSH. These the suite
  cannot run; they need the switch.
- `text` — output and illustrations, not commands.

Set these once, in the shell you will use throughout:

```sh
export FLOWSEER_REPO=/path/to/this/repository
export CENTRAL=https://127.0.0.1:8443
export CACERT=/var/lib/flowseer/device/tls.crt
export DEVICE_ID=0192e6a0-0000-7000-8000-0000000000d1
export INTERFACE="ethernet 1/1/1"

# The change itself, and who is making it.
export DESCRIPTION="uplink to core"
export OPERATOR=your-identity-provider-subject
export IDEMPOTENCY_KEY=$(uuidgen | tr 'A-Z' 'a-z')

# The access policy the registry pins for this device. These must match the
# registry's entry exactly; an intent naming another version is refused.
export POLICY_KEY=icx7150-lab
export POLICY_VERSION=1

# Where the deployment's files live, and a scratch directory for the run.
export RUN=/var/tmp/flowseer-lab
export CENTRAL_CONFIG=/etc/flowseer/device.textproto
export AGENT_CONFIG=/etc/flowseer/agent.textproto
export REGISTRY=/etc/flowseer/registry.textproto
export PROVISIONING=/etc/flowseer/provisioning.textproto
```

`CACERT` is the certificate central generated into its state directory on
first start. `DEVICE_ID` and `INTERFACE` are the device and port this run
targets, and must match the registry.

A call looks like this, and this one is also the check that your shell is set
up — it asks central what it knows about the device and needs nothing to have
happened first:

```sh
buf curl --schema "$FLOWSEER_REPO/spec/proto" --cacert "$CACERT" \
  --data "{\"device\":{\"device\":{\"id\":\"$DEVICE_ID\"}}}" \
  "$CENTRAL/flowseer.api.device.v1.DeviceService/GetDeviceAccessStatus"
```

## Bringing the deployment up

Do this before step 0's switch is powered on; none of it touches the device.

The order is forced and it is not the obvious one. Central will not start
without a registry, the registry must name the edge that hosts the
integration, and the edge's identifier is minted by central — so the registry
cannot be complete until central has run once. Central also reads its registry
exactly once, at start, so a registry that changes needs a restart.

The way through is to start with a registry that names the integration and
**no devices**, which is valid and honest: an integration with no devices
serves none, and nothing is claimed about an edge that does not exist yet. The
device is added once the edge does.

That the sequence has this shape at all is a rough edge rather than a design.
It would disappear if `CreateEdge` accepted an identifier the operator chose,
or if an integration serving no devices did not have to name an edge.

Build the two binaries:

```sh
mkdir -p "$RUN"
go -C "$FLOWSEER_REPO" build -o "$RUN/device" ./src/services/device/cmd/device
go -C "$FLOWSEER_REPO" build -o "$RUN/agent" ./src/edge/agent/cmd/agent
```

Start central on the first registry, and wait for it to say it is listening
rather than guessing:

```sh
"$RUN/device" --config "$CENTRAL_CONFIG" > "$RUN/central.log" 2>&1 &
echo $! > "$RUN/central.pid"
until grep -q "device api listening" "$RUN/central.log"; do sleep 0.2; done
```

Create the edge. The answer carries its identifier and the provisioning to
ship with it, and the setup key inside is shown exactly once:

```sh
buf curl --schema "$FLOWSEER_REPO/spec/proto" --cacert "$CACERT" \
  --data '{"name":"lab"}' \
  "$CENTRAL/flowseer.api.edge.v1.EdgeAdminService/CreateEdge" > "$RUN/created.json"
export EDGE_ID=$(sed -n 's/.*"id": *"\([^"]*\)".*/\1/p' "$RUN/created.json" | head -1)
test -n "$EDGE_ID"
```

Write the full registry — the same integration, now naming the edge that
exists, and the device — then restart central so it reads it:

```sh
"$FLOWSEER_REPO"/deploy/lab/write-registry.sh "$EDGE_ID" > "$REGISTRY"
kill -TERM "$(cat "$RUN/central.pid")"
wait "$(cat "$RUN/central.pid")"
"$RUN/device" --config "$CENTRAL_CONFIG" >> "$RUN/central.log" 2>&1 &
echo $! > "$RUN/central.pid"
until [ "$(grep -c "device api listening" "$RUN/central.log")" -ge 2 ]; do sleep 0.2; done
```

Appended rather than overwritten, so the first run's shutdown stays readable
next to the second's start — a log truncated by the restart loses the only
record that the stop was orderly.

**That `wait` is an assertion, which is why it has no `|| true`.** A clean stop
drains the modules and exits zero; a process killed outright exits with the
signal's status instead. Under `set -e` a non-zero status stops you here
rather than letting you carry on with a deployment that did not shut down the
way it claims to.

The status is all there is to go on: a clean shutdown writes nothing to the
log. So a stopped service and a killed one look identical in `$RUN/central.log`
and differ only in what `wait` returned.

Write the provisioning the agent reads, from what `CreateEdge` returned, and
start the agent:

```sh
"$FLOWSEER_REPO"/deploy/lab/write-provisioning.sh "$RUN/created.json" "$CENTRAL" > "$PROVISIONING"
"$RUN/agent" --config "$AGENT_CONFIG" > "$RUN/agent.log" 2>&1 &
echo $! > "$RUN/agent.pid"
until grep -q '"flowseer.edge.id"' "$RUN/agent.log"; do sleep 0.2; done
```

That wait is for enrollment, which needs central and not the switch. An agent
that cannot enroll exits rather than retrying — without an identity there is
nothing for it to do — so if this does not return, read `$RUN/agent.log`
rather than waiting.

The agent enrolls, attaches its bus, and starts its lane. It will then try to
onboard every device central lists for it, and that is the first thing here
that needs the switch — an identity probe over SNMP against a device that is
still powered off fails, is retried, and says so in `$RUN/agent.log`:

```text
"msg":"listed device cannot be onboarded" … "error":{"msg":"identity probe: … request timed out"}
```

That is the expected state until step 0's switch is on. Whether the device has
been onboarded is step 7's subject, and it is the gate on everything after it.

## Step 0: ask for the switch to be powered on

**This is a scheduling step and it comes days before the rest.** The lab
ICX7150 is normally powered off, and the person who owns it has to be asked in
advance to power it up and to say when it will be available.

An agent that reaches this runbook and finds the device unreachable **asks
rather than retries**. An unreachable approved device is not a transient
error to hammer: the approval is for a device somebody expected to be on, and
a device that is off is a fact about the world that a retry loop cannot
change. Stop and ask.

## Step 1: confirm SNMPv3 authPriv before the switch is powered on

Check that the ICX7150's SNMPv3 user is configured for **authPriv** with
AES128, AES192 or AES256 — not authNoPriv, not noAuthNoPriv.

This system manages devices at no lower level. `SnmpV3Credential` requires
both a privacy protocol and a privacy passphrase, and neither has a value
meaning "none". A device configured authNoPriv does not fail at the device: it
fails earlier, at credential mapping, with a message that reads as central
having failed to fill in a field. An operator meeting that will go looking at
central and find nothing wrong with it.

**On this switch, in September 2026, it failed.** There was no SNMPv3 user at
all — the running configuration carried a read-only community and nothing
else, and an authPriv read answered `Unknown user name`. FlowSeer has no
community path, so the device could not be read by it in any way.

Check it, at the switch:

```cli
show running-config | include snmp-server
```

**Fixing it is itself a live write, and it is not covered by the approval in
step 9.** Creating an SNMPv3 user changes the switch's configuration; that
approval is for one interface description. If the check fails, stop and get
the second change approved on its own terms before making it. What it takes is
a group and a user:

```cli
configure terminal
snmp-server group flowseer-ro v3 priv read all
snmp-server user <name> flowseer-ro v3 auth sha <passphrase> priv aes <passphrase>
end
```

Running configuration only, so it reverts on reload — the same property the
description change has. Confirm it by making an authPriv read that succeeds,
not by the absence of an error.

## Step 2: measure the delayed-apply horizon, and know what the number buys

**Measure it on the path the system reads, which is SNMP.** Set a description
by hand and then poll `ifAlias` for that interface over SNMPv3 — not `show
interfaces`. The CLI shows a change immediately because the CLI is what made
it; the edge reads `1.3.6.1.2.1.31.1.1.1.18.<ifIndex>`, and on this switch
`ethernet 1/1/N` is ifIndex N. Measuring over the CLI measures the wrong
thing and gives a number smaller than the truth.

**Measure it on a different interface from the one you are going to change**,
or you will overwrite the value step 3 records before you record it.

On this switch the answer was **0.13 seconds**, and that is dominated by the
SNMP round trip, so the true figure is smaller.

**The number decides more than its name says.** `delayed_apply_horizon` is
documented as the longest a mutation may take to become visible, and it also
sizes how long the edge keeps looking before an unconfirmed change becomes a
person's problem, and — through the poll interval the lane derives from it —
how many times it looks. At least six looks, more for a horizon past three
minutes.

So a horizon measured honestly as two seconds is the right answer to the
question the field asks and a poor answer to the question it also decides: six
looks inside two seconds means the first live write gets its whole recovery
budget inside one distracted moment, and a brief blip while it runs ends with
the interface changed and the lane held for a person.

For this run, record the measurement and set the registry's horizon to it **or
thirty seconds, whichever is larger**. On this switch that is not a hedge: an
honest 0.13 floors the poll interval at two seconds and buys the first live
write about one recovery attempt, where thirty buys six. Write both numbers
down — the measurement is what the lab is for, the configured value is what
the run needs, and they should stop being one field. The plan carries that as
a follow-up.

## Step 2a: confirm the firmware the adapter was written for

The shell adapter is a set of authored patterns — prompts, and the marker the
pager prints — rather than captures from a device. A firmware whose prompts
differ does not fail cleanly: the adapter waits for a prompt that never comes
and the command times out **inside configuration mode**, which presents as a
mutation whose effect nobody can establish.

```cli
show version | include SW: Version
```

This switch runs **10.0.10g_cd10T213**, and the adapter documents 10.0.10g.
All four of its prompt patterns and its pager marker were checked against this
device and match byte for byte. A materially different version is a reason to
stop and check the patterns, not to proceed and find out.

## Step 3: record the interface's current description

Read the interface you are about to change and write down exactly what it
says, character for character.

This is the restore value. **On this switch it is empty** — `ethernet 1/1/1`
has no description and `ifAlias.1` is blank — and empty is a value like any
other, restored with `no port-name` rather than with `port-name ""`. An empty
description and a description of one space are different states, and the
command that clears one is not the command that sets the other, so read it
rather than assuming which you have:

```cli
show running-config interface ethernet 1/1/1
```

No `port-name` line in the output means no description.

## Step 4: prove the restore path before making the change

Restore the description by hand, to the value you just recorded, using the
same access the run will use. Confirm the read shows what you expect.

Proving the way back before taking the step is the whole of this runbook's
safety. A restore path proven afterwards is a hope.

At the switch:

```cli
configure terminal
interface ethernet 1/1/1
no port-name
end
```

That is the restore for an interface whose description was empty, which is
this one. For any other recorded value the third line is
`port-name <the value you recorded>`; the syntax has no way to set an empty
name, which is why the empty case has its own command rather than a quoted
nothing.

**Leave the session cleanly, with `exit`.** FastIron caps concurrent SSH
sessions, and one that was not closed makes the next login fail with
`Permission denied` — which reads as a wrong password and is not one. That
costs more time to diagnose than it does to avoid.

## Step 5: state the blast radius, out loud, in writing

Before anything is dispatched, write down:

- **What changes:** the description of one interface on one switch.
- **The state before:** the description recorded in step 3.
- **The state after:** the description the intent carries.
- **How to undo it:** the `cli` block in step 4, with the value from step 3.

**The system may issue the write more than once, without being asked.** If
the first attempt cannot be confirmed, recovery polls the device — and on two
corroborating observations that still show the old value, or a positive fence,
it re-sends `port-name`. So the blast radius is not "one command"; it is "this
description, applied possibly more than once, within the horizon". Every
attempt sets the same value, so the end state is the same, but an operator
watching the switch will see it happen again and should not read that as
something having gone wrong.

**Running configuration only.** The capability enters configuration mode,
selects the interface, sets the description and leaves; it never writes
startup configuration. So the switch reverts on reload, and a reload is a
second way back that costs the device's uptime and nothing else. That is a
property of the code rather than a promise: the FastIron adapter's
`SetPortName` runs exactly four commands and none of them saves.

## Step 6: keep the API port off the network

The device service's API listener must not be reachable beyond the host for
the duration of this run.

This is a step rather than a caveat. `DeviceService` and `EdgeAdminService`
are served with no authorization check — accepted deliberately, with the
deployment's network boundary standing in until OpenFGA lands. This run puts
real switch credentials into central's registry, and the port that serves the
operator API is the port that yields them: a caller that reaches it can retire
the edge, issue itself a setup key, enroll as that edge, and read the
device's credential material. The boundary is protecting device credentials,
not an operator convenience.

## Step 7: read the fingerprint the intent must carry

Every intent names the firmware epoch it expects, and a mismatch blocks it
before anything is dispatched. The value comes from central, which learns it
from the edge when the edge onboards the device:

```sh
buf curl --schema "$FLOWSEER_REPO/spec/proto" --cacert "$CACERT" \
  --data "{\"device\":{\"device\":{\"id\":\"$DEVICE_ID\"}}}" \
  "$CENTRAL/flowseer.api.device.v1.DeviceService/GetDeviceAccessStatus"
```

If `firmwareFingerprint` is missing from the answer, **the edge has not
onboarded this device yet** and no mutation is possible until it has. That is
not a failure to wait out blindly: check the agent is running, that the
registry lists this device to this edge, and that the edge enrolled. An answer
with no fingerprint looks like this:

```text
{"highWatermark":"0"}
```

Export it, and everything below uses it:

```sh
export FINGERPRINT=$(buf curl --schema "$FLOWSEER_REPO/spec/proto" --cacert "$CACERT" \
  --data "{\"device\":{\"device\":{\"id\":\"$DEVICE_ID\"}}}" \
  "$CENTRAL/flowseer.api.device.v1.DeviceService/GetDeviceAccessStatus" \
  | sed -n 's/.*"firmwareFingerprint": *"\([^"]*\)".*/\1/p' | head -1)
test -n "$FINGERPRINT"
test ${#FINGERPRINT} -eq 64
```

`head -1` is not tidiness. Once the device has been read, the answer carries
the fingerprint twice — at the top level and again inside the observation's
provenance — so without it `FINGERPRINT` becomes two digests with a newline
between them, and the intent below is refused with
`intent.expected_firmware_fingerprint: string.max_len`. That refusal is
correct and it names the wrong culprit, so the length check is here to fail on
the line that actually produced it. This bites the second write and never the
first, which is how it survived one.

**And it picks the right one of the two, which is a choice rather than a
de-duplication.** The first is the top-level `firmware_fingerprint`, the epoch
central currently believes the device runs; the second belongs to an
observation and names the epoch that observation was taken under. They differ
whenever the firmware changed after the last read: recording a new fingerprint
does not touch the stored interface rows, so the top level moves ahead and the
provenance stays behind until the interface is read again. An intent must
carry the current epoch — central compares it against exactly that field and
refuses a mutation naming any other — so an intent built from the provenance
digest would be refused on a device whose firmware had moved, which is
precisely when you would most want the write to be refused for the right
reason.

## Step 8: dry run with `validate_only`

Send the intent with `validate_only` set. Central checks it and records
nothing; the response carries no mutation state.

```sh
buf curl --schema "$FLOWSEER_REPO/spec/proto" --cacert "$CACERT" \
  --data "{\"validateOnly\":true,\"intent\":{
    \"device\":{\"device\":{\"id\":\"$DEVICE_ID\"}},
    \"idempotencyKey\":\"$IDEMPOTENCY_KEY\",
    \"actor\":{\"operator\":{\"subject\":\"$OPERATOR\"}},
    \"accessPolicy\":{\"key\":\"$POLICY_KEY\",\"version\":\"$POLICY_VERSION\"},
    \"expectedFirmwareFingerprint\":\"$FINGERPRINT\",
    \"interfaceDescription\":{\"interfaceName\":\"$INTERFACE\",\"description\":\"$DESCRIPTION\"}
  }}" \
  "$CENTRAL/flowseer.api.device.v1.DeviceService/ApplyInterfaceDescription"
```

An accepted dry run answers `{}` — no mutation, because nothing was
admitted.

What this catches is everything decided before a dispatch: a firmware
fingerprint that no longer matches, an access policy version the device does
not pin, an unset horizon, a device the registry does not list.

**What it does not catch is anything about the device**, because nothing is
dispatched on this path. A clean dry run says the request is well-formed and
admissible. It says nothing about whether the switch will accept the command.

## Step 9: the approval checkpoint

**Stop here. Do not proceed without confirming the approval still stands.**

The user approved this device and this change on 2026-09-06, on the grounds
that it is a test device.

That approval covers this switch and this change. It does not cover another
device, another interface, another kind of change, a repeat on a different
day, or the same change on a switch that has since been moved into service.
It is not a general authorization to write to hardware and must not be read
as one. If anything about the target has changed since 2026-09-06, ask again.

## Step 10: make the write

Send the same intent without `validate_only`. This is the irreversible step.

```sh
buf curl --schema "$FLOWSEER_REPO/spec/proto" --cacert "$CACERT" \
  --data "{\"intent\":{
    \"device\":{\"device\":{\"id\":\"$DEVICE_ID\"}},
    \"idempotencyKey\":\"$IDEMPOTENCY_KEY\",
    \"actor\":{\"operator\":{\"subject\":\"$OPERATOR\"}},
    \"accessPolicy\":{\"key\":\"$POLICY_KEY\",\"version\":\"$POLICY_VERSION\"},
    \"expectedFirmwareFingerprint\":\"$FINGERPRINT\",
    \"interfaceDescription\":{\"interfaceName\":\"$INTERFACE\",\"description\":\"$DESCRIPTION\"}
  }}" \
  "$CENTRAL/flowseer.api.device.v1.DeviceService/ApplyInterfaceDescription"
```

The answer carries the mutation as admitted, with the sequence central gave
it. Then watch the record until it resolves — `unresolved` gone and the
interface row carrying the new description:

```sh
buf curl --schema "$FLOWSEER_REPO/spec/proto" --cacert "$CACERT" \
  --data "{\"device\":{\"device\":{\"id\":\"$DEVICE_ID\"}}}" \
  "$CENTRAL/flowseer.api.device.v1.DeviceService/GetDeviceAccessStatus"
```

How long to watch is not open-ended: recovery's budget is the device's horizon
plus one poll interval, and past it the mutation stops moving on its own and
becomes yours to resolve. With the horizon this run configures, that is a
little over that horizon.

**Heartbeat latency cannot be recorded here, and the reason is worth knowing
before you look for it.** A successful heartbeat logs nothing at any level:
`RunHeartbeat` writes a line when consecutive misses freeze the lane and when
contact is restored, and the success path records no start, no duration and no
completion. Raising the agent to DEBUG does not help — measured on
2026-09-09, where an agent at DEBUG across several intervals produced no
heartbeat line at all, and neither did central.

That matters more than a missing figure. Each attempt is bounded by the
heartbeat interval and two consecutive misses freeze the lane, so a central
answering more slowly than one interval is indistinguishable from a central
that is down — and the mechanism whose whole job is to notice that decides on
a latency nobody can see. The first evidence an operator gets is a frozen
lane. Until a duration is recorded around each attempt, the number that would
tell you whether the deadline and the interval want separating does not exist.

Do not substitute a loopback round trip for it. On a deployment where central
and the edge share a host, any RPC you can time yourself answers in a fraction
of a millisecond, and what this warning is about is network distance. A
figure like that written into a document as "what the lab measured" is worse
than the blank, because it reads as evidence about a quantity it never
measured.

## If it does not resolve

First, read what state it is in, because two of them look alike and want
opposite things.

**Disposed rejected, lane closed.** The edge could prove the command never
reached the device — a wrong interface name is the usual cause, and the
likeliest failure of a first write, since nothing checks `managed_interfaces`
against the switch until the write is attempted. Nothing happened to the
device. Fix the name and send the intent again with a new idempotency key.

**Resting at `POSSIBLY_APPLIED` with `INDETERMINATE`.** The command may have
been applied and the edge could not establish whether it was, and its budget
has run out. **Read the interface by hand before deciding anything** — the
switch is the only thing that knows:

```cli
show running-config interface ethernet 1/1/1
```

Then end the mutation and say what should stand in its place. Abandoning is
first, and it needs the sequence from the apply's answer:

```sh
buf curl --schema "$FLOWSEER_REPO/spec/proto" --cacert "$CACERT" \
  --data "{\"device\":{\"device\":{\"id\":\"$DEVICE_ID\"}},\"sequence\":\"$SEQUENCE\",
    \"actor\":{\"operator\":{\"subject\":\"$OPERATOR\"}}}" \
  "$CENTRAL/flowseer.api.device.v1.DeviceService/AbandonMutation"
```

**Then resolve it with `replace`, and not with `restore` or `accept`.** This
is the part that is easy to get wrong, because the other two arms sound like
what you want and will refuse you:

- `restore` puts back what central expected. On a first write central expects
  nothing — an expectation is only recorded when a mutation verifies — so
  there is nothing to restore to and it refuses.
- `accept` adopts what the device carries. Central retains observations only
  for interfaces it already has an expectation for, so there is nothing to
  accept either. A read you did by hand at the switch is not an observation
  central holds.
- `replace` supersedes the interrupted mutation with a new intent, which is
  the whole of what you need: put the value you want — the one from step 3 if
  you are undoing, the intended one if you are going on.

```sh
buf curl --schema "$FLOWSEER_REPO/spec/proto" --cacert "$CACERT" \
  --data "{\"device\":{\"device\":{\"id\":\"$DEVICE_ID\"}},\"sequence\":\"$SEQUENCE\",
    \"actor\":{\"operator\":{\"subject\":\"$OPERATOR\"}},
    \"replace\":{
      \"device\":{\"device\":{\"id\":\"$DEVICE_ID\"}},
      \"idempotencyKey\":\"$(uuidgen | tr 'A-Z' 'a-z')\",
      \"actor\":{\"operator\":{\"subject\":\"$OPERATOR\"}},
      \"accessPolicy\":{\"key\":\"$POLICY_KEY\",\"version\":\"$POLICY_VERSION\"},
      \"expectedFirmwareFingerprint\":\"$FINGERPRINT\",
      \"interfaceDescription\":{\"interfaceName\":\"$INTERFACE\",\"description\":\"\"}
    }}" \
  "$CENTRAL/flowseer.api.device.v1.DeviceService/ResolveDesynchronization"
```

**Central refuses a resolution until the edge has acknowledged the
abandonment, and nothing tells you when that is.** The status call does not
carry whether the acknowledgement is still owed, so the only signal is that
this call stops being refused — retry it. That is a gap and it is recorded as
one.

Two refusals to tell apart while you retry, because one goes away and one does
not. *"The edge has not yet acknowledged how this mutation ended"* is the one
to retry. *"There is nothing recorded for this interface to resolve against"*
is `restore` or `accept` telling you they cannot work here, and retrying it
will never help.

## Afterwards

Restore the description unless there is a reason to leave it, confirm the read,
and write down what the run measured: the horizon, the heartbeat latency, and
anything the switch did that no fixture predicted. The last of those is what
this run is really for.
