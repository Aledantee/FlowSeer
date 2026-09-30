package yang

import (
	"reflect"

	"go.aledante.io/FlowSeer/src/common/errs"
)

// structops.go: the generic row machinery over [Schema] — change
// detection, partial-update merge, and leaf enumeration — that
// yanggen's per-list wrappers delegate to.

// EqualStructs reports whether two conforming struct values carry
// identical data. Pointer fields compare by pointee.
func EqualStructs[T any](a, b T) bool {
	return reflect.DeepEqual(a, b)
}

// MergeStructs overlays update's populated fields onto base and
// returns the result: set leaves (non-nil pointers, non-nil slices)
// and present containers in update win; everything else keeps base's
// value. List fields merge by whole-list replacement when update
// carries any entries — per-entry merging is the Watcher's job, keyed
// by row identity, not the codec's.
func MergeStructs[T any](s *Schema, base, update T) T {
	out := base
	bv := reflect.ValueOf(&out).Elem()
	uv := reflect.ValueOf(update)
	mergeStructValue(s, bv, uv)
	return out
}

// mergeStructValue merges uv into the settable bv per s.
func mergeStructValue(s *Schema, bv, uv reflect.Value) {
	for i := range s.Fields {
		f := &s.Fields[i]
		bf := bv.FieldByName(f.GoName)
		uf := uv.FieldByName(f.GoName)
		if !bf.IsValid() || !uf.IsValid() {
			continue
		}
		switch {
		case f.Child != nil && f.List, f.LeafList:
			if uf.Len() > 0 {
				bf.Set(uf)
			}
		case f.Child != nil:
			if uf.IsNil() {
				continue
			}
			if bf.IsNil() {
				bf.Set(uf)
				continue
			}
			// Clone before descending so the merge never mutates a
			// struct base still shares with the caller.
			clone := reflect.New(bf.Type().Elem())
			clone.Elem().Set(bf.Elem())
			bf.Set(clone)
			mergeStructValue(f.Child, bf.Elem(), uf.Elem())
		default:
			switch uf.Kind() {
			case reflect.Pointer:
				if !uf.IsNil() {
					bf.Set(uf)
				}
			case reflect.Slice:
				if !uf.IsNil() {
					bf.Set(uf)
				}
			default:
				bf.Set(uf)
			}
		}
	}
}

// VisitStructLeaves walks v's populated leaves as (relative path,
// typed value) pairs, depth-first in schema order — the
// [LeafEnumerator] contract the gNMI library consumes. Paths are
// relative to the node itself (its own name is not included);
// list-entry segments carry key predicates. fn returning false stops
// the walk.
func VisitStructLeaves(s *Schema, v any, fn func(Path, Value) bool) error {
	rv, err := structValue(v)
	if err != nil {
		return errs.Wrapf(err, "visit %s", s.Name)
	}
	_, err = visitLeaves(s, rv, Path{}, fn)
	return err
}

// visitLeaves recursively walks rv. Returns false when fn stopped
// the walk.
func visitLeaves(s *Schema, rv reflect.Value, prefix Path, fn func(Path, Value) bool) (bool, error) {
	stopped := false
	err := walkFields(s, func(f *Field, owner *Schema, group *Field) error {
		if stopped {
			return nil
		}
		fieldRV := rv
		if group != nil {
			var err error
			fieldRV, err = groupValue(rv, group, false)
			if err != nil {
				return err
			}
			if !fieldRV.IsValid() {
				return nil
			}
		}

		fv, err := fieldValue(fieldRV, f)
		if err != nil {
			return err
		}
		switch {
		case f.Child != nil && f.List:
			for j := 0; j < fv.Len(); j++ {
				entry := fv.Index(j)
				seg, err := listSegment(owner, f, entry)
				if err != nil {
					return err
				}
				cont, err := visitLeaves(f.Child, entry, appendSegment(prefix, seg), fn)
				if err != nil {
					return err
				}
				if !cont {
					stopped = true
					return nil
				}
			}
		case f.Child != nil:
			if fv.IsNil() {
				return nil
			}
			seg := Segment{Module: f.qualifiedModule(owner), Namespace: f.qualifiedNamespace(owner), Name: f.Child.Name}
			cont, err := visitLeaves(f.Child, fv.Elem(), appendSegment(prefix, seg), fn)
			if err != nil {
				return err
			}
			if !cont {
				stopped = true
				return nil
			}
		case f.LeafList:
			for j := 0; j < fv.Len(); j++ {
				val, err := scalarToValue(f.Type, fv.Index(j))
				if err != nil {
					return errs.Wrapf(err, "leaf-list %s", f.Name)
				}
				if !fn(appendSegment(prefix, leafSegment(owner, f)), val) {
					stopped = true
					return nil
				}
			}
		default:
			scalar := fv
			if scalar.Kind() == reflect.Pointer {
				if scalar.IsNil() {
					return nil
				}
				scalar = scalar.Elem()
			} else if scalar.Kind() == reflect.Slice && scalar.IsNil() {
				return nil
			}
			val, err := scalarToValue(f.Type, scalar)
			if err != nil {
				return errs.Wrapf(err, "leaf %s", f.Name)
			}
			if !fn(appendSegment(prefix, leafSegment(owner, f)), val) {
				stopped = true
				return nil
			}
		}
		return nil
	})
	if err != nil {
		return false, err
	}
	return !stopped, nil
}

// leafSegment builds the path segment for a leaf field.
func leafSegment(s *Schema, f *Field) Segment {
	return Segment{Module: f.qualifiedModule(s), Namespace: f.qualifiedNamespace(s), Name: f.Name}
}

// listSegment builds the keyed path segment for one list entry.
func listSegment(s *Schema, f *Field, entry reflect.Value) (Segment, error) {
	seg := Segment{Module: f.qualifiedModule(s), Namespace: f.qualifiedNamespace(s), Name: f.Child.Name}
	for _, keyName := range f.Child.Keys {
		kf := findFieldByName(f.Child, keyName)
		if kf == nil {
			return Segment{}, errs.Msgf("list %s key leaf %s missing from schema", f.Child.Name, keyName)
		}
		kv, err := fieldValue(entry, kf)
		if err != nil {
			return Segment{}, err
		}
		if kv.Kind() == reflect.Pointer {
			if kv.IsNil() {
				continue
			}
			kv = kv.Elem()
		}
		val, err := scalarToValue(kf.Type, kv)
		if err != nil {
			return Segment{}, errs.Wrapf(err, "list %s key %s", f.Child.Name, keyName)
		}
		text, err := val.Canonical()
		if err != nil {
			return Segment{}, errs.Wrapf(err, "list %s key %s", f.Child.Name, keyName)
		}
		seg.Keys = append(seg.Keys, KeyValue{Name: keyName, Value: text})
	}
	return seg, nil
}

// findFieldByName returns the schema field with the given YANG name.
func findFieldByName(s *Schema, name string) *Field {
	for i := range s.Fields {
		if s.Fields[i].Name == name {
			return &s.Fields[i]
		}
	}
	return nil
}

// appendSegment copies prefix and appends seg, so shared prefixes are
// never mutated across siblings.
func appendSegment(prefix Path, seg Segment) Path {
	segs := make([]Segment, 0, len(prefix.Segments)+1)
	segs = append(segs, prefix.Segments...)
	segs = append(segs, seg)
	return Path{Segments: segs}
}

// StructRowCodec assembles the [RowCodec] for a generated list: the
// generic schema-driven codecs plus the generated typed key
// extractor. yanggen emits one call per list.
func StructRowCodec[Row any, Key comparable](s *Schema, key func(*Row) Key) RowCodec[Row, Key] {
	return RowCodec[Row, Key]{
		DecodeXML:  func(data []byte) ([]Row, error) { return DecodeXMLList[Row](s, data) },
		DecodeJSON: func(data []byte) ([]Row, error) { return DecodeJSONList[Row](s, data) },
		Equal:      EqualStructs[Row],
		Merge:      func(base, update Row) Row { return MergeStructs(s, base, update) },
		Key:        func(row Row) Key { return key(&row) },
	}
}

// NestedRowCodec assembles the [RowCodec] for a flattened nested list:
// decoding walks the ancestor chain, maps decoded [NestedEntry] values to
// Rows, and merges updates into the base entry using the target list schema.
func NestedRowCodec[Entry, Row any, Key comparable](
	chain []*Schema,
	row func(ancestors [][]KeyValue, entry Entry) Row,
	entry func(*Row) *Entry,
	key func(*Row) Key,
) RowCodec[Row, Key] {
	var targetSchema *Schema
	if len(chain) > 0 {
		targetSchema = chain[len(chain)-1]
	}
	return RowCodec[Row, Key]{
		DecodeXML: func(data []byte) ([]Row, error) {
			entries, err := DecodeXMLNested[Entry](chain, data)
			if err != nil {
				return nil, err
			}
			rows := make([]Row, len(entries))
			for i := range entries {
				rows[i] = row(entries[i].AncestorKeys, entries[i].Entry)
			}
			return rows, nil
		},
		DecodeJSON: func(data []byte) ([]Row, error) {
			entries, err := DecodeJSONNested[Entry](chain, data)
			if err != nil {
				return nil, err
			}
			rows := make([]Row, len(entries))
			for i := range entries {
				rows[i] = row(entries[i].AncestorKeys, entries[i].Entry)
			}
			return rows, nil
		},
		Equal: EqualStructs[Row],
		Merge: func(base, update Row) Row {
			out := base
			bEntry := entry(&out)
			uEntry := entry(&update)
			if bEntry != nil && uEntry != nil && targetSchema != nil {
				*bEntry = MergeStructs(targetSchema, *bEntry, *uEntry)
			}
			return out
		},
		Key: func(r Row) Key { return key(&r) },
	}
}
