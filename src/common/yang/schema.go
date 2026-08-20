package yang

import (
	"reflect"

	"go.aledante.io/FlowSeer/src/common/errs"
)

// Schema describes one YANG container or list node and how it maps
// onto a generated Go struct: the node's module qualification, its
// key leaves, and one [Field] per child. yanggen emits one Schema
// value per struct; the generic codecs in this package encode and
// decode any conforming struct from a Schema, which is what lets one
// struct drive all three wire forms without per-field generated
// codec code.
//
// A Schema and its Fields are immutable after construction and safe
// for concurrent use.
type Schema struct {
	// Module is the defining module's name; Namespace its XML
	// namespace URI.
	Module    string
	Namespace string
	// Name is the node's local name in the data tree.
	Name string
	// Presence marks a presence container: the container's existence
	// is itself data (a nil struct pointer means absent).
	Presence bool
	// Keys lists a list node's key leaf names in YANG `key` order.
	// Empty for containers.
	Keys []string
	// Fields are the node's children in schema order.
	Fields []Field
}

// Field maps one child node onto a Go struct field. Exactly one of
// Type (scalar leaf / leaf-list) or Child (container / nested list)
// is set.
type Field struct {
	// GoName is the Go struct field's name.
	GoName string
	// Name is the YANG node name.
	Name string
	// Module and Namespace are set only when the child belongs to a
	// different module than its parent (augmented-in nodes); empty
	// means inherit the parent Schema's.
	Module    string
	Namespace string
	// Type is the leaf's resolved YANG type. Nil for containers and
	// nested lists.
	Type *Type
	// LeafList marks a leaf-list: the Go field is a slice of the
	// scalar representation.
	LeafList bool
	// Child is the nested node's schema. The Go field is a struct
	// pointer for a container, a struct slice for a list.
	Child *Schema
	// List marks Child as a list (Go slice) rather than a container
	// (Go pointer).
	List bool
}

// qualifiedModule returns the module owning f within s.
func (f *Field) qualifiedModule(s *Schema) string {
	if f.Module != "" {
		return f.Module
	}
	return s.Module
}

// qualifiedNamespace returns the XML namespace owning f within s.
func (f *Field) qualifiedNamespace(s *Schema) string {
	if f.Namespace != "" {
		return f.Namespace
	}
	return s.Namespace
}

// structValue unwraps v (a struct, struct pointer, or reflect-able
// value of the schema's Go type) to an addressable-or-not struct
// value.
func structValue(v any) (reflect.Value, error) {
	rv := reflect.ValueOf(v)
	for rv.Kind() == reflect.Pointer {
		if rv.IsNil() {
			return reflect.Value{}, errs.Msg("nil struct pointer")
		}
		rv = rv.Elem()
	}
	if rv.Kind() != reflect.Struct {
		return reflect.Value{}, errs.Msgf("value is %s, want a struct", rv.Kind())
	}
	return rv, nil
}

// fieldValue resolves f's Go field on rv.
func fieldValue(rv reflect.Value, f *Field) (reflect.Value, error) {
	fv := rv.FieldByName(f.GoName)
	if !fv.IsValid() {
		return reflect.Value{}, errs.Msgf("struct %s has no field %s", rv.Type(), f.GoName)
	}
	return fv, nil
}
