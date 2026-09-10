#!/bin/sh
# Render the deployment's registry with the edge identifier central minted.
#
#   write-registry.sh <edge-id> [template]
#
# The template defaults to registry.textproto beside this script. This script
# substitutes one position, RegistryIntegration.edge, because that identifier
# cannot be known before central has run. Everything else in the template is
# the deployment's own and is edited by hand — including two positions this
# script refuses to render unfilled, see below.
set -eu

edge="${1:?usage: write-registry.sh <edge-id> [template]}"
shipped="$(dirname "$0")/registry.textproto"
# The template defaults to the shipped one, and FLOWSEER_REGISTRY_TEMPLATE
# overrides it — which is how a test drives this script against a device it
# can reach without the shipped file naming an address that resolves.
template="${2:-${FLOWSEER_REGISTRY_TEMPLATE:-$shipped}}"

placeholder=REPLACE-WITH-THE-EDGE-ID-CREATEEDGE-RETURNED
grep -q "$placeholder" "$template" ||
	{ echo "write-registry: $template has no $placeholder to replace" >&2; exit 1; }

# Two positions this script cannot fill and the shipped file cannot know: the
# device's management address, which is escaped bytes rather than a dotted
# string, and its SSH host key digest, which is read off the device. Both are
# placeholders in the shipped template and both have a runbook step.
#
# Leaving either unfilled fails late and misleadingly. An unfilled address
# points the registry at the documentation range, and the agent then logs
# "listed device cannot be onboarded" with a timed-out identity probe — which
# is exactly what the runbook prints as the expected state while the switch is
# still off, so an operator waits for a device that is already on. An unfilled
# digest is a pin no device offers, and it fails when the mutation opens its
# shell, which is the irreversible step.
#
# Checked only when rendering the shipped file. A caller that supplies its own
# template has filled these its own way, which is how the integration test
# aims this at a loopback port; the cost is that a hand-copied template is
# unguarded.
if [ "$template" = "$shipped" ]; then
	if grep -qF '\300\000\002\006' "$template"; then
		printf '%s\n' "write-registry: $template still points at the placeholder address 192.0.2.6; set the device's address first" >&2
		exit 1
	fi
	if grep -qF REPLACEwithTHEsshHOSTkeyDIGESTofTHEdevice00 "$template"; then
		printf '%s\n' "write-registry: $template still carries the placeholder ssh_host_key_sha256; read the device's ECDSA digest first" >&2
		exit 1
	fi
fi

sed "s/$placeholder/$edge/" "$template"
