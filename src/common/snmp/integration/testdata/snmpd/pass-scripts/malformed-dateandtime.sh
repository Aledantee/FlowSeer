#!/bin/sh
# T1 forged-edge: malformed DateAndTime.
#
# Emits an 8-octet DateAndTime where the hour field is 99 (0x63), a
# RFC 2579 field-range violation (hours = 0..23). The library's
# DecodeDateAndTime (fixed in commit affa176 to enforce field ranges)
# must surface a typed decode error rather than a nonsense time.Time.
#
# DateAndTime layout (8 octets, short form):
#   yy yy mm dd hh mm ss ds
#   2024 01 01 99 01 01 01    -> 07 e8 01 01 63 01 01 01
#
# net-snmp pass protocol: mode and OID arrive as $1 and $2.
# Binary value bytes are emitted via printf with octal escapes
# (POSIX-portable across dash and bash).

mode="$1"
oid="$2"

emit() {
    echo ".1.3.6.1.4.1.99999.4.0"
    echo "string"
    # 8 raw bytes: 0x07 0xE8 0x01 0x01 0x63 0x01 0x01 0x01
    printf '\007\350\001\001\143\001\001\001\n'
}

case "$mode" in
    -g)
        case "$oid" in
            .1.3.6.1.4.1.99999.4 | .1.3.6.1.4.1.99999.4.0)
                emit
                exit 0
                ;;
        esac
        ;;
    -n)
        case "$oid" in
            .1.3.6.1.4.1.99999.4)
                emit
                exit 0
                ;;
        esac
        ;;
esac

exit 0
