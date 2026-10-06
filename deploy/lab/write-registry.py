# /// script
# requires-python = "==3.13.*"
# dependencies = []
# ///
"""Render the deployment's registry with the edge identifier central minted.

    write-registry.py <edge-id> [template]

The template defaults to registry.textproto beside this script, and
FLOWSEER_REGISTRY_TEMPLATE overrides that, which is how a test drives this
script against a device it can reach without the shipped file naming an
address that resolves. This script substitutes one position,
RegistryIntegration.edge, because that identifier cannot be known before
central has run. Everything else in the template is the deployment's own and
is edited by hand, including two positions this script refuses to render
unfilled, see below.
"""

import argparse
import os
import sys
from pathlib import Path

PLACEHOLDER = "REPLACE-WITH-THE-EDGE-ID-CREATEEDGE-RETURNED"

# Two positions this script cannot fill and the shipped file cannot know: the
# device's management address, which is escaped bytes rather than a dotted
# string, and its SSH host key digest, which is read off the device. Both are
# placeholders in the shipped template and both have a runbook step.
#
# Leaving either unfilled fails late and misleadingly. An unfilled address
# points the registry at the documentation range, and the agent then logs
# "listed device cannot be onboarded" with a timed-out identity probe, which
# is exactly what the runbook prints as the expected state while the switch is
# still off, so an operator waits for a device that is already on. An unfilled
# digest is a pin no device offers, and it fails when the mutation opens its
# shell, which is the irreversible step.
ADDRESS_PLACEHOLDER = rb"\300\000\002\006"
DIGEST_PLACEHOLDER = b"REPLACEwithTHEsshHOSTkeyDIGESTofTHEdevice00"


def main() -> int:
    parser = argparse.ArgumentParser(description="Render the deployment's registry with the edge identifier.")
    parser.add_argument("edge", help="the edge identifier CreateEdge returned")
    parser.add_argument("template", nargs="?", help="the template to render (default: registry.textproto beside this script)")
    args = parser.parse_args()
    if not args.edge:
        parser.error("argument edge: expected a non-empty value")

    shipped = Path(__file__).resolve().parent / "registry.textproto"
    template = Path(args.template or os.environ.get("FLOWSEER_REGISTRY_TEMPLATE") or shipped)

    try:
        body = template.read_bytes()
    except OSError as error:
        print(f"write-registry: cannot read {template}: {error.strerror}", file=sys.stderr)
        return 1

    placeholder = PLACEHOLDER.encode("ascii")
    if placeholder not in body:
        print(f"write-registry: {template} has no {PLACEHOLDER} to replace", file=sys.stderr)
        return 1

    # Checked only when rendering the shipped file, under any spelling of its
    # path. A caller that supplies its own template has filled these its own
    # way, which is how the integration test aims this at a loopback port; the
    # cost is that a hand-copied template is unguarded.
    if template.resolve() == shipped:
        if ADDRESS_PLACEHOLDER in body:
            print(
                f"write-registry: {template} still points at the placeholder address 192.0.2.6; "
                "set the device's address first",
                file=sys.stderr,
            )
            return 1
        if DIGEST_PLACEHOLDER in body:
            print(
                f"write-registry: {template} still carries the placeholder ssh_host_key_sha256; "
                "read the device's ECDSA digest first",
                file=sys.stderr,
            )
            return 1

    sys.stdout.buffer.write(body.replace(placeholder, args.edge.encode("utf-8")))
    sys.stdout.buffer.flush()
    return 0


if __name__ == "__main__":
    sys.exit(main())
