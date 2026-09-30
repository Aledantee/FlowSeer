package yang

import (
	"reflect"

	"go.aledante.io/FlowSeer/src/common/errs"
)

// Module identifies a YANG module by its name and XML namespace URI.
// Modules are immutable after construction and safe for concurrent use.
type Module struct {
	Name      string
	Namespace string
}

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
	// Module is the defining module.
	Module *Module
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

// moduleName returns s's defining module name, or "" if s or s.Module is nil.
func (s *Schema) moduleName() string {
	if s == nil || s.Module == nil {
		return ""
	}
	return s.Module.Name
}

// namespaceURI returns s's XML namespace URI, or "" if s or s.Module is nil.
func (s *Schema) namespaceURI() string {
	if s == nil || s.Module == nil {
		return ""
	}
	return s.Module.Namespace
}

// Field maps one child node onto a Go struct field. Exactly one of
// Type (scalar leaf / leaf-list) or Child (container / nested list)
// is set.
type Field struct {
	// GoName is the Go struct field's name.
	GoName string
	// Name is the YANG node name.
	Name string
	// Module is set only when the child belongs to a different module
	// than its parent (augmented-in nodes); nil means inherit the
	// parent Schema's module.
	Module *Module
	// Type is the leaf's resolved YANG type. Nil for containers and
	// nested lists.
	Type *Type
	// LeafList marks a leaf-list: the Go field is a slice of the
	// scalar representation.
	LeafList bool
	// Child is the nested node's schema. The Go field is a struct
	// pointer for a container, a struct slice for a list.
	Child *Schema
	// Group marks Child as a module group. A group has no wire element
	// of its own. Its fields are encoded at the parent level under
	// Child.Module's namespace. Group fields use a pointer to Child's
	// struct, and Child.Name is empty.
	Group bool
	// List marks Child as a list (Go slice) rather than a container
	// (Go pointer).
	List bool
}

// qualifiedModule returns the module owning f within s.
func (f *Field) qualifiedModule(s *Schema) string {
	if f != nil && f.Module != nil {
		return f.Module.Name
	}
	return s.moduleName()
}

// qualifiedNamespace returns the XML namespace owning f within s.
func (f *Field) qualifiedNamespace(s *Schema) string {
	if f != nil && f.Module != nil {
		return f.Module.Namespace
	}
	return s.namespaceURI()
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

// walkFields yields a schema's fields in wire order. Group fields are omitted
// and their children are yielded with the group schema and field that owns
// their namespace.
func walkFields(s *Schema, yield func(field *Field, owner *Schema, group *Field) error) error {
	if s == nil {
		return errs.Msg("nil schema")
	}
	if yield == nil {
		return errs.Msg("nil field visitor")
	}

	for i := range s.Fields {
		field := &s.Fields[i]
		if !field.Group {
			if err := yield(field, s, nil); err != nil {
				return err
			}
			continue
		}
		if field.Child == nil {
			return errs.Msgf("group field %s has no child schema", fieldLabel(field))
		}
		for j := range field.Child.Fields {
			child := &field.Child.Fields[j]
			if child.Group {
				return errs.Msgf("group field %s contains nested group field %s", fieldLabel(field), fieldLabel(child))
			}
			if err := yield(child, field.Child, field); err != nil {
				return err
			}
		}
	}
	return nil
}

// groupValue returns the struct behind group on rv. A nil group is invalid
// when alloc is false and is allocated when alloc is true.
func groupValue(rv reflect.Value, group *Field, alloc bool) (reflect.Value, error) {
	for rv.IsValid() && rv.Kind() == reflect.Pointer {
		if rv.IsNil() {
			return reflect.Value{}, errs.Msg("nil struct pointer")
		}
		rv = rv.Elem()
	}
	if !rv.IsValid() || rv.Kind() != reflect.Struct {
		return reflect.Value{}, errs.Msg("group value is not a struct")
	}

	fv, err := fieldValue(rv, group)
	if err != nil {
		return reflect.Value{}, err
	}
	if fv.Kind() != reflect.Pointer || fv.Type().Elem().Kind() != reflect.Struct {
		return reflect.Value{}, errs.Msgf("group field %s is not a struct pointer", fieldLabel(group))
	}
	if fv.IsNil() {
		if !alloc {
			return reflect.Value{}, nil
		}
		if !fv.CanSet() {
			return reflect.Value{}, errs.Msgf("group field %s is not settable", fieldLabel(group))
		}
		fv.Set(reflect.New(fv.Type().Elem()))
	}
	return fv.Elem(), nil
}

// fieldLabel returns the Go field name used in schema diagnostics.
func fieldLabel(f *Field) string {
	if f == nil {
		return "<nil>"
	}
	if f.GoName != "" {
		return f.GoName
	}
	if f.Name != "" {
		return f.Name
	}
	return "<unnamed>"
}
