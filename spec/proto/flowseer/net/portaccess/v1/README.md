# Port Access Primitives

The `flowseer.net.portaccess.v1` package defines port-access sessions populated
by 802.1X, MAC authentication bypass (MAB), and web authentication for
FlowSeer-owned schemas.

## Boundaries

Imports: net/addr, net/key, net/switching

Imported by: nothing

Deliberately absent:

- Protocol-specific state machines (e.g. 802.1X PAE timers or EAP state).
- Interface-level port control configuration (`dot1xAuthAuthControlledPortControl`).
- RADIUS server configuration (modeled under `net/aaa/v1`).
- Network instance key: sessions are scoped by interface name, so network instance
  is inherited and omitted per Rule 4.

## Contents

- `port_access_method.proto` — `PortAccessMethod` authentication method enum (`DOT1X`, `MAB`, `WEB_AUTH`).
- `port_access_auth_state.proto` — `PortAccessAuthState` authorization state enum (`AUTHORIZED`, `UNAUTHORIZED`).
- `session.proto` — `Session` port-access session table row keyed by interface name and MAC address.

## Sources

The package's field and enum contracts cite:

- [IEEE Std 802.1X-2004](https://standards.ieee.org/ieee/802.1X/3392/) for port-based network access control.
- [IEEE8021-PAE-MIB](../../../../../../spec/mib/ieee/IEEE8021-PAE-MIB) for port access entity objects.
- Aruba CX port-access client architecture (`arubaWiredPortAccessClientTable`) for multi-supplicant session models.
- FlowSeer research dossiers:
  - [05 L2 and Instances](../../../../../../docs/research/schema-building-blocks/05-l2-and-instances.md)
  - [07 QoS, Security, Operations, and WAN](../../../../../../docs/research/schema-building-blocks/07-qos-security-ops-wan.md)
