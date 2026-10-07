# /// script
# requires-python = "==3.13.*"
# dependencies = []
# ///
"""Capture an SNMP agent into snmpsim's .snmprec replay format.

For use by FlowSeer's T3 integration tier.

Usage:
    uv run capture-snmprec.py \\
        --target <host[:port]> \\
        [--community <v2c-community>] \\
        [--root <oid>] \\
        [--output <path>]

Defaults:
    --community public
    --root      .1
    --output    standard output (redirect to a file under
                testdata/snmprec/<vendor>/)

Requires snmpwalk (net-snmp) on PATH. The output format is the canonical
snmpsim .snmprec shape:

    <numeric-oid>|<asn1-type>|<value>

The converter maps the textual TYPE field net-snmp prints into the numeric
type codes snmpsim expects. SR Linux's `snmpwalk -OQ -OU -Onq` already emits
a numeric OID and decoded value; this script trims the surrounding quoting
and adds the type column.

Workflow (adding a new vendor capture):
    1. Bring up the device with v2c read-only community (or v3 USM).
    2. uv run capture-snmprec.py --target <ip> --community public \\
           > src/protocol/snmp/test/integration/testdata/snmprec/<vendor>/<device>.snmprec
    3. Append a manifest entry pointing at the new file.
    4. go test -tags=snmp_integration_t3 ./src/protocol/snmp/test/integration/...

snmpwalk -OnQU emits "<numeric-oid> = <type>: <value>" (when type is
present) or "<numeric-oid> = <value>" (when type was elided). This converter
handles the common cases; uncommon types (NSAP, BIT STRING, etc.) are
best-effort. Real vendor captures usually need manual review of edge OIDs.
"""

import argparse
import re
import shutil
import subprocess
import sys
from pathlib import Path

# net-snmp type label -> snmpsim numeric type code. A label not listed here
# is an OCTET STRING (4).
TYPE_CODES = {
    "INTEGER": 2,
    "STRING": 4,
    "Hex-STRING": 4,
    "OID": 6,
    "IpAddress": 64,
    "Counter32": 65,
    "Gauge32": 66,
    "Timeticks": 67,
    "Opaque": 68,
    "Counter64": 70,
}
OCTET_STRING = 4

# Timeticks values from snmpwalk are sometimes wrapped as "(12345) 0:02:03.45";
# keep the parenthesised raw count when present.
TICKS = re.compile(r"\(([0-9]+)\)")


def convert(line: str) -> str:
    """Return the .snmprec row for one line of snmpwalk output."""
    # The value is the second field of a split on " = ", so a line without
    # one (a Hex-STRING continuation) has an empty value and a value holding
    # " = " is cut at it.
    fields = line.split(" = ")
    oid = fields[0]
    rest = fields[1] if len(fields) > 1 else ""

    colon = rest.find(":")
    if colon >= 0:
        label = rest[:colon]
        # Skip the colon and the one character after it, whatever it is.
        value = rest[colon + 2 :]
    else:
        label = "STRING"
        value = rest

    # Strip surrounding quotes from STRING values.
    value = value.removeprefix('"').removesuffix('"')

    code = TYPE_CODES.get(label, OCTET_STRING)

    # snmpsim prefers no leading dot on the OID.
    oid = oid.removeprefix(".")

    if code == 67:
        ticks = TICKS.search(value)
        if ticks:
            value = ticks.group(1)

    return f"{oid}|{code}|{value}"


def main() -> int:
    parser = argparse.ArgumentParser(
        description="Capture an SNMP agent into snmpsim's .snmprec replay format.",
    )
    parser.add_argument("--target", required=True, help="agent address, host[:port]")
    parser.add_argument("--community", default="public", help="v2c community (default: public)")
    parser.add_argument("--root", default=".1", help="OID to walk from (default: .1)")
    parser.add_argument("--output", help="file to write (default: standard output)")
    args = parser.parse_args()

    if shutil.which("snmpwalk") is None:
        print("snmpwalk not on PATH; install net-snmp", file=sys.stderr)
        return 1

    walk = subprocess.run(
        ["snmpwalk", "-v2c", "-c", args.community, "-OnQU", "-Cc", args.target, args.root],
        stdout=subprocess.PIPE,
    )

    lines = walk.stdout.decode("utf-8", errors="surrogateescape").split("\n")
    if lines[-1] == "":
        lines.pop()
    converted = "".join(convert(line) + "\n" for line in lines)

    if args.output is None:
        sys.stdout.reconfigure(encoding="utf-8", errors="surrogateescape", newline="\n")
        sys.stdout.write(converted)
        sys.stdout.flush()
    else:
        Path(args.output).write_text(
            converted, encoding="utf-8", errors="surrogateescape", newline="\n"
        )
    return walk.returncode


if __name__ == "__main__":
    sys.exit(main())
