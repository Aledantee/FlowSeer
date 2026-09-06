// Package secret carries credential material that must not be printed.
//
// A [Value] holds a password, passphrase, private key, or community
// string. It renders as "[REDACTED]" wherever it is the operand or sits
// in an exported field — the fmt verbs, encoding/json,
// encoding.TextMarshaler, and slog — so a struct that holds one can be
// logged, formatted into an error, or dumped by a failing test without
// leaking what it carries:
//
//	opts := netconf.Options{Username: "admin", Password: secret.NewString(pw)}
//	slog.Info("dialing", "opts", opts) // opts={Username:admin Password:[REDACTED] ...}
//
// The classification travels with the value rather than with the field
// name, because a value survives being copied into a map, a wrapped
// error, or a struct someone else prints, and because a key-name
// denylist cannot recognize a secret stored under an innocent key.
//
// Two paths escape that, both a property of fmt rather than of this
// type. A Value in an *unexported* field is printed by the reflect
// walker as its raw bytes, because fmt consults a value's own methods
// only when the field can be interfaced: %+v on a struct holding one
// yields {pass:{b:[104 117 ...]}}. And %w applied to something that is
// not an error takes fmt's badVerb path, which reprints the operand
// through the same walker; go vet rejects that at compile time in the
// ordinary case. Neither can be closed from inside the type. Where a
// package keeps material in an unexported field, that struct is not
// safe to print.
//
// # Reading the material
//
// [Value.Reveal] and [Value.RevealString] are the only ways out. They
// are named to be greppable: the reveal sites are the audit surface of
// a package that handles credentials, and there should be few of them,
// each at the point where the material enters a transport.
//
// Reveal returns the value's own slice rather than a copy. A copy this
// package does not own cannot be wiped, so every copy handed out is
// material [Value.Zero] can no longer reach. The caller must not write
// to it.
//
// # Comparison
//
// A Value holds a slice, so == does not compile on it. Compare with
// [Value.Equal] or [Value.EqualString], which take time independent of
// where two equal-length inputs first differ. Use [Value.Empty] to ask
// whether one is set.
//
// # Wiping
//
// [Value.Zero] overwrites the material in place. It therefore reaches
// every copy of the Value, since copies share one backing array — that
// is the point, and it is why a Value is passed around rather than the
// bytes inside it. What Zero cannot reach is a slice already handed out
// by Reveal, or the string RevealString returned.
//
// Zeroing is a best effort against a later disclosure of process
// memory, not a defense against an attacker who is already reading it.
// The runtime may have copied the material during a stack or heap move
// before Zero ran.
//
// # Encoding
//
// Marshaling is deliberately asymmetric: [Value.MarshalJSON] and
// [Value.MarshalText] emit the redaction literal, and the unmarshalers
// reject exactly that literal. A round trip through a redacted dump
// would otherwise hand back a Value holding the string "[REDACTED]" and
// a caller with no way to notice.
//
// The write direction is the one that loses data. A Value serialized
// into a document meant to be read back writes the placeholder with no
// error, and the loss surfaces later at the reader as
// [ErrRedactedInput]. Marshaling is for diagnostics; a document that
// has to carry the material takes [Value.RevealString] at a call site
// that says so.
//
// An unset Value renders as the empty string instead of the literal.
// Nothing is hidden by saying that a passphrase was never configured,
// and a diagnostic that distinguishes "unset" from "set" answers the
// question an operator actually has.
package secret
