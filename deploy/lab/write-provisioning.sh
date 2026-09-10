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

# Render every anchor before writing anything, because the output holds a
# live setup key and a file that carries one with no anchors is worse than no
# file at all: an edge reads it as "trust nothing", refuses central's
# certificate, and cannot enroll — after the key has been consumed.
#
# In POSIX sh a pipeline's status is its last command's, so a jq or base64
# failure inside `jq ... | while read` is invisible to set -e. The anchors are
# therefore rendered into a file first and the file checked, which is also why
# nothing here re-expands them: an escape sequence run through printf %b a
# second time is not the byte it names.
anchors=$(jq -r '.provisioning.trustAnchors[]' "$created") ||
	{ echo "write-provisioning: could not read trust anchors from $created" >&2; exit 1; }
[ -n "$anchors" ] ||
	{ echo "write-provisioning: no trust anchors in $created" >&2; exit 1; }

rendered=$(mktemp)
trap 'rm -f "$rendered"' EXIT

# Each anchor: base64 out of JSON, then one \xNN escape per byte, which is
# what prototext reads back as the same 32 bytes.
printf '%s\n' "$anchors" | while IFS= read -r anchor; do
	[ -n "$anchor" ] || continue
	escaped=$(printf '%s' "$anchor" | base64 -d | xxd -p -c 1 | sed 's/^/\\x/' | tr -d '\n') || exit 1
	[ -n "$escaped" ] || exit 1
	printf 'trust_anchors: "%s"\n' "$escaped" >> "$rendered"
done

[ -s "$rendered" ] ||
	{ echo "write-provisioning: no trust anchor could be decoded from $created" >&2; exit 1; }

printf 'central_url: "%s"\n' "$central"
printf 'setup_key: "%s"\n' "$setup_key"
cat "$rendered"
