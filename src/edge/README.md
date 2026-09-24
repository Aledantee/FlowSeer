# edge

Applications built to run at the edge — on a laptop, a jump host, or an agent on
the customer network — as opposed to the control-plane services in
`src/services/`.

| Directory | What it is                                                |
| --------- | --------------------------------------------------------- |
| `agent`   | The device access agent: enrolls with central, holds its dispatch stream, drives the local-network access lane. `cmd/agent` is its binary |
| `netpen`  | L2/L3 security audit and attack tool, run by an operator   |
| `netsimload` | Finite offered-load transmitter and capture comparison tool. `cmd/netsimload` is its binary |

"Edge" names what an application is *designed for*, not only where it ends up
running. An agent that lives here because it can operate on a remote network may
also be deployed centrally, close to the control plane — that does not make it a
service. The dividing line is the role: a service is control-plane
infrastructure; an edge application is built to work at the edge and may be run
anywhere.

Shared behavior between an edge application and a service does not live in
either tree — it belongs in `src/modules/`, which both assemble from.

## Dependencies

`agent` is in the main module rather than its own, because everything it
carries — the access lane, the bus, the protocol libraries — the control plane
carries too. `netpen` is the one that is not.

Edge applications may carry dependencies the control plane must not. `netpen`
lives in its own Go module for exactly that reason: gopacket and the bubbletea
family stay out of the main module's graph, and
`src/common/internal/netpenguard` fails the build if they leak. A new edge
application with a heavy dependency family should do the same and extend that
guard's allowlist.

`netsimload` stays in the root module because it consumes the root-owned stream
and fabric values. Its packet sender uses `golang.org/x/sys/unix` directly and
does not import netpen's gopacket link. It needs `CAP_NET_RAW` (or equivalent
raw-socket permission) for both named interfaces. It never selects a default
interface, and the transmit and receive interfaces must be distinct and up.

The wire signature reserves the first 32 payload octets for `FSLD`, version 1,
flow ID, sequence, and submission timestamp. The application README documents
the byte layout and the distinction between userspace submission, capture, and
simulator timestamps. A physical-switch comparison is a separately approved
operation: it requires advance notice to power on the normally-off switch and
an owner-approved blast-radius statement before any traffic is sent.
