---
title: Two Sources of "the Same" Metadata Can Use Different Vocabularies, and a Diff That Flags Everything Looks Like It Is Working
date: 2026-09-18
last_verified: 2026-09-18
category: conventions
module: src/protocol/yang
problem_type: bug
component: protocol_client
severity: high
symptoms:
  - "A comparison between a local record and a device's advertisement reports a difference for nearly every entry, and the volume reads as a genuine finding about an out-of-date peer"
  - "The same comparison behaves correctly against one transport and reports everything against another, with no error from either"
  - "Two functions documented as feeding the same comparison return the same Go type and carry values that were never comparable"
root_cause: "DiffRevisions compared revision strings by equality. A NETCONF hello carries RFC 7950 revision dates, so both sides were dates and the comparison held. gNMI Capabilities reports an OpenConfig module's openconfig-version semantic version instead, so every OpenConfig model compared a semver against a vendored date, could never be equal, and was reported as drift."
resolution_type: code_fix
applies_when:
  - "Comparing a locally recorded version, revision, or identifier against one a peer advertises, where a second transport or vendor can supply the same field"
  - "Reading a drift, diff, or reconciliation report in which most or all entries differ, and deciding whether that is a finding about the peer or a broken comparison"
  - "Adding a transport or vendor to a check that already works, where the new source populates an existing map[string]string"
  - "Writing or reviewing two accessors documented as feeding one comparison, especially when one is named for what it is compared against rather than what it returns"
related_components: [netconf, gnmi, code_generation]
tags: [versioning, yang, openconfig, comparison, false-positive, drift-detection]
---

## The situation

FlowSeer records the YANG module revisions its generated bindings were built
from, and compares them against what a device advertises so a mismatch
surfaces before a decode quietly returns nothing. Three functions feed that
comparison, and all three return `map[string]string`:

- `ParseLockfileRevisions` (`src/protocol/yang/revisions.go:37`) reads the
  generator lockfile and yields RFC 7950 revision dates.
- `netconf.Session.ModuleRevisions` (`src/protocol/netconf/session.go:213`)
  pulls `revision=Y` out of the hello's capability URIs — also dates.
- `gnmi.Capabilities.ModelRevisions` (`src/protocol/gnmi/session.go:69`)
  returns each model's `Version`, which for an OpenConfig module is its
  `openconfig-version` semantic version.

The first two speak dates. The third speaks semver. Nothing in the type
system separates them, both accessors are named `*Revisions`, and the gNMI
one's own comment calls what it returns "advertised versions" while the name
says revisions.

## What is true

A comparison across two sources must first establish that both sides use the
same vocabulary. String inequality is evidence of difference only once that
holds; before it, inequality is evidence of nothing at all.

The detector was correct against NETCONF and wrong against gNMI, and the
wrongness was invisible because of how it presented. Against a Cisco CSR1000v
running IOS-XE 17.3.2, comparing 493 device modules against a vendored 26.11
tree, the drift list was long and every line was true — the device really is
six years behind. Against Arista vEOS-lab 4.33, the list was also long, and
every OpenConfig line was meaningless:

```
revision drift: openconfig-bgp vendored 2023-12-28, device 9.8.0
revision drift: openconfig-aaa vendored 2022-07-29, device 1.0.0
```

Both runs look the same from a distance: a detector emitting plenty of
findings about an older device. That is the trap. A check that flags
everything and a check that works are hard to tell apart when the story
"this peer is out of date" is plausible, and a check that flags *nothing*
would have drawn suspicion far sooner.

## How to apply it

Classify before comparing, and keep what you could not compare — dropping it
silently trades a false positive for a false negative:

```go
func isRevisionDate(rev string) bool {
	if len(rev) != len("2006-01-02") {
		return false
	}
	_, err := time.Parse(time.DateOnly, rev)
	return err == nil
}

// Returns drift and incomparable pairs separately.
if isRevisionDate(vendoredRev) != isRevisionDate(advertisedRev) {
	incomparable = append(incomparable, entry)
	continue
}
drift = append(drift, entry)
```

Callers then decide what each set means. Over NETCONF every revision is a
date, so an incomparable pair is a malformed advertisement and the lab suite
fails on it; over gNMI it is the expected OpenConfig case and is logged
(`src/protocol/netconf/test/integration/t4_lab_test.go:266`,
`src/protocol/gnmi/test/integration/t4_lab_test.go:270`).

When a diff reports a difference for most of its entries, suspect the
comparison before believing the report. Check one entry by hand against both
sources: the vocabularies diverge in the first entry you look at, if they
diverge at all.

## Evidence

`src/protocol/yang/revisions.go:87` splits the two outcomes, and
`TestDiffRevisionsSeparatesIncomparableVocabularies`
(`src/protocol/yang/revisions_test.go:55`) pins it with a semver, a matching
date, and a genuinely drifted date in one table — the matching date proves the
split did not simply reclassify everything into the new bucket.

The observation is from a live run on 2026-09-18 against Arista vEOS-lab
4.33.1.1F at the lab's gNMI target, alongside the IOS-XE 17.3.2 NETCONF run
whose drift list was correct.

## What this does not cover

It classifies vocabularies; it does not compare semvers. An OpenConfig module
whose device semver genuinely lags the vendored one is reported as
incomparable, not as drift, so real OpenConfig drift is still undetected. The
fuller fix is recording `openconfig-version` in the generator lockfile beside
the revision date so semver compares against semver, which needs a `yanggen`
change and a lockfile regeneration.

Nor does it say what a caller should do about drift: detection is
warn-and-proceed, and whether a mismatch is fatal stays the caller's call.

For the neighbouring failure where a check stops running rather than
over-reporting, see
[A Gate Selected By Name Stops Running Silently](a-gate-selected-by-name-stops-running-silently.md).
