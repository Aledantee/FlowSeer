package yang

// keyhelpers.go: the small helpers generated key extractors call so
// composite key structs stay comparable — the compound-representation
// kinds (identityref, decimal64, union, bits, binary) key on their
// canonical string form.

// CanonicalKey renders a generated leaf field's value as its
// canonical string for use in a composite key struct. v is the field
// itself: *Value (decimal64/union), *Identity, []string (bits), or
// []byte (binary). Nil and unrenderable values yield "".
func CanonicalKey(v any) string {
	switch t := v.(type) {
	case *Value:
		if t == nil {
			return ""
		}
		s, err := t.Canonical()
		if err != nil {
			return ""
		}
		return s
	case *Identity:
		if t == nil {
			return ""
		}
		return t.String()
	case []string:
		s, err := (Value{Type: Type{Kind: TypeBits}, Bits: t}).Canonical()
		if err != nil {
			return ""
		}
		return s
	case []byte:
		s, err := (Value{Type: Type{Kind: TypeBinary}, Bytes: t}).Canonical()
		if err != nil {
			return ""
		}
		return s
	default:
		return ""
	}
}

// AncestorKey returns the named key value at one ancestor level of a
// flattened nested-list entry, or "" when the level or key is
// missing. Generated flat-row assembly uses it so a payload with
// unexpected shape degrades to empty identity components instead of
// panicking.
func AncestorKey(levels [][]KeyValue, level int, name string) string {
	if level < 0 || level >= len(levels) {
		return ""
	}
	return KeyValueLookup(levels[level], name)
}
