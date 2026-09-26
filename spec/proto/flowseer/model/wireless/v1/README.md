# Wireless LAN

`flowseer.model.wireless.v1` holds the wireless LAN (`Wlan`) entity family: its
UUID-keyed ref pair, intended configuration, observed broadcast state, lifecycle
status transitions, and radio broadcast associations.

An SSID can be configured and broadcast by no access point; the triad separates
intended configuration (`WlanConfig`) from observed broadcast state (`WlanState`),
while `WlanBroadcast` links a network to the radio components and BSSIDs
currently beaconing it.

## Boundaries

Imports: model/inventory, net/addr, net/switching, net/wlan

Imported by: nothing

Deliberately absent:

- Admission to `EntityType`. `Wlan` stays out of `EntityType` until its store
  and service land.
- Station tracking and wireless client associations (modeled under `net/endpoint`).
