package yang

import (
	"reflect"

	"go.aledante.io/FlowSeer/src/common/errs"
)

// scalar.go maps generated Go field representations onto [Value].
// The generated representation per kind: the signed and unsigned
// integer widths use the matching Go integer; boolean uses bool;
// string, enumeration (as a generated named string type), and
// instance-identifier use string kinds; bits uses []string; binary
// uses []byte; identityref uses [Identity]; decimal64 and union use
// [Value] itself; empty uses bool (present).

// scalarToValue converts one dereferenced Go scalar to a typed Value.
func scalarToValue(t *Type, rv reflect.Value) (Value, error) {
	switch t.Kind {
	case TypeInt8, TypeInt16, TypeInt32, TypeInt64:
		return Value{Type: *t, Int: rv.Int()}, nil
	case TypeUint8, TypeUint16, TypeUint32, TypeUint64:
		return Value{Type: *t, Uint: rv.Uint()}, nil
	case TypeBool:
		return Value{Type: *t, Bool: rv.Bool()}, nil
	case TypeString, TypeEnum, TypeInstanceID:
		return Value{Type: *t, String: rv.String()}, nil
	case TypeBits:
		bits, ok := rv.Interface().([]string)
		if !ok {
			return Value{}, errs.Msgf("bits leaf has Go type %s, want []string", rv.Type())
		}
		return Value{Type: *t, Bits: bits}, nil
	case TypeBinary:
		raw, ok := rv.Interface().([]byte)
		if !ok {
			return Value{}, errs.Msgf("binary leaf has Go type %s, want []byte", rv.Type())
		}
		return Value{Type: *t, Bytes: raw}, nil
	case TypeIdentityRef:
		id, ok := rv.Interface().(Identity)
		if !ok {
			return Value{}, errs.Msgf("identityref leaf has Go type %s, want yang.Identity", rv.Type())
		}
		return Value{Type: *t, Identity: id}, nil
	case TypeDecimal64, TypeUnion:
		val, ok := rv.Interface().(Value)
		if !ok {
			return Value{}, errs.Msgf("%s leaf has Go type %s, want yang.Value", t.Kind, rv.Type())
		}
		return val, nil
	case TypeEmpty:
		return Value{Type: *t}, nil
	default:
		return Value{}, errs.Msgf("cannot convert a %s leaf", t.Kind)
	}
}

// valueToScalar stores a typed Value into a settable dereferenced Go
// scalar of the generated representation.
func valueToScalar(t *Type, val Value, target reflect.Value) error {
	switch t.Kind {
	case TypeInt8, TypeInt16, TypeInt32, TypeInt64:
		if target.Kind() < reflect.Int || target.Kind() > reflect.Int64 {
			return errs.Msgf("%s leaf has Go type %s, want a signed integer", t.Kind, target.Type())
		}
		target.SetInt(val.Int)
	case TypeUint8, TypeUint16, TypeUint32, TypeUint64:
		if target.Kind() < reflect.Uint || target.Kind() > reflect.Uint64 {
			return errs.Msgf("%s leaf has Go type %s, want an unsigned integer", t.Kind, target.Type())
		}
		target.SetUint(val.Uint)
	case TypeBool:
		target.SetBool(val.Bool)
	case TypeString, TypeEnum, TypeInstanceID:
		if target.Kind() != reflect.String {
			return errs.Msgf("%s leaf has Go type %s, want a string kind", t.Kind, target.Type())
		}
		target.SetString(val.String)
	case TypeBits:
		return assign(target, val.Bits, t)
	case TypeBinary:
		return assign(target, val.Bytes, t)
	case TypeIdentityRef:
		return assign(target, val.Identity, t)
	case TypeDecimal64, TypeUnion:
		return assign(target, val, t)
	case TypeEmpty:
		target.SetBool(true)
	default:
		return errs.Msgf("cannot store a %s leaf", t.Kind)
	}
	return nil
}

// assign sets target to v, requiring exact assignability.
func assign(target reflect.Value, v any, t *Type) error {
	rv := reflect.ValueOf(v)
	if !rv.Type().AssignableTo(target.Type()) {
		return errs.Msgf("%s leaf has Go type %s, want %s", t.Kind, target.Type(), rv.Type())
	}
	target.Set(rv)
	return nil
}

// derefForSet returns the settable scalar behind fv, allocating the
// pointee of a nil pointer field first.
func derefForSet(fv reflect.Value) reflect.Value {
	if fv.Kind() == reflect.Pointer {
		if fv.IsNil() {
			fv.Set(reflect.New(fv.Type().Elem()))
		}
		return fv.Elem()
	}
	return fv
}
