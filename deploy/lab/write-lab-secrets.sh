#!/usr/bin/env bash
set -euo pipefail

# Every file below is created owner-only, so none is readable between its
# creation and the chmod that follows.
umask 077

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
SECRETS_DIR="${SCRIPT_DIR}/secrets"

for tool in openssl htpasswd; do
  if ! command -v "${tool}" >/dev/null 2>&1; then
    echo "${tool} is required" >&2
    exit 1
  fi
done

if [ -e "${SECRETS_DIR}" ]; then
  echo "Secrets directory already exists: ${SECRETS_DIR}" >&2
  echo "Refusing to overwrite existing secrets." >&2
  exit 1
fi

mkdir "${SECRETS_DIR}"

# A failed run leaves nothing behind, so the rerun is not stopped by the
# refusal above.
cleanup() {
  local status=$?
  if [ "${status}" -ne 0 ]; then
    rm -rf "${SECRETS_DIR}"
  fi
}
trap cleanup EXIT

# quiet runs a command and prints its output only when it fails.
quiet() {
  local out
  if ! out=$("$@" 2>&1); then
    echo "Failed: $*" >&2
    echo "${out}" >&2
    return 1
  fi
}

# 1. Certificate Authority
quiet openssl req -x509 -newkey rsa:4096 -sha256 -days 365 -nodes \
  -keyout "${SECRETS_DIR}/ca.key" \
  -out "${SECRETS_DIR}/ca.crt" \
  -subj "/CN=FlowSeer Lab CA"

# 2. Server Certificate for localhost and 127.0.0.1
quiet openssl req -newkey rsa:2048 -nodes \
  -keyout "${SECRETS_DIR}/server.key" \
  -out "${SECRETS_DIR}/server.csr" \
  -subj "/CN=localhost"

quiet openssl x509 -req \
  -in "${SECRETS_DIR}/server.csr" \
  -CA "${SECRETS_DIR}/ca.crt" \
  -CAkey "${SECRETS_DIR}/ca.key" \
  -CAcreateserial \
  -out "${SECRETS_DIR}/server.crt" \
  -days 365 -sha256 \
  -extfile <(printf "subjectAltName=DNS:localhost,IP:127.0.0.1\nextendedKeyUsage=serverAuth,clientAuth")

rm -f "${SECRETS_DIR}/server.csr" "${SECRETS_DIR}/ca.srl"

# 3. OpenFGA preshared key
OPENFGA_PSK=$(openssl rand -hex 32)
printf "%s\n" "${OPENFGA_PSK}" > "${SECRETS_DIR}/openfga.key"

# 4. Dex client secret
DEX_CLIENT_SECRET=$(openssl rand -hex 32)
printf "%s\n" "${DEX_CLIENT_SECRET}" > "${SECRETS_DIR}/dex_client.secret"

# 5. User passwords and bcrypt hashes
ALICE_PASSWORD=$(openssl rand -hex 16)
ADMIN_PASSWORD=$(openssl rand -hex 16)

# The password goes in on stdin, which keeps it out of the process list. Dex
# refuses a bcrypt cost below 10 and htpasswd defaults to 5.
hash_password() {
  local out
  out=$(printf "%s" "$1" | htpasswd -niB -C 10 dummy) || return 1
  printf "%s\n" "${out#dummy:}"
}

ALICE_HASH=$(hash_password "${ALICE_PASSWORD}")
ADMIN_HASH=$(hash_password "${ADMIN_PASSWORD}")

# 6. Environment files
cat << EOF > "${SECRETS_DIR}/openfga.env"
OPENFGA_AUTHN_PRESHARED_KEYS=${OPENFGA_PSK}
EOF

# Compose substitutes $name in an unquoted env-file value, and a bcrypt hash
# holds $2y$05$<salt>..., so Dex would receive a truncated hash. Single
# quotes keep every value literal.
cat << EOF > "${SECRETS_DIR}/dex.env"
DEX_LAB_CLIENT_SECRET='${DEX_CLIENT_SECRET}'
DEX_USER_ALICE_HASH='${ALICE_HASH}'
DEX_USER_ADMIN_HASH='${ADMIN_HASH}'
EOF

cat << EOF > "${SECRETS_DIR}/credentials.txt"
OpenFGA Preshared Key: ${OPENFGA_PSK}
Dex Client Secret (flowseer-lab): ${DEX_CLIENT_SECRET}
Dex User alice: ${ALICE_PASSWORD}
Dex User admin: ${ADMIN_PASSWORD}
EOF

echo "Lab secrets written to ${SECRETS_DIR}"
