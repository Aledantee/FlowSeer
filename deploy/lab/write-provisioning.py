# /// script
# requires-python = "==3.13.*"
# dependencies = []
# ///
"""Render the prototext flowseer.model.edge.v1.EdgeProvisioning an agent reads,
from the JSON body CreateEdge returned.

It exists because the two representations differ in one place that a person
should not be doing by hand: trust_anchors is a bytes field, which JSON
carries as base64 and prototext carries as an escaped byte string. Getting
that wrong produces an anchor of the right length and the wrong value, and
the first thing that notices is an edge refusing central's certificate with
a message about the chain rather than about this file.

    write-provisioning.py <created.json> <central-url>

The output holds a live setup key. Redirect it somewhere with the
permissions a secret gets, and delete it once the edge has enrolled.
"""

import argparse
import base64
import binascii
import json
import sys
from pathlib import Path


class ProvisioningError(Exception):
    """A failure the operator can read and act on, without a traceback."""


def quoted(label: str, value: str) -> str:
    """Return value for printing between double quotes, as the output does.

    Neither character is escaped on output, so a value holding one would
    print a line prototext reads differently from what was given.
    """
    if '"' in value or "\\" in value:
        raise ProvisioningError(f"the {label} holds a double quote or a backslash")
    return value


def render_anchor(anchor: str) -> str:
    """Base64 out of JSON, then one \\xNN escape per byte, which is what
    prototext reads back as the same bytes."""
    try:
        raw = base64.b64decode(anchor, validate=True)
    except (binascii.Error, ValueError) as error:
        raise ProvisioningError(f"a trust anchor is not base64: {error}") from error
    if not raw:
        raise ProvisioningError("a trust anchor decodes to no bytes")
    return "".join(f"\\x{byte:02x}" for byte in raw)


def render(created: Path, central: str) -> str:
    try:
        body = json.loads(created.read_text(encoding="utf-8"))
    except (OSError, ValueError) as error:
        raise ProvisioningError(f"could not read {created}: {error}") from error

    provisioning = body.get("provisioning") if isinstance(body, dict) else None
    if not isinstance(provisioning, dict):
        provisioning = {}

    setup_key = provisioning.get("setupKey")
    if not isinstance(setup_key, str) or not setup_key:
        raise ProvisioningError(f"no setup key in {created}")

    anchors = provisioning.get("trustAnchors")
    if not isinstance(anchors, list) or not anchors:
        raise ProvisioningError(f"no trust anchors in {created}")
    if not all(isinstance(anchor, str) for anchor in anchors):
        raise ProvisioningError(f"could not read trust anchors from {created}")

    # Render every anchor before printing anything, because the output holds a
    # live setup key and a file that carries one with no anchors is worse than
    # no file at all: an edge reads it as "trust nothing", refuses central's
    # certificate, and cannot enroll, after the key has been consumed.
    rendered = [f'trust_anchors: "{render_anchor(anchor)}"\n' for anchor in anchors if anchor]
    if not rendered:
        raise ProvisioningError(f"no trust anchor could be decoded from {created}")

    return (
        f'central_url: "{quoted("central URL", central)}"\n'
        f'setup_key: "{quoted("setup key", setup_key)}"\n' + "".join(rendered)
    )


def main() -> int:
    parser = argparse.ArgumentParser(description="Render the EdgeProvisioning an agent reads.")
    parser.add_argument("created", type=Path, help="the JSON body CreateEdge returned")
    parser.add_argument("central_url", help="the URL the agent enrolls against")
    args = parser.parse_args()

    try:
        text = render(args.created, args.central_url)
    except ProvisioningError as error:
        print(f"write-provisioning: {error}", file=sys.stderr)
        return 1

    sys.stdout.buffer.write(text.encode("utf-8"))
    sys.stdout.buffer.flush()
    return 0


if __name__ == "__main__":
    sys.exit(main())
