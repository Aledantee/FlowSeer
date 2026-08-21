// Command yanggen generates typed Go bindings for the vendored YANG
// trees under spec/yang, mibgen's sibling for the model-driven
// protocol libraries.
//
// The manifest (yanggen.yaml, strict YAML) lists vendors rather than
// modules: each vendor includes every .yang file under its paths,
// minus an explicit skip-list whose entries each record why a module
// is excluded. goyang resolves the schema; submodules are inlined
// into their parents first (flatten.go) so typedefs and groupings
// defined in submodules stay visible to importing modules. Output is
// one Go package per module under generated/go/yang/<vendor>/,
// consuming only src/common/yang's public API.
//
// A lockfile (yanggen.lock.json) records, per module, its newest
// revision, source hash, and a closure hash covering every source in
// its dependency closure — imports, includes, and reverse
// augment/deviation contributors. `yanggen -check` compares lockfiles
// instead of regenerating ~1,200 packages; a generator-version bump
// flags everything.
//
// Flags: -config, -out, -pkg-prefix, -verify (load-only), -check
// (drift gate), -update. Exit codes: 0 success, 1 load/check/runtime
// failure, 2 flag misuse.
package main
