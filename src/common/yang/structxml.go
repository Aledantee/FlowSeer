package yang

import (
	"bytes"
	"encoding/xml"
	"errors"
	"io"
	"reflect"
	"strings"

	"go.aledante.io/FlowSeer/src/common/errs"
)

// structxml.go is the generic NETCONF XML codec over [Schema]: one
// implementation encodes and decodes every generated struct, in
// schema order, with augmented-in children carrying their own
// module's namespace.

// MarshalXMLStruct renders v (a struct or struct pointer conforming
// to s) as the node's NETCONF XML element, including its xmlns.
func MarshalXMLStruct(s *Schema, v any) ([]byte, error) {
	rv, err := structValue(v)
	if err != nil {
		return nil, errs.Wrapf(err, "marshal %s", s.Name)
	}
	var b strings.Builder
	if err := writeXMLElement(&b, s, rv, ""); err != nil {
		return nil, err
	}
	return []byte(b.String()), nil
}

// writeXMLElement writes one <s.Name> element for rv. parentNS is the
// effective namespace already in scope.
func writeXMLElement(b *strings.Builder, s *Schema, rv reflect.Value, parentNS string) error {
	openXML(b, s.Name, s.Namespace, parentNS)
	ns := s.Namespace
	if ns == "" {
		ns = parentNS
	}
	for i := range s.Fields {
		if err := writeXMLField(b, s, &s.Fields[i], rv, ns); err != nil {
			return err
		}
	}
	b.WriteString("</")
	b.WriteString(s.Name)
	b.WriteByte('>')
	return nil
}

// writeXMLField writes the elements for one populated field of rv.
func writeXMLField(b *strings.Builder, s *Schema, f *Field, rv reflect.Value, ns string) error {
	fv, err := fieldValue(rv, f)
	if err != nil {
		return err
	}
	fieldNS := f.qualifiedNamespace(s)

	switch {
	case f.Child != nil && f.List:
		for i := 0; i < fv.Len(); i++ {
			if err := writeXMLElement(b, f.Child, fv.Index(i), ns); err != nil {
				return err
			}
		}
	case f.Child != nil:
		if fv.IsNil() {
			return nil
		}
		return writeXMLElement(b, f.Child, fv.Elem(), ns)
	case f.LeafList:
		for i := 0; i < fv.Len(); i++ {
			if err := writeXMLLeaf(b, f, fv.Index(i), fieldNS, ns); err != nil {
				return err
			}
		}
	default:
		if fv.Kind() == reflect.Pointer {
			if fv.IsNil() {
				return nil
			}
			fv = fv.Elem()
		} else if fv.Kind() == reflect.Slice && fv.IsNil() {
			// Unset bits/binary leaf.
			return nil
		}
		return writeXMLLeaf(b, f, fv, fieldNS, ns)
	}
	return nil
}

// writeXMLLeaf renders one scalar leaf element.
func writeXMLLeaf(b *strings.Builder, f *Field, scalar reflect.Value, fieldNS, ns string) error {
	val, err := scalarToValue(f.Type, scalar)
	if err != nil {
		return errs.Wrapf(err, "leaf %s", f.Name)
	}
	text, err := val.Canonical()
	if err != nil {
		return errs.Wrapf(err, "leaf %s", f.Name)
	}
	openXML(b, f.Name, fieldNS, ns)
	if err := xml.EscapeText(b, []byte(text)); err != nil {
		return errs.Wrapf(err, "leaf %s", f.Name)
	}
	b.WriteString("</")
	b.WriteString(f.Name)
	b.WriteByte('>')
	return nil
}

// openXML writes an opening tag, adding xmlns only when it changes
// the effective namespace.
func openXML(b *strings.Builder, name, ns, parentNS string) {
	b.WriteByte('<')
	b.WriteString(name)
	if ns != "" && ns != parentNS {
		b.WriteString(` xmlns="`)
		_ = xml.EscapeText(b, []byte(ns)) // strings.Builder never errors
		b.WriteString(`"`)
	}
	b.WriteByte('>')
}

// UnmarshalXMLStruct locates the first element matching s in data
// (at any depth, by local name, and by namespace when both sides
// declare one) and decodes it into v, a pointer to the schema's Go
// struct. Unknown child elements are skipped.
func UnmarshalXMLStruct(s *Schema, data []byte, v any) error {
	rv := reflect.ValueOf(v)
	if rv.Kind() != reflect.Pointer || rv.IsNil() || rv.Elem().Kind() != reflect.Struct {
		return errs.New().Code(ErrCodeValueParse).Msgf("decode target for %s must be a non-nil struct pointer", s.Name)
	}
	dec := xml.NewDecoder(bytes.NewReader(data))
	if err := findXMLElement(dec, s); err != nil {
		return err
	}
	return decodeXMLInto(dec, s, rv.Elem())
}

// DecodeXMLList decodes every element matching s in data into one
// Row per entry, in document order — the RowCodec.DecodeXML shape.
func DecodeXMLList[Row any](s *Schema, data []byte) ([]Row, error) {
	var rows []Row
	dec := xml.NewDecoder(bytes.NewReader(data))
	for {
		if err := findXMLElement(dec, s); err != nil {
			if errors.Is(err, io.EOF) {
				return rows, nil
			}
			return nil, err
		}
		var row Row
		rv := reflect.ValueOf(&row).Elem()
		if rv.Kind() != reflect.Struct {
			return nil, errs.New().Code(ErrCodeValueParse).Msgf("row type %T is not a struct", row)
		}
		if err := decodeXMLInto(dec, s, rv); err != nil {
			return nil, err
		}
		rows = append(rows, row)
	}
}

// findXMLElement advances dec past the next start element matching s
// at any depth. io.EOF signals no further match.
func findXMLElement(dec *xml.Decoder, s *Schema) error {
	for {
		tok, err := dec.Token()
		if err != nil {
			if errors.Is(err, io.EOF) {
				return io.EOF
			}
			return errs.From(err).Code(ErrCodeValueParse).Msgf("scan XML for %s", s.Name)
		}
		start, ok := tok.(xml.StartElement)
		if !ok {
			continue
		}
		if start.Name.Local != s.Name {
			continue
		}
		if s.Namespace != "" && start.Name.Space != "" && start.Name.Space != s.Namespace {
			continue
		}
		return nil
	}
}

// decodeXMLInto consumes the just-opened element's content,
// populating rv (a settable struct value) from its children per s.
func decodeXMLInto(dec *xml.Decoder, s *Schema, rv reflect.Value) error {
	for {
		tok, err := dec.Token()
		if err != nil {
			return errs.From(err).Code(ErrCodeValueParse).Msgf("decode %s", s.Name)
		}
		switch t := tok.(type) {
		case xml.EndElement:
			return nil
		case xml.StartElement:
			f := matchField(s, t.Name.Local, t.Name.Space)
			if f == nil {
				if err := dec.Skip(); err != nil {
					return errs.From(err).Code(ErrCodeValueParse).Msgf("skip unknown element %s", t.Name.Local)
				}
				continue
			}
			if err := decodeXMLField(dec, f, rv); err != nil {
				return err
			}
		default:
			// Character data between children (whitespace) and
			// comments are ignored.
		}
	}
}

// matchField finds the schema field for a child element name, by
// local name and, when both sides declare one, namespace.
func matchField(s *Schema, local, space string) *Field {
	for i := range s.Fields {
		f := &s.Fields[i]
		name := f.Name
		if f.Child != nil {
			name = f.Child.Name
		}
		if name != local {
			continue
		}
		ns := f.qualifiedNamespace(s)
		if ns != "" && space != "" && ns != space {
			continue
		}
		return f
	}
	return nil
}

// decodeXMLField decodes one child element into its struct field.
func decodeXMLField(dec *xml.Decoder, f *Field, rv reflect.Value) error {
	fv, err := fieldValue(rv, f)
	if err != nil {
		return err
	}
	switch {
	case f.Child != nil && f.List:
		entry := reflect.New(fv.Type().Elem()).Elem()
		if err := decodeXMLInto(dec, f.Child, entry); err != nil {
			return err
		}
		fv.Set(reflect.Append(fv, entry))
	case f.Child != nil:
		if fv.IsNil() {
			fv.Set(reflect.New(fv.Type().Elem()))
		}
		return decodeXMLInto(dec, f.Child, fv.Elem())
	case f.LeafList:
		text, err := elementText(dec)
		if err != nil {
			return errs.Wrapf(err, "leaf-list %s", f.Name)
		}
		entry := reflect.New(fv.Type().Elem()).Elem()
		if err := storeLeafText(f, text, entry); err != nil {
			return err
		}
		fv.Set(reflect.Append(fv, entry))
	default:
		text, err := elementText(dec)
		if err != nil {
			return errs.Wrapf(err, "leaf %s", f.Name)
		}
		return storeLeafText(f, text, derefForSet(fv))
	}
	return nil
}

// storeLeafText parses one leaf's canonical text under f's type and
// stores it.
func storeLeafText(f *Field, text string, target reflect.Value) error {
	val, err := ParseCanonical(*f.Type, text)
	if err != nil {
		return errs.Wrapf(err, "leaf %s", f.Name)
	}
	// The declared type drives storage: a union member resolves to a
	// member-typed Value that lands whole in the field.
	return valueToScalar(f.Type, val, target)
}

// elementText consumes the current element to its end tag and
// returns its concatenated character data, skipping any unexpected
// nested elements.
func elementText(dec *xml.Decoder) (string, error) {
	var b strings.Builder
	depth := 0
	for {
		tok, err := dec.Token()
		if err != nil {
			return "", errs.From(err).Code(ErrCodeValueParse).Msg("read leaf text")
		}
		switch t := tok.(type) {
		case xml.CharData:
			if depth == 0 {
				b.Write(t)
			}
		case xml.StartElement:
			depth++
		case xml.EndElement:
			if depth == 0 {
				return b.String(), nil
			}
			depth--
		}
	}
}
