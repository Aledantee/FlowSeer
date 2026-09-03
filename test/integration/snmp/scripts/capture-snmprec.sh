#!/usr/bin/env bash
# capture-snmprec.sh — capture an SNMP agent into snmpsim's .snmprec
# replay format for use by FlowSeer's T3 integration tier.
#
# Usage:
#   capture-snmprec.sh \
#       --target <host[:port]> \
#       --community <v2c-community> \
#       [--root <oid>] \
#       [--output <path>]
#
# Defaults:
#   --root      .1
#   --output    /dev/stdout (pipe to a file under testdata/snmprec/<vendor>/)
#
# Requires snmpwalk (net-snmp) on PATH. The output format is the
# canonical snmpsim .snmprec shape:
#
#   <numeric-oid>|<asn1-type>|<value>
#
# The converter maps the textual TYPE field net-snmp prints into the
# numeric type codes snmpsim expects. SR Linux's `snmpwalk -OQ -OU -Onq`
# already emits a numeric OID and decoded value; this script trims
# the surrounding quoting and adds the type column.
#
# Workflow (F3 — adding a new vendor capture):
#   1. Bring up the device with v2c read-only community (or v3 USM).
#   2. capture-snmprec.sh --target <ip> --community public \
#                         > test/integration/snmp/testdata/snmprec/<vendor>/<device>.snmprec
#   3. Append a manifest entry pointing at the new file.
#   4. go test -tags=snmp_integration_t3 ./test/integration/snmp/...

set -euo pipefail

TARGET=""
COMMUNITY="public"
ROOT=".1"
OUTPUT="/dev/stdout"

usage() {
    sed -n '2,30p' "$0"
    exit 1
}

while [ $# -gt 0 ]; do
    case "$1" in
        --target)    TARGET="$2"; shift 2 ;;
        --community) COMMUNITY="$2"; shift 2 ;;
        --root)      ROOT="$2"; shift 2 ;;
        --output)    OUTPUT="$2"; shift 2 ;;
        -h|--help)   usage ;;
        *)           echo "unknown arg: $1" >&2; usage ;;
    esac
done

if [ -z "$TARGET" ]; then
    echo "missing --target" >&2
    usage
fi

if ! command -v snmpwalk >/dev/null 2>&1; then
    echo "snmpwalk not on PATH; install net-snmp" >&2
    exit 1
fi

# net-snmp type label → snmpsim numeric type code.
#   INTEGER       2
#   STRING        4
#   Hex-STRING    4 (rendered as hex)
#   "NULL"        5
#   OID           6
#   IpAddress     64
#   Counter32     65
#   Gauge32       66
#   Timeticks     67
#   Opaque        68
#   Counter64     70
#
# snmpwalk -OnQ emits "<numeric-oid> = <type>: <value>" (when type is
# present) or "<numeric-oid> = <value>" (when type was elided). This
# converter handles the common cases; uncommon types (NSAP, BIT
# STRING, etc.) are best-effort. Real vendor captures usually need
# manual review of edge OIDs.

snmpwalk -v2c -c "$COMMUNITY" -OnQU -Cc "$TARGET" "$ROOT" \
  | awk -F' = ' '
        {
            oid = $1
            rest = $2
            # Split type and value, e.g. "STRING: foo" or "Counter32: 12"
            colon = index(rest, ":")
            if (colon > 0) {
                type = substr(rest, 1, colon - 1)
                value = substr(rest, colon + 2)
            } else {
                # No explicit type; default to OCTET STRING.
                type = "STRING"
                value = rest
            }

            # Strip surrounding quotes from STRING values.
            gsub(/^"|"$/, "", value)

            tnum = 4
            if (type == "INTEGER")        tnum = 2
            else if (type == "STRING")    tnum = 4
            else if (type == "Hex-STRING")tnum = 4
            else if (type == "OID")       tnum = 6
            else if (type == "IpAddress") tnum = 64
            else if (type == "Counter32") tnum = 65
            else if (type == "Gauge32")   tnum = 66
            else if (type == "Timeticks") tnum = 67
            else if (type == "Opaque")    tnum = 68
            else if (type == "Counter64") tnum = 70

            # Strip leading dot from OID (snmpsim prefers no leading dot).
            sub(/^\./, "", oid)

            # Timeticks values from snmpwalk are sometimes wrapped as
            # "(12345) 0:02:03.45"; keep the parenthesised raw count
            # when present.
            if (tnum == 67) {
                if (match(value, /\([0-9]+\)/)) {
                    value = substr(value, RSTART + 1, RLENGTH - 2)
                }
            }

            printf "%s|%d|%s\n", oid, tnum, value
        }
    ' > "$OUTPUT"
