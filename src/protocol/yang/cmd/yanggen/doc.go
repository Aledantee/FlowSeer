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
// consuming only src/protocol/yang's public API.
//
// Structurally identical containers and lists share one struct type
// and one Schema across all their schema paths. A struct takes the
// shortest suffix of its instances' common ancestry names that is
// unique in the package, and its schema var is that name plus
// "Schema". Each list instance names its Key, FlatRow, and Descriptor
// from the shortest unique suffix of its own path, so a shared list
// struct `ServersServer` can carry `ServerKey` and `ServerDescriptor`;
// a top-level container's descriptor is named the same way. Clashing
// names grow by one ancestor segment together until they are unique,
// and a clash that runs out of segments resolves to X plus six hex
// digits of the shape key or path.
//
// Every child from an augmenting module is emitted behind one group field
// per augmenting module. The group struct and schema live in the augmenting
// package, and a group schema has no codec root.
// Descriptors and companion key types for lists inside a group live in
// the package of the tree's top-level module, while their row structs
// remain in the package that owns the list.
//
// Module descriptors are emitted as package-level *yang.Module variables
// for modules named by schemas or descriptor paths in that package. Schemas
// format single-line field literals, and list descriptors construct compact
// paths and codecs via JoinPath, In, and NestedRowCodec.
//
// A lockfile (yanggen.lock.json) records, per module, its newest
// revision, source hash, and a closure hash covering every source in
// its dependency closure — imports, includes, and reverse
// augment/deviation contributors. `yanggen -check` compares lockfiles
// instead of regenerating ~1,200 packages; a generator-version bump
// flags everything.
//
// The output directory is a Go module of its own, so the root module's
// `./...` does not compile every binding package. yanggen writes its
// go.mod (the root module path plus the output directory, the root's go
// directive, and a require and replace for the root module found above
// -out), then runs `go mod tidy` there for the indirect requirements
// and go.sum. -check also reports drift in the statements yanggen
// writes; the indirect requirements belong to tidy and are not
// compared.
//
// Duplicate augment children are recovered from goyang's augment
// entries and sorted by module and name before emission. The view and
// its qualified paths keep generated output stable when goyang chooses
// a different survivor.
//
// Flags: -config, -out, -verify (load-only), -check (drift gate),
// -update. Exit codes: 0 success, 1 load/check/runtime failure, 2
// flag misuse.
//
// Run from the repository root:
//
//	go run ./src/protocol/yang/cmd/yanggen -update
//	go run ./src/protocol/yang/cmd/yanggen -check
//
// The default invocation and -update both rebuild every configured vendor's
// output directory. Generation renders to temporary directories and swaps each
// vendor into place only after every vendor renders without error. A failure
// while rendering leaves existing output untouched. -check verifies source
// hashes and the generator version, so it does not detect edited or missing
// generated Go files.
package main
