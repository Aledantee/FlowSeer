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

The device commands are `configure terminal`, `interface <name>`, and
`port-name <text>` — or `no port-name` to clear it, because the syntax has no
way to set an empty name.

## Step 5: state the blast radius, out loud, in writing

Before anything is dispatched, write down:

- **What changes:** the description of one interface on one switch.
- **The state before:** the description recorded in step 3.
- **The state after:** the description the intent carries.
- **How to undo it:** `configure terminal`, `interface <name>`,
  `port-name <recorded value>` — or `no port-name` if it was empty.

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

## Step 7: dry run with `validate_only`

Send the intent with `validate_only` set. Central checks it and records
nothing; the response carries no mutation state.

What this catches is everything decided before a dispatch: a firmware
fingerprint that no longer matches, an access policy version the device does
not pin, an unset horizon, a device the registry does not list.

**What it does not catch is anything about the device**, because nothing is
dispatched on this path. A clean dry run says the request is well-formed and
admissible. It says nothing about whether the switch will accept the command.

## Step 8: the approval checkpoint

**Stop here. Do not proceed without confirming the approval still stands.**

The user approved this device and this change on 2026-09-06, on the grounds
that it is a test device.

That approval covers this switch and this change. It does not cover another
device, another interface, another kind of change, a repeat on a different
day, or the same change on a switch that has since been moved into service.
It is not a general authorization to write to hardware and must not be read
as one. If anything about the target has changed since 2026-09-06, ask again.

## Step 9: make the write

Send the intent without `validate_only`. Then watch central's record until
the mutation resolves: `GetDeviceAccessStatus` until `unresolved` is unset and
the interface row carries the new description.

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
