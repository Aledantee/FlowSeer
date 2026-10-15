#!/bin/sh
# Creates and removes the VNET jail and tunnel interfaces for the rawsocket
# VM tests. The guest has no uv, so this stays shell. Usage: sh rawsocket-setup.sh up|down

set -eu

jail_name=rawsocket
pidfile="${FLOWSEER_RAWSOCKET_PIDFILE:-${FLOWSEER_RAWSOCKET_DIR:-/tmp}/rawsocket-ping.pid}"

# stop_pings ends background pings with SIGTERM and waits for them to exit.
# It succeeds when no pings were running.
stop_pings() {
	if [ ! -f "$pidfile" ]; then
		return 0
	fi
	pids=$(cat "$pidfile") || return $?
	for pid in $pids; do
		kill -TERM "$pid" 2>/dev/null || :
	done
	for _ in 1 2 3 4 5 6 7 8 9 10 11 12 13 14 15 16 17 18 19 20; do
		still_running=0
		for pid in $pids; do
			if ps -p "$pid" >/dev/null 2>&1; then
				still_running=1
				break
			fi
		done
		if [ "$still_running" -eq 0 ]; then
			rm -f "$pidfile"
			return $?
		fi
		sleep 0.25 || return $?
	done
	echo "ping $pids did not exit" >&2
	return 1
}

# start_pings starts background ping processes through vxlan0 and gre0
# toward the jail, writing their PIDs to the PID file.
start_pings() {
	piddir=$(dirname "$pidfile")
	mkdir -p "$piddir"
	nohup ping -i 0.5 10.99.42.2 </dev/null >/dev/null 2>&1 &
	pid_vxlan=$!
	nohup ping -i 0.5 10.99.100.2 </dev/null >/dev/null 2>&1 &
	pid_gre=$!
	echo "$pid_vxlan $pid_gre" >"$pidfile"
}

# destroy_jail removes the jail and succeeds when the jail is already gone.
destroy_jail() {
	if jls -j "$jail_name" >/dev/null 2>&1; then
		jail -r "$jail_name"
	fi
}

# destroy_iface destroys the named interface and succeeds when it is already gone.
destroy_iface() {
	if ifconfig "$1" >/dev/null 2>&1; then
		ifconfig "$1" destroy
	fi
}

# destroy removes the jail and all host interfaces. It attempts all steps
# and returns non-zero if any step fails.
destroy() {
	rc=0
	destroy_jail || rc=1
	destroy_iface vxlan0 || rc=1
	destroy_iface gre0 || rc=1
	destroy_iface epair0a || rc=1
	destroy_iface epair1a || rc=1
	return "$rc"
}

load_module() {
	mod=$1
	if ! kldstat -m "$mod" >/dev/null 2>&1; then
		kldload "$mod"
	fi
}

case "${1:-}" in
up)
	stop_pings
	destroy || :

	load_module if_vxlan
	load_module if_gre

	ifconfig epair0 create
	ifconfig epair1 create

	jail -c name="$jail_name" vnet persist path=/ allow.raw_sockets
	jexec "$jail_name" ifconfig lo0 inet 127.0.0.1/8 up

	ifconfig epair0b vnet "$jail_name"
	ifconfig epair1a vnet "$jail_name"
	ifconfig epair1b vnet "$jail_name"

	# ifconfig takes one address family per call, so IPv6 is disabled
	# before the IPv4 address is set.
	ifconfig epair0a inet6 ifdisabled up
	ifconfig epair0a inet 10.99.0.1/24
	jexec "$jail_name" ifconfig epair0b inet6 ifdisabled up
	jexec "$jail_name" ifconfig epair0b inet 10.99.0.2/24
	jexec "$jail_name" ifconfig epair0b inet 10.99.0.3/32 alias
	jexec "$jail_name" ifconfig epair1a inet6 ifdisabled up
	jexec "$jail_name" ifconfig epair1b inet6 ifdisabled up

	ifconfig vxlan0 create vxlanid 42 vxlanlocal 10.99.0.1 vxlanremote 10.99.0.2
	ifconfig vxlan0 inet 10.99.42.1/24 up

	ifconfig gre0 create tunnel 10.99.0.1 10.99.0.2
	ifconfig gre0 inet 10.99.100.1 10.99.100.2 up

	start_pings
	;;
down)
	status=0
	stop_pings || status=1
	destroy || status=1
	exit "$status"
	;;
*)
	echo "usage: sh rawsocket-setup.sh up|down" >&2
	exit 2
	;;
esac
