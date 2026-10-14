#!/bin/sh
# Creates and removes the epair(4) the bpfdev VM tests run on. The guest has
# no uv, so this stays shell. Usage: sh bpfdev-setup.sh up|down

set -eu

# destroy removes both ends and succeeds when the pair is already gone, so
# down leaves the guest clean after a run that failed at any point.
destroy() {
	if ifconfig epair0a >/dev/null 2>&1; then
		ifconfig epair0a destroy
	fi
}

case "${1:-}" in
up)
	destroy
	ifconfig epair0 create
	# ifdisabled keeps IPv6 from sending MLD reports and neighbour
	# solicitations, which would add to the packet counters the tests
	# assert on.
	ifconfig epair0a inet6 ifdisabled up
	ifconfig epair0b inet6 ifdisabled up
	;;
down)
	destroy
	;;
*)
	echo "usage: sh bpfdev-setup.sh up|down" >&2
	exit 2
	;;
esac
