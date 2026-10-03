#!/usr/bin/env bash
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
MODEL_FILE="${SCRIPT_DIR}/../../src/services/device/internal/authz/openfga/model.json"
KEY_FILE="${SCRIPT_DIR}/secrets/openfga.key"
CA_FILE="${SCRIPT_DIR}/secrets/ca.crt"

OPENFGA_HTTP_ENDPOINT="${OPENFGA_HTTP_ENDPOINT:-https://127.0.0.1:8080}"
OPENFGA_GRPC_ENDPOINT="${OPENFGA_GRPC_ENDPOINT:-https://127.0.0.1:8081}"

if ! command -v curl >/dev/null 2>&1; then
  echo "curl is required" >&2
  exit 1
fi

if ! command -v jq >/dev/null 2>&1; then
  echo "jq is required" >&2
  exit 1
fi

if [ ! -f "${MODEL_FILE}" ]; then
  echo "Model file not found: ${MODEL_FILE}" >&2
  exit 1
fi

if [ ! -f "${KEY_FILE}" ]; then
  echo "Preshared key file not found: ${KEY_FILE}" >&2
  exit 1
fi

if [ ! -f "${CA_FILE}" ]; then
  echo "CA certificate file not found: ${CA_FILE}" >&2
  exit 1
fi

PSK=$(tr -d '\r\n' < "${KEY_FILE}")

# 1. Create Store
CREATE_RESP=$(curl -sS --cacert "${CA_FILE}" \
  -H "Authorization: Bearer ${PSK}" \
  -H "Content-Type: application/json" \
  -X POST "${OPENFGA_HTTP_ENDPOINT}/stores" \
  -d '{"name":"flowseer-lab"}')

STORE_ID=$(echo "${CREATE_RESP}" | jq -r '.id // empty')
if [ -z "${STORE_ID}" ]; then
  echo "Failed to create OpenFGA store: ${CREATE_RESP}" >&2
  exit 1
fi

# 2. Write Authorization Model
MODEL_RESP=$(curl -sS --cacert "${CA_FILE}" \
  -H "Authorization: Bearer ${PSK}" \
  -H "Content-Type: application/json" \
  -X POST "${OPENFGA_HTTP_ENDPOINT}/stores/${STORE_ID}/authorization-models" \
  --data-binary @"${MODEL_FILE}")

MODEL_ID=$(echo "${MODEL_RESP}" | jq -r '.authorization_model_id // empty')
if [ -z "${MODEL_ID}" ]; then
  echo "Failed to write authorization model: ${MODEL_RESP}" >&2
  exit 1
fi

# 3. Print authorization block
cat << EOF
authorization {
  endpoint: "${OPENFGA_GRPC_ENDPOINT}"
  store_id: "${STORE_ID}"
  model_id: "${MODEL_ID}"
  preshared_key_file: "${KEY_FILE}"
}
EOF
