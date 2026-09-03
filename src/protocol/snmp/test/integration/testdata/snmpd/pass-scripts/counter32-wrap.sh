#!/bin/sh
# T1 forged-edge: Counter32 boundary value.
#
# Returns the maximum Counter32 value (2^32 - 1) so the library's
# Counter32 decoder is exercised at the upper boundary. Detecting
# counter *wrap* is the job of an external observer comparing two
# successive reads — a pass-script is stateless, so this script pins
# boundary-value decode rather than wrap detection (see D8).
#
# net-snmp pass protocol: mode and OID arrive as $1 ($-g/-n/-s) and $2.

mode="$1"
oid="$2"

case "$mode" in
    -g)
        case "$oid" in
            .1.3.6.1.4.1.99999.3 | .1.3.6.1.4.1.99999.3.0)
                echo ".1.3.6.1.4.1.99999.3.0"
                echo "counter"
                echo "4294967295"
                exit 0
                ;;
        esac
        ;;
    -n)
        case "$oid" in
            .1.3.6.1.4.1.99999.3)
                echo ".1.3.6.1.4.1.99999.3.0"
                echo "counter"
                echo "4294967295"
                exit 0
                ;;
        esac
        ;;
esac

exit 0
