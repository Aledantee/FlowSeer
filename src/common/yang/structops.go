package yang

import (
	"reflect"

	"go.aledante.io/FlowSeer/src/common/errs"
)

// structops.go: the generic row machinery over [Schema] — change
// detection, partial-update merge, and leaf enumeration — that
// yanggen's per-list wrappers delegate to (KTD4).

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
	for i := range s.Fields {
		f := &s.Fields[i]
		fv, err := fieldValue(rv, f)
		if err != nil {
			return false, err
		}
		switch {
		case f.Child != nil && f.List:
			for j := 0; j < fv.Len(); j++ {
				entry := fv.Index(j)
				seg, err := listSegment(s, f, entry)
				if err != nil {
					return false, err
				}
				cont, err := visitLeaves(f.Child, entry, appendSegment(prefix, seg), fn)
				if err != nil || !cont {
					return cont, err
				}
			}
		case f.Child != nil:
			if fv.IsNil() {
				continue
			}
			seg := Segment{Module: f.qualifiedModule(s), Namespace: f.qualifiedNamespace(s), Name: f.Child.Name}
			cont, err := visitLeaves(f.Child, fv.Elem(), appendSegment(prefix, seg), fn)
			if err != nil || !cont {
				return cont, err
			}
		case f.LeafList:
			for j := 0; j < fv.Len(); j++ {
				val, err := scalarToValue(f.Type, fv.Index(j))
				if err != nil {
					return false, errs.Wrapf(err, "leaf-list %s", f.Name)
				}
				if !fn(appendSegment(prefix, leafSegment(s, f)), val) {
					return false, nil
				}
			}
		default:
			scalar := fv
			if scalar.Kind() == reflect.Pointer {
				if scalar.IsNil() {
					continue
				}
				scalar = scalar.Elem()
			} else if scalar.Kind() == reflect.Slice && scalar.IsNil() {
				continue
			}
			val, err := scalarToValue(f.Type, scalar)
			if err != nil {
				return false, errs.Wrapf(err, "leaf %s", f.Name)
			}
			if !fn(appendSegment(prefix, leafSegment(s, f)), val) {
				return false, nil
			}
		}
	}
	return true, nil
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
