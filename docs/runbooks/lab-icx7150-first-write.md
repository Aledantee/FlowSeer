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

Confirm it while the switch is still off and the configuration is whatever it
was left as. It is the one precondition that is cheaper to check than to
diagnose.

## Step 2: measure the delayed-apply horizon, and know what the number buys

Set one interface description by hand, then read it back repeatedly until it
appears. The longest gap you see is the horizon.

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

For this run, measure the true value, record it, and set the registry's
horizon to the measured value **or thirty seconds, whichever is larger**.
Write both numbers down: the measurement is what the lab is for, and the
configured value is what the run needs. They should stop being one field, and
the plan carries that as a follow-up.

## Step 3: record the interface's current description

Read the interface you are about to change and write down exactly what it
says, character for character, including an empty description.

This is the restore value. Do not skip it because the description "looks
empty" — an empty description and a description of one space are different
states, and the command that clears one is not the command that sets the
other.

## Step 4: prove the restore path before making the change

Restore the description by hand, to the value you just recorded, using the
same access the run will use. Confirm the read shows what you expect.

Proving the way back before taking the step is the whole of this runbook's
safety. A restore path proven afterwards is a hope.

At the switch:

```cli
configure terminal
interface ethernet 1/1/1
port-name <the value you recorded>
end
```

`no port-name` clears it instead, because the syntax has no way to set an
empty name — so an interface whose description was empty is restored with
that rather than with `port-name ""`.

## Step 5: state the blast radius, out loud, in writing

Before anything is dispatched, write down:

- **What changes:** the description of one interface on one switch.
- **The state before:** the description recorded in step 3.
- **The state after:** the description the intent carries.
- **How to undo it:** the `cli` block in step 4, with the value from step 3.

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
  | sed -n 's/.*"firmwareFingerprint": *"\([^"]*\)".*/\1/p')
test -n "$FINGERPRINT"
```

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

**Record the observed heartbeat latency, whether or not it looks
interesting.** Each heartbeat attempt is bounded by the heartbeat interval,
and two consecutive misses freeze the lane — so a central answering more
slowly than one interval is indistinguishable from a central that is down. If
the lab shows latency anywhere near the interval, the deadline and the
interval want separating rather than both being raised. Nobody can pick that
number from a desk, and this is the only run that will have the measurement.

## If it does not resolve

The mutation resting at `POSSIBLY_APPLIED` with `INDETERMINATE` means the
edge could not establish the effect within the horizon. The change may have
applied. Read the interface by hand before deciding anything.

Then `AbandonMutation` for the sequence, and `ResolveDesynchronization` with
`restore` to put back the value from step 3, or `accept` if the hand read
shows the change did apply and you want central to adopt it.

**Central refuses the resolution until the edge acknowledges the
abandonment**, and nothing tells you when that has happened —
`GetDeviceAccessStatus` does not carry whether the acknowledgement is still
owed. The only signal is that the call stops being refused, so retry it. That
gap is recorded as a follow-up; until it closes, retrying is the procedure
rather than a workaround.

## Afterwards

Restore the description unless there is a reason to leave it, confirm the read,
and write down what the run measured: the horizon, the heartbeat latency, and
anything the switch did that no fixture predicted. The last of those is what
this run is really for.
