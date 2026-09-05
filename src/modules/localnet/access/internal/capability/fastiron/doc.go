// Package fastiron is the typed SSH adapter for one firmware: FastIron
// 10.0.10g on a Ruckus ICX switch. It carries no vendor-agnostic
// vocabulary of its own — commands.go builds the exact command lines and
// prompt patterns this firmware's CLI expects, parser.go reads its "show
// interfaces" output, and adapter.go drives src/protocol/ssh through them.
// A second firmware gets its own sibling package rather than a change
// here; a shell DSL waits for that second firmware to show which
// variation is worth abstracting.
//
// Every prompt shape, pagination marker, and command syntax here traces to
// the FastIron 10.0.10 command reference or the ICX YANG models cited in
// the source files — never to a captured device transcript, since none
// exists for this device family and none may be captured from the lab for
// it.
//
// See docs/architecture/2026-09-05-verified-device-access-direction.md,
// decision 11.
package fastiron
