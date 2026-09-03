package main

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"strings"
	"unicode"
)

// naming.go: deterministic YANG-to-Go identifier mangling with a
// collision registry. The ygot enum-clash failure (openconfig/ygot
// issue 888, IOS-XE native) is the case this must survive: two names
// mangling to the same identifier resolve deterministically by
// suffixing a short hash of the loser's schema path, never by parse
// order.

// camel converts a YANG identifier to an exported Go identifier:
// split on '-', '.', '_', capitalize each part, drop anything not
// alphanumeric, and prefix "X" when the result would start with a
// digit or be empty.
func camel(name string) string {
	var b strings.Builder
	upperNext := true
	for _, r := range name {
		switch {
		case r == '-' || r == '.' || r == '_' || r == ' ' || r == '/' || r == ':':
			upperNext = true
		case unicode.IsLetter(r) || unicode.IsDigit(r):
			if upperNext {
				r = unicode.ToUpper(r)
				upperNext = false
			}
			b.WriteRune(r)
		default:
			upperNext = true
		}
	}
	s := b.String()
	if s == "" || unicode.IsDigit(rune(s[0])) {
		s = "X" + s
	}
	return s
}

// nameScope allocates unique identifiers within one scope (a package
// or one struct's fields). Identical (identifier, path) pairs return
// the same name; a different path colliding on the identifier gets a
// deterministic "_<hash>" suffix derived from its path.
type nameScope struct {
	byName map[string]string // identifier -> owning path
	byPath map[string]string // path -> identifier
}

func newNameScope() *nameScope {
	return &nameScope{byName: make(map[string]string), byPath: make(map[string]string)}
}

// claim returns the unique identifier for (want, path).
func (s *nameScope) claim(want, path string) string {
	if name, ok := s.byPath[path]; ok {
		return name
	}
	name := want
	if owner, taken := s.byName[name]; taken && owner != path {
		sum := sha256.Sum256([]byte(path))
		name = fmt.Sprintf("%s_%s", want, hex.EncodeToString(sum[:3]))
		// A hash collision on top of a name collision is vanishingly
		// unlikely but must still terminate deterministically.
		for i := 4; ; i++ {
			if owner, taken := s.byName[name]; !taken || owner == path {
				break
			}
			sum = sha256.Sum256([]byte(path + fmt.Sprint(i)))
			name = fmt.Sprintf("%s_%s", want, hex.EncodeToString(sum[:3]))
		}
	}
	s.byName[name] = path
	s.byPath[path] = name
	return name
}
