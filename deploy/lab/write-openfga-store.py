# /// script
# requires-python = "==3.13.*"
# dependencies = []
# ///
"""Create the lab's OpenFGA store, write the authorization model, and print
the `authorization` block that central's configuration takes.

Reads secrets/openfga.key and secrets/ca.crt beside this file, which
write-lab-secrets.py writes. The server is verified against that CA with the
default TLS context, so a certificate without the extensions a strict
verifier asks for is refused.

Env: OPENFGA_HTTP_ENDPOINT (default https://127.0.0.1:8080) and
OPENFGA_GRPC_ENDPOINT (default https://127.0.0.1:8081, printed in the block).
"""

import json
import os
import ssl
import sys
import urllib.error
import urllib.request
from pathlib import Path

SCRIPT_DIR = Path(__file__).resolve().parent
MODEL_FILE = SCRIPT_DIR / ".." / ".." / "src" / "services" / "device" / "internal" / "authz" / "openfga" / "model.json"
KEY_FILE = SCRIPT_DIR / "secrets" / "openfga.key"
CA_FILE = SCRIPT_DIR / "secrets" / "ca.crt"


class StoreError(Exception):
    """A failure the operator can read and act on, without a traceback."""


def post(context: ssl.SSLContext, psk: str, url: str, body: bytes) -> str:
    """POST body as JSON and return the response body, whatever the status.

    The key travels in a header of this process's own request, so it never
    appears in an argument list.
    """
    request = urllib.request.Request(
        url,
        data=body,
        method="POST",
        headers={"Authorization": f"Bearer {psk}", "Content-Type": "application/json"},
    )
    try:
        with urllib.request.urlopen(request, context=context) as response:
            return response.read().decode("utf-8", errors="replace")
    except urllib.error.HTTPError as error:
        # An error status carries a body that says what went wrong.
        with error:
            return error.read().decode("utf-8", errors="replace")
    except urllib.error.URLError as error:
        reason = error.reason
        if isinstance(reason, ssl.SSLCertVerificationError):
            raise StoreError(
                f"the server's certificate is refused: {reason.verify_message}\n"
                f"Delete secrets/ and run write-lab-secrets.py to create a new one."
            ) from error
        raise StoreError(f"{url}: {reason}") from error


def field(response: str, name: str) -> str:
    """Return the string a JSON response holds under name, or "" when absent."""
    try:
        parsed = json.loads(response)
    except json.JSONDecodeError:
        return ""
    value = parsed.get(name) if isinstance(parsed, dict) else None
    if value in (None, False, ""):
        return ""
    return str(value)


def run() -> int:
    http_endpoint = os.environ.get("OPENFGA_HTTP_ENDPOINT", "https://127.0.0.1:8080")
    grpc_endpoint = os.environ.get("OPENFGA_GRPC_ENDPOINT", "https://127.0.0.1:8081")

    if not MODEL_FILE.is_file():
        print(f"Model file not found: {MODEL_FILE}", file=sys.stderr)
        return 1
    if not KEY_FILE.is_file():
        print(f"Preshared key file not found: {KEY_FILE}", file=sys.stderr)
        return 1
    if not CA_FILE.is_file():
        print(f"CA certificate file not found: {CA_FILE}", file=sys.stderr)
        return 1

    psk = KEY_FILE.read_text(encoding="utf-8").replace("\r", "").replace("\n", "")
    context = ssl.create_default_context(cafile=str(CA_FILE))

    # 1. Create Store
    created = post(context, psk, f"{http_endpoint}/stores", b'{"name":"flowseer-lab"}')
    store_id = field(created, "id")
    if not store_id:
        print(f"Failed to create OpenFGA store: {created}", file=sys.stderr)
        return 1

    # 2. Write Authorization Model
    written = post(
        context,
        psk,
        f"{http_endpoint}/stores/{store_id}/authorization-models",
        MODEL_FILE.read_bytes(),
    )
    model_id = field(written, "authorization_model_id")
    if not model_id:
        print(f"Failed to write authorization model: {written}", file=sys.stderr)
        return 1

    # 3. Print authorization block
    print(
        "authorization {\n"
        f'  endpoint: "{grpc_endpoint}"\n'
        f'  store_id: "{store_id}"\n'
        f'  model_id: "{model_id}"\n'
        f'  preshared_key_file: "{KEY_FILE}"\n'
        f'  ca_file: "{CA_FILE}"\n'
        "}"
    )
    return 0


def main() -> int:
    try:
        return run()
    except StoreError as error:
        print(f"write-openfga-store.py: {error}", file=sys.stderr)
        return 1


if __name__ == "__main__":
    sys.exit(main())
