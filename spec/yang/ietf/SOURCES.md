# IETF / IANA standard YANG modules

Shared standard modules for vendor trees that reference them without
vendoring their own copies (today: Ruckus ICX 9.0.00, whose bundle
imports these but does not ship them).

| Module | Revision | Provenance |
| --- | --- | --- |
| `ietf-inet-types.yang` | 2013-07-15 | RFC 6991; byte-identical copy of `spec/yang/cisco/iosxe/2611/ietf-inet-types.yang` |
| `ietf-yang-types.yang` | 2013-07-15 | RFC 6991; byte-identical copy of `spec/yang/cisco/iosxe/2611/ietf-yang-types.yang` |
| `ietf-interfaces.yang` | 2014-05-08 | RFC 7223; byte-identical copy of `spec/yang/cisco/iosxe/2611/ietf-interfaces.yang` |
| `iana-if-type.yang` | 2014-05-08 | IANA ifType registry; byte-identical copy of `spec/yang/cisco/iosxe/2611/iana-if-type.yang` |

These are IETF/IANA-published documents; the Cisco bundle is used as
the local donor because it carries them unmodified. Vendors that ship
their own copies keep using those (per-vendor search paths do not
include this directory unless listed in `yanggen.yaml`).
