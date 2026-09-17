# Credential material

`flowseer.model.credential.v1` holds `CredentialMaterial`, the shape a
device credential takes when central hands it to an edge: an SNMPv3 user
with its authentication and privacy settings, or a shell login.

## Boundaries

Imports: nothing FlowSeer-owned

Imported by: edge/attach

Deliberately absent:

- authNoPriv, noAuthNoPriv, and SNMPv2c. See "Only authPriv" below.
- MD5, DES, and 3DES. Legacy management protocols are off; the enums cannot
  express them, so a weak setting cannot be configured by accident.
- An SSH host key. Host trust is not a credential: the pin rides beside the
  material on the credential responses and is versioned by `HostTrustHandle`.
- A triad and a ref pair. Material is resolved from a handle, never
  configured or observed as an entity.

`edge/attach` carries this material on its credential responses without a
cycle, the same way it carries the handles from `model/policy`. A handle in
`model/policy` names a credential version; this package is what that version
resolves to. The two stay apart because the handle travels on records that
must never contain a secret, and the material travels once, on an
authenticated call, to the process that opens the session.

```prototext
snmp_v3 {
  user: "flowseer-ro"
  auth_protocol: SNMP_AUTH_PROTOCOL_SHA256
  auth_passphrase: "correct horse battery"
  priv_protocol: SNMP_PRIV_PROTOCOL_AES128
  priv_passphrase: "staple lamp cinder"
}
```

The same message is the on-disk format of the device service's mounted
credential files: one file per credential version, holding the whole
material as prototext, so a rotation changes one file and nothing has to
agree with it elsewhere.

## Only authPriv, and what that costs

Every field of `SnmpV3Credential` is required, so the only SNMPv3 security
level this system can express is authPriv. There is no authNoPriv and no
noAuthNoPriv, and no v2c community anywhere in the package.

The reason is that an edge acquires a credential per operation rather than
holding a lease, which bounds how long a secret is useful — and that buys
nothing if the secret crosses the wire in the clear, as a v2c community does
on every read and an authNoPriv passphrase does not but its payload does.
Making it unrepresentable rather than discouraged means no deployment reaches
it by configuration.

The cost lands on whoever meets it first, and it does not look like this from
there. A device configured authNoPriv, or with a privacy algorithm outside
AES-128/192/256, is refused by the edge with "credential names no SNMPv3
privacy protocol" — which reads as central having failed to populate a field.
An operator will go looking at credential delivery, find it correct, and have
no way from there to the real answer: the device's own security level is not
one this system manages at. Check the device before provisioning it, not
after.
