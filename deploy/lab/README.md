# Lab deployment files

The files one lab run needs: the device service, its registry and edge agent, and a local OpenFGA and Dex for operator authorization. Placeholders appear in every position that takes an environment-specific value.

| File | What it is |
| --- | --- |
| `central.textproto` | `DeviceServiceConfig`: the device service deployment |
| `registry.textproto` | `DeviceRegistry`: the switch central serves and the policy it resolves |
| `agent.textproto` | `AgentConfig`: where edge agent state lives and which provisioning file it reads |
| `provisioning.textproto` | `EdgeProvisioning`: the credentials central issues to an edge |
| `compose.yaml` | Container definitions for Postgres, OpenFGA, and Dex |
| `dex/config.yaml` | Dex identity provider configuration |
| `write-lab-secrets.sh` | Generates TLS certificates, keys, and user credentials under `secrets/` |
| `write-openfga-store.sh` | Creates the OpenFGA store, writes the authorization model, and prints the `authorization` block |

The provisioning file is the only file that holds an edge credential. Its placeholders fail validation on purpose, so an agent given an unedited file refuses it at load and names the field, instead of failing later somewhere less obvious.

`registry.textproto` fails the same way for the two positions that describe a device rather than the deployment: the management address and `ssh_host_key_sha256`. `write-registry.sh` refuses to render the shipped file while either is still a placeholder.

Read [the runbook](../../docs/runbooks/lab-icx7150-first-write.md) before using any of this against a switch. Two values cannot be copied from a document because they are measurements: the delayed-apply horizon and the switch's SSH host key.

## Lab run

Run every command from this directory. Compose publishes each port on `127.0.0.1` only. The commands below assume `DOCKER_HOST` already points at your Docker daemon.

1. Generate secrets and certificates. The script refuses to overwrite an existing `secrets/` directory, which `.gitignore` keeps out of the repository:

```bash
./write-lab-secrets.sh
```

`credentials.txt` holds the preshared key, the client secret, and one password per lab user.

2. Start the local containers:

```bash
docker compose up -d --wait
```

3. Create the OpenFGA store and write the authorization model:

```bash
./write-openfga-store.sh
```

The script prints an `authorization` block. Paste it over the `authorization` section of `central.textproto`, which replaces the placeholder ids and the key path. The block's `ca_file` is the lab CA. The `authentication` section needs the same file as its own `ca_file`, because Dex serves a certificate the system trust store does not know.

4. Request an operator token for each lab user by password grant, with the cross-client audience scope. `alice` belongs to the groups `acme` and `globex`, and `admin` to `flowseer-platform`:

```bash
lab_token() {
  curl -sS --cacert secrets/ca.crt \
    -d "grant_type=password" \
    -d "client_id=flowseer-lab" \
    -d "client_secret=$(cat secrets/dex_client.secret)" \
    -d "username=$1@flowseer.local" \
    -d "password=$(grep "Dex User $1:" secrets/credentials.txt | cut -d: -f2 | tr -d ' ')" \
    -d "scope=openid groups audience:server:client_id:flowseer-device" \
    https://127.0.0.1:8445/dex/token | jq -r .id_token
}
ALICE_TOKEN=$(lab_token alice)
ADMIN_TOKEN=$(lab_token admin)
```

Read the claims the device service will see. The `sub` of the admin token is the value `platform_admin.subject` takes, because Dex encodes the user and connector in it:

```bash
echo "${ADMIN_TOKEN}" | jq -R 'split(".")[1] | gsub("-";"+") | gsub("_";"/") | @base64d | fromjson | {iss, sub, aud, groups}'
```

5. Write authorization tuples for the lab user in OpenFGA. Until `TenantService` lands in phase 4, a deployment writes `enrolled` and role tuples directly to OpenFGA. Compute the principal identifier from the token issuer and subject:

```bash
ALICE_SUB=$(echo "${ALICE_TOKEN}" | jq -R 'split(".")[1] | gsub("-";"+") | gsub("_";"/") | @base64d | fromjson | .sub')
ALICE_ID=$(printf '%s\0%s' "https://127.0.0.1:8445/dex" "${ALICE_SUB}" | shasum -a 256 | awk '{print $1}')
STORE_ID=$(grep -E '^\s*store_id:' central.textproto | awk '{print $2}' | tr -d '"')

curl -sS --cacert secrets/ca.crt \
  -H "Authorization: Bearer $(cat secrets/openfga.key)" \
  -H "Content-Type: application/json" \
  -X POST "https://127.0.0.1:8080/stores/${STORE_ID}/write" \
  -d '{
    "writes": {
      "tuple_keys": [
        {"user": "user:'"${ALICE_ID}"'", "relation": "enrolled", "object": "tenant:default"},
        {"user": "user:'"${ALICE_ID}"'", "relation": "admin", "object": "tenant:default"}
      ]
    }
  }'
```

6. Call an operator procedure. Requesting `GetEdge` with the bearer token and tenant header:

```bash
buf curl --cacert secrets/ca.crt \
  -H "Authorization: Bearer ${ALICE_TOKEN}" \
  -H "X-FlowSeer-Tenant: default" \
  -d '{"edge":{"edge":{"id":"0192e6a0-0000-7000-8000-000000000001"}}}' \
  https://127.0.0.1:8443/flowseer.api.edge.v1.EdgeAdminService/GetEdge
```

The same call without the header answers `InvalidArgument`:

```bash
buf curl --cacert secrets/ca.crt \
  -H "Authorization: Bearer ${ALICE_TOKEN}" \
  -d '{"edge":{"edge":{"id":"0192e6a0-0000-7000-8000-000000000001"}}}' \
  https://127.0.0.1:8443/flowseer.api.edge.v1.EdgeAdminService/GetEdge
```

7. Verify OpenFGA preshared key enforcement on both listeners.

On the HTTP listener, a request with no `Authorization` header returns HTTP 401 with `bearer_token_missing`:

```bash
curl -i --cacert secrets/ca.crt https://127.0.0.1:8080/stores
```

A request with a wrong preshared key returns HTTP 401 with `unauthenticated`:

```bash
curl -i --cacert secrets/ca.crt -H "Authorization: Bearer wrong-key" https://127.0.0.1:8080/stores
```

Over gRPC, `ListStores` without `authorization` metadata fails with status 1010:

```bash
printf '\0\0\0\0\0' | curl -sS -i --http2 --cacert secrets/ca.crt \
  -H 'content-type: application/grpc' --data-binary @- \
  https://127.0.0.1:8081/openfga.v1.OpenFGAService/ListStores | grep -a -i '^grpc-status'
```

With a wrong key it fails with status 1500:

```bash
printf '\0\0\0\0\0' | curl -sS -i --http2 --cacert secrets/ca.crt \
  -H 'content-type: application/grpc' -H 'authorization: Bearer wrong-key' --data-binary @- \
  https://127.0.0.1:8081/openfga.v1.OpenFGAService/ListStores | grep -a -i '^grpc-status'
```

8. Stop the containers and drop their state:

```bash
docker compose down -v
```

## User groups and key rotation

A user leaves a group by editing `dex/config.yaml` and restarting the container. Dex stores state in memory, so each restart generates new signing keys and invalidates previously issued tokens.

## Secret files

`write-lab-secrets.sh` creates every file owner-only. Compose bind-mounts `server.key` into the OpenFGA and Dex containers, which run as non-root users, and both read it on the Colima setup the tier tests use. Another Docker host may map file ownership differently. If a container cannot read the key, loosen the mode of `secrets/server.key` there only.

## Testing

The issuer integration suite in [`lab_issuer_test.go`](../../src/services/device/test/integration/lab_issuer_test.go) runs against a containerized Dex instance. Run the whole tier as the [integration README](../../src/services/device/test/integration/README.md) gives it:

```bash
DOCKER_HOST="unix://$HOME/.colima/default/docker.sock" \
TMPDIR="$HOME/tmp" \
TESTCONTAINERS_RYUK_DISABLED=true \
go test -v -race -tags=authz_integration ./src/services/device/test/integration/
```

The tier does not run these scripts or the README steps above. [`lab_fixtures_test.go`](../../src/services/device/test/integration/lab_fixtures_test.go) holds the script text, the compose file, and the README's file names to the properties that broke a real run.
