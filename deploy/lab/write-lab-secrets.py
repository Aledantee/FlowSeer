# /// script
# requires-python = "==3.13.*"
# dependencies = ["cryptography==50.0.1", "bcrypt==5.0.0"]
#
# [tool.uv]
# exclude-newer = "2026-09-22T00:00:00Z"
# ///
"""Write the lab's secrets under secrets/ beside this file.

Creates a CA and a server certificate for localhost and 127.0.0.1, the
OpenFGA preshared key, the Dex client secret, and a password with its bcrypt
hash for each lab user. The script refuses to overwrite an existing secrets/
directory, and a failed run removes the directory it made, so a rerun is not
stopped by that refusal.

Start it with `uv run --locked write-lab-secrets.py`. The lock file beside it
pins the two packages and their dependencies by digest, and --locked makes uv
fail on a lock that no longer matches the block above.

The certificates carry the extensions a strict verifier asks for, since Python
3.13 sets ssl.VERIFY_X509_STRICT by default: the CA has key usage and a subject
key identifier, and the server certificate has an authority key identifier,
key usage, and the names it is verified for.
"""

import datetime
import ipaddress
import os
import secrets
import shutil
import sys
from pathlib import Path

import bcrypt
from cryptography import x509
from cryptography.hazmat.primitives import hashes, serialization
from cryptography.hazmat.primitives.asymmetric import rsa
from cryptography.x509.oid import ExtendedKeyUsageOID, NameOID

VALID_DAYS = 365
# Dex refuses a bcrypt cost below 10 at login.
BCRYPT_COST = 10
USERS = ("alice", "admin")


class SecretsError(Exception):
    """A failure the operator can read and act on, without a traceback."""


def write_file(directory: Path, name: str, data: str | bytes) -> None:
    """Create directory/name owner-only, and fail if it already exists."""
    raw = data.encode("utf-8") if isinstance(data, str) else data
    fd = os.open(directory / name, os.O_WRONLY | os.O_CREAT | os.O_EXCL, 0o600)
    with os.fdopen(fd, "wb") as handle:
        handle.write(raw)


def key_pem(key: rsa.RSAPrivateKey) -> bytes:
    return key.private_bytes(
        serialization.Encoding.PEM,
        serialization.PrivateFormat.PKCS8,
        serialization.NoEncryption(),
    )


def cert_pem(cert: x509.Certificate) -> bytes:
    return cert.public_bytes(serialization.Encoding.PEM)


def name_of(common_name: str) -> x509.Name:
    return x509.Name([x509.NameAttribute(NameOID.COMMON_NAME, common_name)])


def make_ca(key: rsa.RSAPrivateKey, start: datetime.datetime) -> x509.Certificate:
    subject = name_of("FlowSeer Lab CA")
    public = key.public_key()
    ski = x509.SubjectKeyIdentifier.from_public_key(public)
    return (
        x509.CertificateBuilder()
        .subject_name(subject)
        .issuer_name(subject)
        .public_key(public)
        .serial_number(x509.random_serial_number())
        .not_valid_before(start)
        .not_valid_after(start + datetime.timedelta(days=VALID_DAYS))
        .add_extension(x509.BasicConstraints(ca=True, path_length=None), critical=True)
        .add_extension(
            x509.KeyUsage(
                digital_signature=False,
                content_commitment=False,
                key_encipherment=False,
                data_encipherment=False,
                key_agreement=False,
                key_cert_sign=True,
                crl_sign=True,
                encipher_only=False,
                decipher_only=False,
            ),
            critical=True,
        )
        .add_extension(ski, critical=False)
        .add_extension(x509.AuthorityKeyIdentifier.from_issuer_public_key(public), critical=False)
        .sign(key, hashes.SHA256())
    )


def make_server(
    key: rsa.RSAPrivateKey,
    ca: x509.Certificate,
    ca_key: rsa.RSAPrivateKey,
    start: datetime.datetime,
) -> x509.Certificate:
    public = key.public_key()
    ca_ski = ca.extensions.get_extension_for_class(x509.SubjectKeyIdentifier).value
    return (
        x509.CertificateBuilder()
        .subject_name(name_of("localhost"))
        .issuer_name(ca.subject)
        .public_key(public)
        .serial_number(x509.random_serial_number())
        .not_valid_before(start)
        .not_valid_after(start + datetime.timedelta(days=VALID_DAYS))
        .add_extension(x509.BasicConstraints(ca=False, path_length=None), critical=True)
        .add_extension(
            x509.KeyUsage(
                digital_signature=True,
                content_commitment=False,
                key_encipherment=True,
                data_encipherment=False,
                key_agreement=False,
                key_cert_sign=False,
                crl_sign=False,
                encipher_only=False,
                decipher_only=False,
            ),
            critical=True,
        )
        .add_extension(
            x509.SubjectAlternativeName(
                [x509.DNSName("localhost"), x509.IPAddress(ipaddress.ip_address("127.0.0.1"))]
            ),
            critical=False,
        )
        .add_extension(
            x509.ExtendedKeyUsage([ExtendedKeyUsageOID.SERVER_AUTH, ExtendedKeyUsageOID.CLIENT_AUTH]),
            critical=False,
        )
        .add_extension(x509.SubjectKeyIdentifier.from_public_key(public), critical=False)
        .add_extension(x509.AuthorityKeyIdentifier.from_issuer_subject_key_identifier(ca_ski), critical=False)
        .sign(ca_key, hashes.SHA256())
    )


def write_secrets(directory: Path) -> None:
    start = datetime.datetime.now(datetime.timezone.utc).replace(microsecond=0)

    ca_key = rsa.generate_private_key(public_exponent=65537, key_size=4096)
    ca = make_ca(ca_key, start)
    server_key = rsa.generate_private_key(public_exponent=65537, key_size=2048)
    server = make_server(server_key, ca, ca_key, start)

    write_file(directory, "ca.key", key_pem(ca_key))
    write_file(directory, "ca.crt", cert_pem(ca))
    write_file(directory, "server.key", key_pem(server_key))
    write_file(directory, "server.crt", cert_pem(server))

    openfga_key = secrets.token_hex(32)
    write_file(directory, "openfga.key", openfga_key + "\n")

    client_secret = secrets.token_hex(32)
    write_file(directory, "dex_client.secret", client_secret + "\n")

    passwords = {user: secrets.token_hex(16) for user in USERS}
    hashes_by_user = {
        user: bcrypt.hashpw(password.encode("utf-8"), bcrypt.gensalt(rounds=BCRYPT_COST)).decode("ascii")
        for user, password in passwords.items()
    }

    write_file(directory, "openfga.env", f"OPENFGA_AUTHN_PRESHARED_KEYS={openfga_key}\n")

    # Compose substitutes $name in an unquoted env-file value, and a bcrypt
    # hash holds $2b$10$<salt>..., so Dex would receive a truncated hash.
    # Single quotes keep every value literal, which a value holding one cannot.
    dex_env = {"DEX_LAB_CLIENT_SECRET": client_secret}
    dex_env.update({f"DEX_USER_{user.upper()}_HASH": value for user, value in hashes_by_user.items()})
    for name, value in dex_env.items():
        if "'" in value:
            raise SecretsError(f"{name} holds a single quote, which dex.env cannot quote")
    write_file(directory, "dex.env", "".join(f"{name}='{value}'\n" for name, value in dex_env.items()))

    credentials = [
        f"OpenFGA Preshared Key: {openfga_key}",
        f"Dex Client Secret (flowseer-lab): {client_secret}",
        *(f"Dex User {user}: {password}" for user, password in passwords.items()),
    ]
    write_file(directory, "credentials.txt", "\n".join(credentials) + "\n")


def main() -> int:
    # Every file is created owner-only, so none is readable between its
    # creation and a later chmod.
    os.umask(0o077)

    directory = Path(__file__).resolve().parent / "secrets"
    if directory.exists():
        print(f"Secrets directory already exists: {directory}", file=sys.stderr)
        print("Refusing to overwrite existing secrets.", file=sys.stderr)
        return 1

    directory.mkdir()
    try:
        write_secrets(directory)
    except SecretsError as error:
        shutil.rmtree(directory, ignore_errors=True)
        print(f"write-lab-secrets.py: {error}", file=sys.stderr)
        return 1
    except BaseException:
        shutil.rmtree(directory, ignore_errors=True)
        raise

    print(f"Lab secrets written to {directory}")
    return 0


if __name__ == "__main__":
    sys.exit(main())
