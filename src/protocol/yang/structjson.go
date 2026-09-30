package yang

import (
	"bytes"
	"encoding/json"
	"reflect"
	"strings"

	"go.aledante.io/FlowSeer/src/common/errs"
)

// structjson.go is the generic RFC 7951 JSON codec over [Schema]:
// members appear in schema order, and a child from a different
// module than its parent is qualified as "module:name" per RFC 7951
// §4.

// MarshalJSON7951Struct renders v (a struct or struct pointer
// conforming to s) as the node's RFC 7951 JSON object value.
func MarshalJSON7951Struct(s *Schema, v any) ([]byte, error) {
	rv, err := structValue(v)
	if err != nil {
		return nil, errs.Wrapf(err, "marshal %s", s.Name)
	}
	var b bytes.Buffer
	if err := writeJSONObject(&b, s, rv); err != nil {
		return nil, err
	}
	return b.Bytes(), nil
}

// writeJSONObject writes rv as a JSON object of s's populated fields.
func writeJSONObject(b *bytes.Buffer, s *Schema, rv reflect.Value) error {
	b.WriteByte('{')
	first := true
	comma := func() {
		if !first {
			b.WriteByte(',')
		}
		first = false
	}
	if err := walkFields(s, func(f *Field, owner *Schema, group *Field) error {
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
		name := f.Name
		if f.Child != nil {
			name = f.Child.Name
		}
		if mod := f.qualifiedModule(owner); mod != s.moduleName() {
			name = mod + ":" + name
		}
		key, err := json.Marshal(name)
		if err != nil {
			return errs.Wrapf(err, "member %s", name)
		}

		switch {
		case f.Child != nil && f.List:
			if fv.Len() == 0 {
				return nil
			}
			comma()
			b.Write(key)
			b.WriteString(":[")
			for j := 0; j < fv.Len(); j++ {
				if j > 0 {
					b.WriteByte(',')
				}
				if err := writeJSONObject(b, f.Child, fv.Index(j)); err != nil {
					return err
				}
			}
			b.WriteByte(']')
		case f.Child != nil:
			if fv.IsNil() {
				return nil
			}
			comma()
			b.Write(key)
			b.WriteByte(':')
			if err := writeJSONObject(b, f.Child, fv.Elem()); err != nil {
				return err
			}
		case f.LeafList:
			if fv.Len() == 0 {
				return nil
			}
			comma()
			b.Write(key)
			b.WriteString(":[")
			for j := 0; j < fv.Len(); j++ {
				if j > 0 {
					b.WriteByte(',')
				}
				if err := writeJSONLeaf(b, f, fv.Index(j)); err != nil {
					return err
				}
			}
			b.WriteByte(']')
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
			comma()
			b.Write(key)
			b.WriteByte(':')
			if err := writeJSONLeaf(b, f, scalar); err != nil {
				return err
			}
		}
		return nil
	}); err != nil {
		return err
	}
	b.WriteByte('}')
	return nil
}

// writeJSONLeaf renders one scalar in its RFC 7951 form.
func writeJSONLeaf(b *bytes.Buffer, f *Field, scalar reflect.Value) error {
	val, err := scalarToValue(f.Type, scalar)
	if err != nil {
		return errs.Wrapf(err, "leaf %s", f.Name)
	}
	raw, err := val.MarshalJSON7951()
	if err != nil {
		return errs.Wrapf(err, "leaf %s", f.Name)
	}
	b.Write(raw)
	return nil
}

// UnmarshalJSON7951Struct decodes an RFC 7951 JSON object value into
// v, a pointer to s's Go struct, replacing its content. Unknown members
// are ignored; members may arrive bare or module-qualified. A decode
// error may leave v partially populated.
func UnmarshalJSON7951Struct(s *Schema, data []byte, v any) error {
	rv := reflect.ValueOf(v)
	if rv.Kind() != reflect.Pointer || rv.IsNil() || rv.Elem().Kind() != reflect.Struct {
		return errs.New().Code(ErrCodeValueParse).Msgf("decode target for %s must be a non-nil struct pointer", s.Name)
	}
	var obj map[string]json.RawMessage
	if err := json.Unmarshal(data, &obj); err != nil || obj == nil {
		return errs.From(err).Code(ErrCodeValueParse).Msgf("%s is not a JSON object", s.Name)
	}
	rv.Elem().SetZero()
	return decodeJSONObject(s, obj, rv.Elem())
}

// DecodeJSONList decodes the list's entries into one Row each — the
// RowCodec.DecodeJSON shape. data may be the bare JSON array, or an
// object carrying the array under the list's bare or
// module-qualified name.
func DecodeJSONList[Row any](s *Schema, data []byte) ([]Row, error) {
	trimmed := bytes.TrimLeft(data, " \t\r\n")
	arr := json.RawMessage(trimmed)
	if len(trimmed) > 0 && trimmed[0] == '{' {
		var obj map[string]json.RawMessage
		if err := json.Unmarshal(data, &obj); err != nil {
			return nil, errs.From(err).Code(ErrCodeValueParse).Msgf("list %s payload is not a JSON object", s.Name)
		}
		found, ok := lookupMember(obj, s.moduleName(), s.Name)
		if !ok {
			return nil, nil
		}
		arr = found
	}
	var rawRows []json.RawMessage
	if err := json.Unmarshal(arr, &rawRows); err != nil || rawRows == nil {
		return nil, errs.From(err).Code(ErrCodeValueParse).Msgf("list %s is not a JSON array", s.Name)
	}
	rows := make([]Row, 0, len(rawRows))
	for _, raw := range rawRows {
		var row Row
		if err := UnmarshalJSON7951Struct(s, raw, &row); err != nil {
			return nil, err
		}
		rows = append(rows, row)
	}
	return rows, nil
}

// lookupMember finds a member by qualified then bare name.
func lookupMember(obj map[string]json.RawMessage, module, name string) (json.RawMessage, bool) {
	if raw, ok := obj[module+":"+name]; ok {
		return raw, true
	}
	raw, ok := obj[name]
	return raw, ok
}

// lookupJSONField finds a field's JSON member using the struct decoder's
// qualification rule. A bare grouped member is accepted only when its name
// is unique among the parent's fields.
func lookupJSONField(obj map[string]json.RawMessage, parent *Schema, f *Field, owner *Schema, group *Field) (json.RawMessage, bool, error) {
	name := f.Name
	if f.Child != nil {
		name = f.Child.Name
	}
	module := f.qualifiedModule(owner)
	if group == nil {
		raw, ok := lookupMember(obj, module, name)
		return raw, ok, nil
	}
	if raw, ok := obj[module+":"+name]; ok {
		return raw, true, nil
	}
	raw, ok := obj[name]
	if !ok {
		return nil, false, nil
	}

	var candidates []string
	plain := false
	if err := walkFields(parent, func(candidate *Field, candidateOwner *Schema, candidateGroup *Field) error {
		candidateName := candidate.Name
		if candidate.Child != nil {
			candidateName = candidate.Child.Name
		}
		if candidateName != name {
			return nil
		}
		if candidateGroup == nil {
			plain = true
			return nil
		}
		candidates = append(candidates, candidate.qualifiedModule(candidateOwner))
		return nil
	}); err != nil {
		return nil, false, err
	}
	if plain {
		return nil, false, nil
	}
	if len(candidates) == 1 {
		return raw, true, nil
	}
	return nil, false, errs.Msgf("ambiguous bare JSON member %q matches grouped fields from modules %s", name, strings.Join(candidates, ", "))
}

// decodeJSONObject populates rv from obj per s.
func decodeJSONObject(s *Schema, obj map[string]json.RawMessage, rv reflect.Value) error {
	return walkFields(s, func(f *Field, owner *Schema, group *Field) error {
		name := f.Name
		if f.Child != nil {
			name = f.Child.Name
		}
		raw, ok, err := lookupJSONField(obj, s, f, owner, group)
		if err != nil {
			return err
		}
		if !ok {
			return nil
		}
		if group != nil && ((f.Child != nil && f.List) || f.LeafList) {
			var entries []json.RawMessage
			if err := json.Unmarshal(raw, &entries); err != nil || entries == nil {
				kind := "leaf-list"
				if f.Child != nil {
					kind = "list"
				}
				return errs.From(err).Code(ErrCodeValueParse).Msgf("%s %s is not a JSON array", kind, name)
			}
			if len(entries) == 0 {
				return nil
			}
		}
		fieldRV := rv
		if group != nil {
			var err error
			fieldRV, err = groupValue(rv, group, true)
			if err != nil {
				return err
			}
		}
		fv, err := fieldValue(fieldRV, f)
		if err != nil {
			return err
		}
		switch {
		case f.Child != nil && f.List:
			var rawRows []json.RawMessage
			if err := json.Unmarshal(raw, &rawRows); err != nil || rawRows == nil {
				return errs.From(err).Code(ErrCodeValueParse).Msgf("list %s is not a JSON array", name)
			}
			for _, rawRow := range rawRows {
				entry := reflect.New(fv.Type().Elem())
				if err := UnmarshalJSON7951Struct(f.Child, rawRow, entry.Interface()); err != nil {
					return err
				}
				fv.Set(reflect.Append(fv, entry.Elem()))
			}
		case f.Child != nil:
			if fv.IsNil() {
				fv.Set(reflect.New(fv.Type().Elem()))
			}
			if err := UnmarshalJSON7951Struct(f.Child, raw, fv.Interface()); err != nil {
				return err
			}
		case f.LeafList:
			var rawEntries []json.RawMessage
			if err := json.Unmarshal(raw, &rawEntries); err != nil || rawEntries == nil {
				return errs.From(err).Code(ErrCodeValueParse).Msgf("leaf-list %s is not a JSON array", name)
			}
			for _, rawEntry := range rawEntries {
				entry := reflect.New(fv.Type().Elem()).Elem()
				if err := storeLeafJSON(f, rawEntry, entry); err != nil {
					return err
				}
				fv.Set(reflect.Append(fv, entry))
			}
		default:
			if err := storeLeafJSON(f, raw, derefForSet(fv)); err != nil {
				return err
			}
		}
		return nil
	})
}

// storeLeafJSON parses one RFC 7951 leaf value under f's type and
// stores it.
func storeLeafJSON(f *Field, raw json.RawMessage, target reflect.Value) error {
	val, err := ParseJSON7951(*f.Type, raw)
	if err != nil {
		return errs.Wrapf(err, "leaf %s", f.Name)
	}
	return valueToScalar(f.Type, val, target)
}
