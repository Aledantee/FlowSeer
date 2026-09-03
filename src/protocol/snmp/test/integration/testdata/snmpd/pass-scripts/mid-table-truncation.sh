#!/bin/sh
# T1 forged-edge: walk that yields three rows then a type-mismatched
# fourth row. Tests assert the Walker yields >= 3 well-formed
# OCTET STRINGs; a silent truncated walk that drops rows 1..3 is the
# regression mode this script protects against.
#
# Subtree layout:
#   .1.3.6.1.4.1.99999.6.1   string   "row-1"
#   .1.3.6.1.4.1.99999.6.2   string   "row-2"
#   .1.3.6.1.4.1.99999.6.3   string   "row-3"
#   .1.3.6.1.4.1.99999.6.4   counter  99            <-- wire-shape mismatch
#   .1.3.6.1.4.1.99999.6.5+  (no value)             <-- EndOfMibView
#
# net-snmp pass protocol: mode and OID arrive as $1 and $2.

mode="$1"
oid="$2"

emit_row() {
    case "$1" in
        .1.3.6.1.4.1.99999.6.1)
            echo ".1.3.6.1.4.1.99999.6.1"; echo "string"; echo "row-1"; return 0 ;;
        .1.3.6.1.4.1.99999.6.2)
            echo ".1.3.6.1.4.1.99999.6.2"; echo "string"; echo "row-2"; return 0 ;;
        .1.3.6.1.4.1.99999.6.3)
            echo ".1.3.6.1.4.1.99999.6.3"; echo "string"; echo "row-3"; return 0 ;;
        .1.3.6.1.4.1.99999.6.4)
            echo ".1.3.6.1.4.1.99999.6.4"; echo "counter"; echo "99"; return 0 ;;
    esac
    return 1
}

case "$mode" in
    -g)
        emit_row "$oid"
        exit 0
        ;;
    -n)
        case "$oid" in
            .1.3.6.1.4.1.99999.6 | .1.3.6.1.4.1.99999.6.0)
                emit_row .1.3.6.1.4.1.99999.6.1; exit 0 ;;
            .1.3.6.1.4.1.99999.6.1) emit_row .1.3.6.1.4.1.99999.6.2; exit 0 ;;
            .1.3.6.1.4.1.99999.6.2) emit_row .1.3.6.1.4.1.99999.6.3; exit 0 ;;
            .1.3.6.1.4.1.99999.6.3) emit_row .1.3.6.1.4.1.99999.6.4; exit 0 ;;
        esac
        ;;
esac

exit 0
