#!/bin/sh
# T1 forged-edge: EndOfMibView.
#
# Returns a value for GET on .1.3.6.1.4.1.99999.2.0 so the OID is
# reachable, but emits no output for GETNEXT — so a Walk that arrives
# here moves past the subtree and snmpd produces EndOfMibView when no
# subsequent registered OID exists.
#
# net-snmp pass protocol: mode and OID arrive as $1 ($-g/-n/-s) and $2.

mode="$1"
oid="$2"

if [ "$mode" = "-g" ]; then
    case "$oid" in
        .1.3.6.1.4.1.99999.2.0)
            echo ".1.3.6.1.4.1.99999.2.0"
            echo "integer"
            echo "1"
            exit 0
            ;;
    esac
fi

# GETNEXT or unknown leaf: emit nothing → snmpd surfaces EndOfMibView
# once the walk passes this subtree.
exit 0
