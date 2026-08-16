#!/bin/sh
# T1 forged-edge: NoSuchInstance / NoSuchObject.
#
# Returns no output for any GET or GETNEXT below .1.3.6.1.4.1.99999.1,
# which causes snmpd to surface the requested instance as
# NoSuchInstance (when the OID is inside the registered subtree) or
# NoSuchObject (when it is past the subtree's end and the next-OID
# scan finds nothing).
#
# Invocation:
#   stdin line 1: -g | -n
#   stdin line 2: <OID>
#
# Exit 0 with empty stdout is the canonical "no value" signal.
exit 0
