# Credential material

The `flowseer.device.credential.v1` package holds `CredentialMaterial`, the
shape a device credential takes when central hands it to an edge:
an SNMPv3 user with its authentication and privacy settings, or a shell
login. It imports nothing FlowSeer-owned, so `api/edge` can carry it on its
credential responses without a cycle, the same way it carries the handles
from `device/policy`.

A handle in `device/policy` names a credential version; this package is what
that version resolves to. The two stay apart because the handle travels on
records that must never contain a secret, and the material travels once, on
an authenticated call, to the process that opens the session.

```prototext
snmp_v3 {
  user: "flowseer-ro"
  auth_protocol: SNMP_AUTH_PROTOCOL_SHA256
  auth_passphrase: "..."
  priv_protocol: SNMP_PRIV_PROTOCOL_AES128
  priv_passphrase: "..."
}
```

The same message is the on-disk format of the device service's mounted
credential files: one file per credential version, holding the whole
material as prototext, so a rotation changes one file and nothing has to
agree with it elsewhere.

## What is deliberately absent

- MD5, DES, and 3DES. Legacy management protocols are off; the enums cannot
  express them, so a weak setting cannot be configured by accident.
- An SSH host key. Host trust is not a credential: the pin rides beside the
  material on the credential responses and is versioned by `HostTrustHandle`.
- A triad and a ref pair. Material is resolved from a handle, never
  configured or observed as an entity.
