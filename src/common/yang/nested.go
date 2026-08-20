package yang

import (
	"bytes"
	"encoding/json"
	"encoding/xml"
	"errors"
	"io"
	"reflect"

	"go.aledante.io/FlowSeer/src/common/errs"
)

// nested.go supports KTD4's nested-list flattening: an inner-list
// entry is its own row, identified by the full keyed instance path,
// so decoding must recover each ancestor list level's key values from
// the wire payload alongside the entry itself. yanggen emits flat-row
// wrappers over [DecodeXMLNested] / [DecodeJSONNested]; ancestor keys
// travel as canonical strings ([KeyValue]), which keeps generated key
// structs comparable regardless of the ancestor's key types.

// NestedEntry is one flattened inner-list entry: the decoded entry
// plus, per ancestor list level (outermost first), that level's key
// values in YANG key order.
type NestedEntry[Inner any] struct {
	AncestorKeys [][]KeyValue
	Entry        Inner
}

// KeyValueLookup returns the value for name in kvs, or "" when
// absent. Generated flat-row assembly uses it to pick ancestor keys
// by name.
func KeyValueLookup(kvs []KeyValue, name string) string {
	for _, kv := range kvs {
		if kv.Name == name {
			return kv.Value
		}
	}
	return ""
}

// DecodeXMLNested decodes every instance of the target list from a
// NETCONF XML payload that includes its ancestor list levels. chain
// holds the list schemas outermost-first; the last element is the
// target. Ancestor key leaves are read as they appear, so a peer that
// emits keys after nested content (against RFC 7950 §7.8.5's
// canonical order) would yield incomplete ancestor keys.
func DecodeXMLNested[Inner any](chain []*Schema, data []byte) ([]NestedEntry[Inner], error) {
	if len(chain) == 0 {
		return nil, errs.Msg("empty schema chain")
	}
	dec := xml.NewDecoder(bytes.NewReader(data))
	var out []NestedEntry[Inner]
	err := scanXMLLevel(dec, chain, nil, &out, false)
	if err != nil {
		return nil, err
	}
	return out, nil
}

// scanXMLLevel scans the current containment scope for elements
// matching chain[0]. bounded is true when the scope is a specific
// element whose EndElement terminates the scan; false at document
// top level, where io.EOF terminates it.
func scanXMLLevel[Inner any](dec *xml.Decoder, chain []*Schema, anc [][]KeyValue, out *[]NestedEntry[Inner], bounded bool) error {
	level := chain[0]
	for {
		tok, err := dec.Token()
		if err != nil {
			if !bounded && errors.Is(err, io.EOF) {
				return nil
			}
			return errs.From(err).Code(ErrCodeValueParse).Msgf("scan XML for %s", level.Name)
		}
		switch t := tok.(type) {
		case xml.EndElement:
			if bounded {
				return nil
			}
		case xml.StartElement:
			if !xmlNameMatches(t.Name, level.Name, level.Namespace) {
				// Descend transparently: the matching elements may sit
				// under wrapper elements (rpc-reply/data, parent
				// containers). A non-matching element just opens a new
				// scope of the same level.
				if err := scanXMLLevel(dec, chain, anc, out, true); err != nil {
					return err
				}
				continue
			}
			if len(chain) == 1 {
				var entry Inner
				rv := reflect.ValueOf(&entry).Elem()
				if err := decodeXMLInto(dec, level, rv); err != nil {
					return err
				}
				*out = append(*out, NestedEntry[Inner]{AncestorKeys: cloneKeys(anc), Entry: entry})
				continue
			}
			if err := scanXMLAncestor(dec, chain, anc, out); err != nil {
				return err
			}
		default:
		}
	}
}

// scanXMLAncestor consumes one ancestor-list entry element: it
// collects the level's key leaves as they appear and scans the
// remaining content for the next chain level.
func scanXMLAncestor[Inner any](dec *xml.Decoder, chain []*Schema, anc [][]KeyValue, out *[]NestedEntry[Inner]) error {
	level := chain[0]
	keys := make([]KeyValue, 0, len(level.Keys))
	for {
		tok, err := dec.Token()
		if err != nil {
			return errs.From(err).Code(ErrCodeValueParse).Msgf("decode %s entry", level.Name)
		}
		switch t := tok.(type) {
		case xml.EndElement:
			return nil
		case xml.StartElement:
			if isKeyLeaf(level, t.Name.Local) {
				text, err := elementText(dec)
				if err != nil {
					return errs.Wrapf(err, "%s key %s", level.Name, t.Name.Local)
				}
				keys = append(keys, KeyValue{Name: t.Name.Local, Value: text})
				continue
			}
			next := chain[1]
			if xmlNameMatches(t.Name, next.Name, next.Namespace) {
				if err := dispatchXMLMatch(dec, chain[1:], append(cloneKeys(anc), orderKeys(level, keys)), out); err != nil {
					return err
				}
				continue
			}
			// Anything else may still contain the next level deeper
			// down (intermediate containers).
			if err := scanXMLLevel(dec, chain[1:], append(cloneKeys(anc), orderKeys(level, keys)), out, true); err != nil {
				return err
			}
		default:
		}
	}
}

// dispatchXMLMatch handles an element already matched against
// chain[0]: decode it as the target entry, or recurse as the next
// ancestor level.
func dispatchXMLMatch[Inner any](dec *xml.Decoder, chain []*Schema, anc [][]KeyValue, out *[]NestedEntry[Inner]) error {
	if len(chain) == 1 {
		var entry Inner
		rv := reflect.ValueOf(&entry).Elem()
		if err := decodeXMLInto(dec, chain[0], rv); err != nil {
			return err
		}
		*out = append(*out, NestedEntry[Inner]{AncestorKeys: anc, Entry: entry})
		return nil
	}
	return scanXMLAncestor(dec, chain, anc, out)
}

// xmlNameMatches reports whether an element name matches a schema
// node, by local name and by namespace when both declare one.
func xmlNameMatches(name xml.Name, local, ns string) bool {
	if name.Local != local {
		return false
	}
	return ns == "" || name.Space == "" || name.Space == ns
}

// isKeyLeaf reports whether local names one of s's key leaves.
func isKeyLeaf(s *Schema, local string) bool {
	for _, k := range s.Keys {
		if k == local {
			return true
		}
	}
	return false
}

// orderKeys returns keys sorted into s.Keys order, dropping
// duplicates and unknowns.
func orderKeys(s *Schema, keys []KeyValue) []KeyValue {
	out := make([]KeyValue, 0, len(s.Keys))
	for _, name := range s.Keys {
		if v := KeyValueLookup(keys, name); v != "" || containsKey(keys, name) {
			out = append(out, KeyValue{Name: name, Value: v})
		}
	}
	return out
}

// containsKey reports whether kvs carries name at all (even empty).
func containsKey(kvs []KeyValue, name string) bool {
	for _, kv := range kvs {
		if kv.Name == name {
			return true
		}
	}
	return false
}

// cloneKeys copies the ancestor-key stack so sibling branches never
// share backing arrays.
func cloneKeys(anc [][]KeyValue) [][]KeyValue {
	out := make([][]KeyValue, len(anc))
	copy(out, anc)
	return out
}

// DecodeJSONNested is [DecodeXMLNested]'s RFC 7951 counterpart: data
// is an object (or the outermost list's bare array) carrying the
// ancestor levels; each level's entries are walked, keys captured in
// canonical form, and target entries decoded.
func DecodeJSONNested[Inner any](chain []*Schema, data []byte) ([]NestedEntry[Inner], error) {
	if len(chain) == 0 {
		return nil, errs.Msg("empty schema chain")
	}
	var out []NestedEntry[Inner]
	trimmed := bytes.TrimLeft(data, " \t\r\n")
	if len(trimmed) > 0 && trimmed[0] == '[' {
		if err := walkJSONLevel(chain, json.RawMessage(trimmed), nil, &out); err != nil {
			return nil, err
		}
		return out, nil
	}
	var obj map[string]json.RawMessage
	if err := json.Unmarshal(data, &obj); err != nil {
		return nil, errs.From(err).Code(ErrCodeValueParse).Msgf("nested %s payload is not a JSON object", chain[0].Name)
	}
	arr, ok := lookupMember(obj, chain[0].Module, chain[0].Name)
	if !ok {
		return nil, nil
	}
	if err := walkJSONLevel(chain, arr, nil, &out); err != nil {
		return nil, err
	}
	return out, nil
}

// walkJSONLevel consumes one level's entry array.
func walkJSONLevel[Inner any](chain []*Schema, arr json.RawMessage, anc [][]KeyValue, out *[]NestedEntry[Inner]) error {
	level := chain[0]
	var rawRows []json.RawMessage
	if err := json.Unmarshal(arr, &rawRows); err != nil {
		return errs.From(err).Code(ErrCodeValueParse).Msgf("list %s is not a JSON array", level.Name)
	}
	for _, raw := range rawRows {
		if len(chain) == 1 {
			var entry Inner
			if err := UnmarshalJSON7951Struct(level, raw, &entry); err != nil {
				return err
			}
			*out = append(*out, NestedEntry[Inner]{AncestorKeys: cloneKeys(anc), Entry: entry})
			continue
		}
		var obj map[string]json.RawMessage
		if err := json.Unmarshal(raw, &obj); err != nil {
			return errs.From(err).Code(ErrCodeValueParse).Msgf("list %s entry is not a JSON object", level.Name)
		}
		keys, err := jsonLevelKeys(level, obj)
		if err != nil {
			return err
		}
		next := chain[1]
		childArr, ok := findJSONDescendant(level, next, obj)
		if !ok {
			continue
		}
		if err := walkJSONLevel(chain[1:], childArr, append(cloneKeys(anc), keys), out); err != nil {
			return err
		}
	}
	return nil
}

// jsonLevelKeys extracts a level's key leaves in canonical form.
func jsonLevelKeys(level *Schema, obj map[string]json.RawMessage) ([]KeyValue, error) {
	keys := make([]KeyValue, 0, len(level.Keys))
	for _, name := range level.Keys {
		f := findFieldByName(level, name)
		if f == nil || f.Type == nil {
			continue
		}
		raw, ok := lookupMember(obj, f.qualifiedModule(level), name)
		if !ok {
			continue
		}
		val, err := ParseJSON7951(*f.Type, raw)
		if err != nil {
			return nil, errs.Wrapf(err, "%s key %s", level.Name, name)
		}
		canon, err := val.Canonical()
		if err != nil {
			return nil, errs.Wrapf(err, "%s key %s", level.Name, name)
		}
		keys = append(keys, KeyValue{Name: name, Value: canon})
	}
	return keys, nil
}

// findJSONDescendant locates the next level's entry array within an
// ancestor entry: directly as a member, or one intermediate container
// object down (lists nested under a wrapper container).
func findJSONDescendant(level, next *Schema, obj map[string]json.RawMessage) (json.RawMessage, bool) {
	if arr, ok := lookupMember(obj, next.Module, next.Name); ok {
		return arr, true
	}
	for i := range level.Fields {
		f := &level.Fields[i]
		if f.Child == nil || f.List {
			continue
		}
		raw, ok := lookupMember(obj, f.qualifiedModule(level), f.Child.Name)
		if !ok {
			continue
		}
		var inner map[string]json.RawMessage
		if err := json.Unmarshal(raw, &inner); err != nil {
			continue
		}
		if arr, ok := findJSONDescendant(f.Child, next, inner); ok {
			return arr, true
		}
	}
	return json.RawMessage{}, false
}

// SubtreeDescriptor builds the synthetic-row descriptor for any
// non-list subtree from its schema and path. yanggen emits descriptor
// functions only for top-level containers; a caller watching a deeper
// subtree composes one here from the exported schema value and the
// subtree's path.
func SubtreeDescriptor[Row any](s *Schema, path Path) ListDescriptor[Row, struct{}] {
	return ListDescriptor[Row, struct{}]{Path: path, Codec: ContainerRowCodec[Row](s)}
}

// ContainerRowCodec is the synthetic-row codec for a non-list
// subtree (KTD4): the whole subtree is one row whose identity is the
// subtree path, so Key is the unit type. Decoding an absent subtree
// yields zero rows — a presence container disappearing surfaces as a
// Remove event.
func ContainerRowCodec[Row any](s *Schema) RowCodec[Row, struct{}] {
	return RowCodec[Row, struct{}]{
		DecodeXML: func(data []byte) ([]Row, error) {
			rows, err := DecodeXMLList[Row](s, data)
			if err != nil || len(rows) == 0 {
				return nil, err
			}
			return rows[:1], nil
		},
		DecodeJSON: func(data []byte) ([]Row, error) {
			var obj map[string]json.RawMessage
			if err := json.Unmarshal(data, &obj); err != nil {
				// The payload may be the bare object value itself.
				var row Row
				if uerr := UnmarshalJSON7951Struct(s, data, &row); uerr != nil {
					return nil, uerr
				}
				return []Row{row}, nil
			}
			raw, ok := lookupMember(obj, s.Module, s.Name)
			if !ok {
				return nil, nil
			}
			var row Row
			if err := UnmarshalJSON7951Struct(s, raw, &row); err != nil {
				return nil, err
			}
			return []Row{row}, nil
		},
		Equal: func(a, b Row) bool { return EqualStructs(a, b) },
		Merge: func(base, update Row) Row { return MergeStructs(s, base, update) },
		Key:   func(Row) struct{} { return struct{}{} },
	}
}
