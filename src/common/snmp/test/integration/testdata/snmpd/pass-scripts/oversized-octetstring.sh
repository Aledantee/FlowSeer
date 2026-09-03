#!/bin/sh
# T1 forged-edge: oversized OCTET STRING.
#
# Returns an OCTET STRING of >1500 bytes (the typical UDP MTU) so the
# library is exercised against a value that crosses a single SNMP
# datagram. The decoder must accept the length and surface the full
# payload; tests assert len(value) > 1500 and that no transport-level
# truncation occurred.
#
# net-snmp pass protocol: mode and OID arrive as $1 and $2.

mode="$1"
oid="$2"

emit() {
    echo ".1.3.6.1.4.1.99999.5.0"
    echo "string"
    # Build a 1700-byte payload of repeating ASCII 'A'. awk is in
    # debian:bookworm-slim by default (mawk).
    awk 'BEGIN { s = ""; for (i = 0; i < 1700; i++) s = s "A"; print s }'
}

case "$mode" in
    -g)
        case "$oid" in
            .1.3.6.1.4.1.99999.5 | .1.3.6.1.4.1.99999.5.0)
                emit
                exit 0
                ;;
        esac
        ;;
    -n)
        case "$oid" in
            .1.3.6.1.4.1.99999.5)
                emit
                exit 0
                ;;
        esac
        ;;
esac

exit 0
