# Cellular Primitives

The `flowseer.net.cellular.v1` package defines the cellular facts of a WAN
interface: `CellularInterface` with its radio access technology, band,
modem and SIM identifiers, serving cell, and `CellularSignal`.

## Boundaries

Imports: net/key

Imported by: nothing

Deliberately absent:

- MSISDN, APN and bearer settings, GPS, and SMS.
- NR non-standalone as a technology of its own; no targeted source
  distinguishes it.
- ARFCN, and the modem as a `Component` kind.
- WAN path quality. Latency, jitter, and loss are a property of the uplink,
  not of the radio, and stay `measure.v1.PathQuality`.
- Network instance key. The row is keyed by an interface and inherits it.

## Contents

- `radio_access_technology.proto`: `RadioAccessTechnology`, the generation
  in use, with the fold of each source's values.
- `cellular_signal.proto`: `CellularSignal`, RSRP, RSRQ, RSSI, and SINR.
- `cellular_interface.proto`: `CellularInterface`, the per-interface row.

The row is per interface rather than a component because every cellular
source presents the modem as an interface: IOS-XE keys every cellular table
by `cellular-interface`, Meraki reports cellular as an uplink interface, and
HH3C indexes by wireless card, which a mapper resolves to its interface. An
802.11 radio is the opposite case and stays a component.

`CellularSignal` shares its units with `net/wlan`, milli-dBm for absolute
power and milli-dB for ratios, but not a message. The two field sets overlap
in RSSI alone, and `net/measure` holds no dBm or dB type to share.

## Signal bounds

The bounds are the union of the LTE and NR measurement report mapping ranges,
in milli-units. Secondary source, verify against TS 36.133 / TS 38.133: the
3GPP archive serves no extractable table, so these were read from secondary
sources quoting the specifications, which disagree on the NR RSRP clause
number (10.1.6.1 versus 10.1.2).

| Field | Bound | Source |
| --- | --- | --- |
| `rsrp_millidbm` | `-156000..-30000` | TS 36.133 §9.1.4 (LTE, −156 to −44 dBm extended) and TS 38.133 Table 10.1.6.1-1 (NR SS-RSRP, −156 to −30 dBm), 1 dB steps |
| `rsrq_millidb` | `-43000..20000` | TS 36.133 §9.1.7 (LTE, −34 to +2.5 dB, 0.5 dB steps) and TS 38.133 (NR SS-RSRQ, −43 to +20 dB, 0.5 dB steps) |
| `sinr_millidb` | `-23000..40000` | TS 38.133 §10.1.16 (NR SS-SINR, −23 to +40 dB, 0.5 dB steps); the LTE RS-SINR range was not found and is assumed no wider |
| `rssi_millidbm` | none | no report mapping for RSSI in either clause; unbounded, as `net/wlan`'s `rssi_millidbm` is |

The report mapping saturates (reported value 0 means below −156 dBm), so a
mapper writes the endpoint for a saturated value.

## Identifiers

`imei` is 15 digits (TS 23.003 §6.2.1: an 8-digit TAC, a 6-digit serial
number, and a check or spare digit); the 16-digit IMEISV is out. `imsi` is 6
to 15 digits, and `mcc` 3 and `mnc` 2 or 3 digits (TS 23.003 §2.2). The MNC
is a string because `01` and `1` are different networks, a difference
IOS-XE's integer MNC loses. `iccid` is 1 to 22 digits, a deliberately loose
ceiling not verified against ITU-T E.118 so that no real card fails; a mapper
strips BCD `F` padding. The TS 23.003 clauses were read through search
results quoting the specification.

IMSI and ICCID identify a subscription, and through it a person. The privacy
rules of the [observability conventions](../../../../../../docs/conventions/observability.md)
apply wherever they are logged.

## Sources

- IOS-XE `Cisco-IOS-XE-cellwan-oper.yang` (`spec/yang/cisco/iosxe/2611/`) for
  radio technology, band, identifiers, serving cell, and signal.
- HH3C `HH3C-3GMODEM-MIB` (`spec/mib/hp/hh3c/`) for the current connection
  technology.
- Meraki uplink status, as dossier 07 records it
  ([07 QoS, Security, Operations, and WAN](../../../../../../docs/research/schema-building-blocks/07-qos-security-ops-wan.md)).
- 3GPP TS 23.003 for IMEI, IMSI, MCC, and MNC formats; TS 36.133 and
  TS 38.133 for the LTE and NR measurement report mappings.
