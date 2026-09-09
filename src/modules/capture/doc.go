// Package capture turns a capture source, filter, and budget into a stream
// of packet batches and a pcapng artifact. It opens a capture source
// (src/modules/capture/rawsocket), pushes a compiled filter
// (src/modules/capture/filter) into the kernel or golang.org/x/net/bpf's own
// VM, decapsulates mirrored traffic (src/modules/capture/mirror), stops at
// its budget, and accounts for every packet it did not deliver. Rendering
// the accepted records as pcapng is src/modules/capture/pcapng's job, driven
// by whatever drains the pump this package returns.
//
// The module is a library at this stage: no service.Module wiring, gates,
// or config messages. Those land with the host that assembles it.
package capture
