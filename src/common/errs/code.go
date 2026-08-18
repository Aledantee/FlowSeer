package errs

import (
	"fmt"
	"slices"
	"strings"
	"sync"
)

// Code is an error's stable identity across process boundaries. Two errors
// carrying the same Code match under [errors.Is] even when they share no
// pointer identity — that is how a peer's decoded error matches the local
// sentinel it stands for.
//
// Codes are a wire contract: append-only, never renamed, never reused for a
// different meaning. Declare them with [NewCode].
type Code string

// String returns the code's wire form, "<package>/<name>".
func (c Code) String() string {
	return string(c)
}

var registry sync.Map

// NewCode registers name as a [Code] and returns it. Declare codes as
// package-level variables so registration happens at init.
//
// name is "<package>/<name>", each segment starting with a lowercase letter
// or digit and continuing with lowercase letters, digits, '-' or '_' — for
// example "snmp/priv-decrypt". NewCode panics on a malformed name and on a
// name already registered, so a collision fails the first run of any binary
// that links both declarations rather than surfacing later as a false
// [errors.Is] match.
func NewCode(name string) Code {
	if err := validateCode(name); err != nil {
		panic(fmt.Sprintf("errs: %v", err))
	}

	if _, loaded := registry.LoadOrStore(name, struct{}{}); loaded {
		panic(fmt.Sprintf("errs: duplicate error code %q", name))
	}

	return Code(name)
}

// Codes returns every [Code] registered in this process, sorted. Only codes
// from linked packages appear; the repo-wide uniqueness gate is a source
// scan, not this list.
func Codes() []Code {
	var codes []Code

	registry.Range(func(key, _ any) bool {
		codes = append(codes, Code(key.(string)))
		return true
	})
	slices.Sort(codes)

	return codes
}

// CodeOf returns the first [Code] found in err's chain, searching outermost
// first, and reports whether one was found. Errors that carry no code, and
// errors from other packages, yield false.
func CodeOf(err error) (Code, bool) {
	var (
		found Code
		ok    bool
	)

	walk(err, func(e *Error) bool {
		if e.code == "" {
			return true
		}

		found, ok = e.code, true

		return false
	})

	return found, ok
}

func validateCode(name string) error {
	pkg, rest, found := strings.Cut(name, "/")
	if !found {
		return fmt.Errorf("error code %q must be \"<package>/<name>\"", name)
	}

	if err := validateCodeSegment(name, pkg); err != nil {
		return err
	}

	return validateCodeSegment(name, rest)
}

func validateCodeSegment(name, segment string) error {
	if segment == "" {
		return fmt.Errorf("error code %q has an empty segment", name)
	}

	for i, r := range segment {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9':
		case (r == '-' || r == '_') && i > 0:
		default:
			return fmt.Errorf("error code %q has an invalid segment %q", name, segment)
		}
	}

	return nil
}
