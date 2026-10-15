#!/bin/sh
# Creates and removes the epair(4) the bpfdev VM tests run on. The guest has
# no uv, so this stays shell. Usage: sh bpfdev-setup.sh up|down
#
# With FLOWSEER_BPFDEV_FIXTURE_DIR set, up also starts tcpdump on epair0b,
# writing freebsd14-arm64.pcap into that directory, and down stops it. The
# capture holds only the EtherType the fixture test sends.

set -eu

ethertype=0x88b5

# stop_capture ends tcpdump with SIGTERM, which makes it flush the file, and
# waits for it to exit. It succeeds when no capture was started.
stop_capture() {
	pidfile="${FLOWSEER_BPFDEV_FIXTURE_DIR:-}/tcpdump.pid"
	if [ -z "${FLOWSEER_BPFDEV_FIXTURE_DIR:-}" ] || [ ! -f "$pidfile" ]; then
		return 0
	fi
	pid=$(cat "$pidfile") || return $?
	kill -TERM "$pid" 2>/dev/null || :
	for _ in 1 2 3 4 5 6 7 8 9 10 11 12 13 14 15 16 17 18 19 20; do
		if ! ps -p "$pid" >/dev/null 2>&1; then
			rm -f "$pidfile"
			return $?
		fi
		sleep 0.25 || return $?
	done
	echo "tcpdump $pid did not exit" >&2
	return 1
}

# start_capture returns once tcpdump has attached to epair0b, so no frame
# sent afterwards is missed. -U writes each packet out as it arrives.
start_capture() {
	dir=$FLOWSEER_BPFDEV_FIXTURE_DIR
	mkdir -p "$dir"
	chmod 777 "$dir"
	rm -f "$dir/freebsd14-arm64.pcap" "$dir/tcpdump.log"
	nohup tcpdump -U -n -i epair0b -w "$dir/freebsd14-arm64.pcap" ether proto "$ethertype" \
		</dev/null >"$dir/tcpdump.log" 2>&1 &
	echo $! >"$dir/tcpdump.pid"
	for _ in 1 2 3 4 5 6 7 8 9 10 11 12 13 14 15 16 17 18 19 20; do
		if grep -q "listening on" "$dir/tcpdump.log"; then
			return 0
		fi
		sleep 0.25
	done
	echo "tcpdump did not attach:" >&2
	cat "$dir/tcpdump.log" >&2
	return 1
}

# destroy removes both ends and succeeds when the pair is already gone, so
# down leaves the guest clean after a run that failed at any point.
destroy() {
	if ifconfig epair0a >/dev/null 2>&1; then
		ifconfig epair0a destroy
	fi
}

case "${1:-}" in
up)
	stop_capture
	destroy
	ifconfig epair0 create
	# ifdisabled keeps IPv6 from sending MLD reports and neighbour
	# solicitations, which would add to the packet counters the tests
	# assert on.
	ifconfig epair0a inet6 ifdisabled up
	ifconfig epair0b inet6 ifdisabled up
	if [ -n "${FLOWSEER_BPFDEV_FIXTURE_DIR:-}" ]; then
		start_capture
	fi
	;;
down)
	status=0
	stop_capture || status=1
	destroy || status=1
	exit "$status"
	;;
*)
	echo "usage: sh bpfdev-setup.sh up|down" >&2
	exit 2
	;;
esac
