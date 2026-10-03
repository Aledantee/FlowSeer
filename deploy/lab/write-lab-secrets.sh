#!/usr/bin/env bash
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
SECRETS_DIR="${SCRIPT_DIR}/secrets"

if [ -e "${SECRETS_DIR}" ]; then
  echo "Secrets directory already exists: ${SECRETS_DIR}" >&2
  echo "Refusing to overwrite existing secrets." >&2
  exit 1
fi

mkdir -p "${SECRETS_DIR}"

# 1. Certificate Authority
openssl req -x509 -newkey rsa:4096 -sha256 -days 365 -nodes \
  -keyout "${SECRETS_DIR}/ca.key" \
  -out "${SECRETS_DIR}/ca.crt" \
  -subj "/CN=FlowSeer Lab CA" \
  2>/dev/null

# 2. Server Certificate for localhost and 127.0.0.1
openssl req -newkey rsa:2048 -nodes \
  -keyout "${SECRETS_DIR}/server.key" \
  -out "${SECRETS_DIR}/server.csr" \
  -subj "/CN=localhost" \
  2>/dev/null

openssl x509 -req \
  -in "${SECRETS_DIR}/server.csr" \
  -CA "${SECRETS_DIR}/ca.crt" \
  -CAkey "${SECRETS_DIR}/ca.key" \
  -CAcreateserial \
  -out "${SECRETS_DIR}/server.crt" \
  -days 365 -sha256 \
  -extfile <(printf "subjectAltName=DNS:localhost,IP:127.0.0.1\nextendedKeyUsage=serverAuth,clientAuth") \
  2>/dev/null

rm -f "${SECRETS_DIR}/server.csr" "${SECRETS_DIR}/ca.srl"

# 3. OpenFGA preshared key (0600)
OPENFGA_PSK=$(openssl rand -hex 32)
printf "%s\n" "${OPENFGA_PSK}" > "${SECRETS_DIR}/openfga.key"
chmod 0600 "${SECRETS_DIR}/openfga.key"

# 4. Dex client secret
DEX_CLIENT_SECRET=$(openssl rand -hex 32)

# 5. User passwords and bcrypt hashes
ALICE_PASSWORD=$(openssl rand -hex 16)
ADMIN_PASSWORD=$(openssl rand -hex 16)

hash_password() {
  local pass="$1"
  htpasswd -nb -B dummy "${pass}" | cut -d: -f2
}

ALICE_HASH=$(hash_password "${ALICE_PASSWORD}")
ADMIN_HASH=$(hash_password "${ADMIN_PASSWORD}")

# 6. Environment files
cat << EOF > "${SECRETS_DIR}/openfga.env"
OPENFGA_AUTHN_PRESHARED_KEYS=${OPENFGA_PSK}
EOF

cat << EOF > "${SECRETS_DIR}/dex.env"
DEX_LAB_CLIENT_SECRET=${DEX_CLIENT_SECRET}
DEX_USER_ALICE_HASH=${ALICE_HASH}
DEX_USER_ADMIN_HASH=${ADMIN_HASH}
EOF

cat << EOF > "${SECRETS_DIR}/credentials.txt"
OpenFGA Preshared Key: ${OPENFGA_PSK}
Dex Client Secret (flowseer-lab): ${DEX_CLIENT_SECRET}
Dex User alice: ${ALICE_PASSWORD}
Dex User admin: ${ADMIN_PASSWORD}
EOF

chmod 0600 "${SECRETS_DIR}/openfga.env" "${SECRETS_DIR}/dex.env" "${SECRETS_DIR}/credentials.txt"

echo "Lab secrets written to ${SECRETS_DIR}"
