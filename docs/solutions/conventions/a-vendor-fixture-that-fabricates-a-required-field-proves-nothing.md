---
title: A conformance fixture that fabricates a required field the source cannot report proves invented data validates, not real representability
date: 2026-09-26
category: conventions
module: test/conformance/proto
problem_type: convention
component: schema
severity: medium
applies_when:
  - "Writing a conformance fixture that maps a real vendor API or protocol payload into a FlowSeer message"
  - "A required field in the target message has no counterpart in the vendor source the fixture cites"
  - "Reviewing a plan requirement of the form 'a <vendor> payload maps to <message>'"
related_components: [spec/proto/flowseer]
tags: [conformance, fixture, protovalidate, vendor-mapping, required-fields]
---

A vendor-mapping conformance fixture earns its keep by proving a real payload
from the named source maps into the target message and validates. If the target
message has a required field the source never reports, the fixture can only pass
by inventing that field — and then it proves that *invented* data validates, not
that the vendor's data is representable. The invented value also reads as real to
every later reader.

This surfaced when a UniFi endpoint fixture set `WirelessAttachment.bssid`
(required) to a hand-written locally-administered MAC, because the UniFi Network
API reports a client's AP by id but never a BSSID (confirmed across every
official surface; the legacy controller API has it but is unpublished and
unversioned). The fixture declared a `BSSID json:"bssid"` payload field the
UniFi schema does not contain, so the mapper always fabricated.

A required field the source cannot fill is a signal about the schema, not a
licence to fabricate. Resolve it one of three ways, in order of preference:

- **Loosen the field to match reality.** If the fact is genuinely optional
  across sources, make it optional; if a related identity is always available,
  model the choice (here: a required `oneof serving_bss { bssid | vendor_bss_id }`
  so a source without a BSSID names the serving AP/BSS by its own id). See
  [[a-oneof-both-arms-set-is-unrepresentable-so-validate-the-empty-case]].
- **Map from a surface that reports the field**, if one exists and the project
  vendors it. Do not reach for an unpublished/unversioned vendor API to satisfy
  a required field the official surface omits.
- **Record the gap.** If the source truly cannot produce the message, record it
  as the plan's Stop-condition outcome rather than hiding a fabricated value
  behind a synthesized constant and a dead payload field.

## How to apply

A vendor fixture's payload struct carries only fields the cited source schema
defines, and the mapper sets only what those fields supply. When a required
target field has no source, change the schema (loosen or model the choice)
instead of the fixture. Assert the mapped value, so a fabricated one cannot pass
unremarked.

## Evidence

- The fix: `spec/proto/flowseer/net/endpoint/v1/wireless_attachment.proto`
  (`oneof serving_bss` with `bssid` and `vendor_bss_id`, required).
- The corrected fixture maps `vendor_bss_id` from the UniFi client's
  `uplinkDeviceId` and asserts `HasBssid()` is false:
  `test/conformance/proto/endpoint_unifi_fixture_test.go`.
- The oneof conformance cases (no arm fails, each arm passes):
  `test/conformance/proto/endpoint_rules_test.go`
  (`TestWirelessAttachmentServingBss`).
- The source has no BSSID: the vendored `spec/openapi/ubiquiti/unifi-network-openapi-v10.4.57.json`
  contains "BSSID" only as a RADIUS NAS-ID enum value, and the client and device
  radio schemas expose none.

## What it does not cover

A fixture may still hand-build a message directly (not through a vendor mapper)
to exercise a validation rule; that is not a vendor-representability claim and
carries no source-fidelity obligation. Optional fields left unset are fine — the
rule is about fabricating a value the source cannot supply, not about omitting
one it does not.
