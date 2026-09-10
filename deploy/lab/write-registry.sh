#!/bin/sh
# Render the deployment's registry with the edge identifier central minted.
#
#   write-registry.sh <edge-id> [template]
#
# The template defaults to registry.textproto beside this script, which ships
# with a placeholder in the one position that cannot be known before central
# has run: RegistryIntegration.edge. Everything else in it is the deployment's
# own and is edited by hand.
set -eu

edge="${1:?usage: write-registry.sh <edge-id> [template]}"
# The template defaults to the shipped one, and FLOWSEER_REGISTRY_TEMPLATE
# overrides it — which is how a test drives this script against a device it
# can reach without the shipped file naming an address that resolves.
template="${2:-${FLOWSEER_REGISTRY_TEMPLATE:-$(dirname "$0")/registry.textproto}}"

placeholder=REPLACE-WITH-THE-EDGE-ID-CREATEEDGE-RETURNED
grep -q "$placeholder" "$template" ||
	{ echo "write-registry: $template has no $placeholder to replace" >&2; exit 1; }

sed "s/$placeholder/$edge/" "$template"
