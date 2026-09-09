#!/bin/sh
# Render the prototext flowseer.api.edge.v1.EdgeProvisioning an agent reads,
# from the JSON body CreateEdge returned.
#
# It exists because the two representations differ in one place that a person
# should not be doing by hand: trust_anchors is a bytes field, which JSON
# carries as base64 and prototext carries as an escaped byte string. Getting
# that wrong produces an anchor of the right length and the wrong value, and
# the first thing that notices is an edge refusing central's certificate with
# a message about the chain rather than about this file.
#
#   write-provisioning.sh <created.json> <central-url>
#
# The output holds a live setup key. Redirect it somewhere with the
# permissions a secret gets, and delete it once the edge has enrolled.
set -eu

created="${1:?usage: write-provisioning.sh <created.json> <central-url>}"
central="${2:?usage: write-provisioning.sh <created.json> <central-url>}"

setup_key=$(jq -r '.provisioning.setupKey' "$created")
[ -n "$setup_key" ] && [ "$setup_key" != "null" ] ||
	{ echo "write-provisioning: no setup key in $created" >&2; exit 1; }

printf 'central_url: "%s"\n' "$central"
printf 'setup_key: "%s"\n' "$setup_key"

# Each anchor: base64 out of JSON, then one \xNN escape per byte, which is
# what prototext reads back as the same 32 bytes.
jq -r '.provisioning.trustAnchors[]' "$created" | while IFS= read -r anchor; do
	escaped=$(printf '%s' "$anchor" | base64 -d | xxd -p -c 1 | sed 's/^/\\x/' | tr -d '\n')
	printf 'trust_anchors: "%s"\n' "$escaped"
done
