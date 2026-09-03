package lex

// interner holds one copy of each distinct identifier in a file.
//
// The table is per file and starts empty because reserved words never
// reach it: they are resolved to a [Keyword] during the scan and get
// their text from the fixed table instead. What is left is the file's
// own descriptors, which repeat heavily — a MIB names the same object in
// its definition, its index clause and its conformance group — so one
// copy per distinct name is a large saving over one per occurrence.
//
// The zero value is usable. An interner is not safe for concurrent use.
type interner struct {
	table map[string]string
}

// intern returns the file's single copy of the text in b. It never
// retains b, so the caller may reuse the underlying array.
func (n *interner) intern(b []byte) string {
	// Indexing a string-keyed map with a byte slice is the one
	// conversion the compiler performs without copying, so a repeat
	// occurrence costs a hash and no allocation at all.
	if s, ok := n.table[string(b)]; ok {
		return s
	}

	if n.table == nil {
		n.table = make(map[string]string, 64)
	}

	s := string(b)
	n.table[s] = s

	return s
}
