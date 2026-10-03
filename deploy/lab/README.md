# Lab deployment files

The files needed to run a local lab deployment with OpenFGA and Dex. Placeholders appear in every position that takes an environment-specific value.

| File | What it is |
| --- | --- |
| `central.textproto` | `DeviceServiceConfig`: the device service deployment |
| `registry.textproto` | `DeviceRegistry`: the switch central serves and the policy it resolves |
| `agent.textproto` | `AgentConfig`: where edge agent state lives and which provisioning file it reads |
| `provisioning.textproto` | `EdgeProvisioning`: the credentials central issues to an edge |
| `compose.yaml` | Container definitions for Postgres, OpenFGA, and Dex |
| `dex/config.yaml` | Dex identity provider configuration |
| `write-lab-secrets.sh` | Generates TLS certificates, keys, and user credentials under `secrets/` |
| `write-openfga-store.sh` | Creates the OpenFGA store and writes the authorization model |

The provisioning file is the only file that holds an edge credential. Its placeholders fail validation deliberately so an agent given an unedited file refuses it at load rather than failing later during operations.

`registry.textproto` fails similarly when management address or `ssh_host_key_sha256` retain placeholder values.

## Lab run

1. Generate secrets and certificates:

```bash
./write-lab-secrets.sh
```

2. Start the local containers:

```bash
docker compose up -d
```

3. Create the OpenFGA store and authorization model:

```bash
./write-openfga-store.sh
```

4. Request an operator token by password grant with the cross-client audience scope:

```bash
curl -sS --cacert secrets/ca.crt \
  -d "grant_type=password" \
  -d "client_id=flowseer-lab" \
  -d "client_secret=$(cat secrets/dex_client.secret)" \
  -d "username=alice@flowseer.local" \
  -d "password=$(grep 'Dex User alice:' secrets/credentials.txt | cut -d: -f2 | tr -d ' ')" \
  -d "scope=openid groups audience:server:client_id:flowseer-device" \
  https://127.0.0.1:8445/dex/token | jq -r .id_token
```

5. Verify OpenFGA preshared key enforcement:

On the HTTP listener:
- A request with no `Authorization` header returns HTTP 401 with `bearer_token_missing`:

```bash
curl -i --cacert secrets/ca.crt https://127.0.0.1:8080/stores
```

- A request with a wrong preshared key returns HTTP 401 with `unauthenticated`:

```bash
curl -i --cacert secrets/ca.crt -H "Authorization: Bearer wrong-key" https://127.0.0.1:8080/stores
```

Over gRPC, calling `openfga.v1.OpenFGAService/ListStores` without `authorization` metadata fails with code 1010, and calling with a wrong key fails with code 1500.

## User groups and key rotation

A user leaves a group by editing `dex/config.yaml` and restarting the container. Dex stores state in memory, so each restart generates new signing keys and invalidates previously issued tokens.

## Testing

The issuer integration suite in [`lab_issuer_test.go`](../../src/services/device/test/integration/lab_issuer_test.go) runs against a containerized Dex instance:

```bash
DOCKER_HOST="unix:///Users/aledante/.colima/default/docker.sock" TMPDIR="$HOME/tmp" TESTCONTAINERS_RYUK_DISABLED=true go test -v -tags=authz_integration -run TestLabDexIssuer ./src/services/device/test/integration/
```
