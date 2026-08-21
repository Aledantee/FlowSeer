package yang

import "go.aledante.io/FlowSeer/src/common/errs"

// Error codes are the yang package's wire contract: append-only, never
// renamed, never reused for a different meaning. Generated bindings and
// the protocol libraries attach these codes so a decode failure keeps
// its identity across process boundaries.
var (
	// ErrCodePathParse marks a path string that does not parse in the
	// gNMI or RESTCONF form.
	ErrCodePathParse = errs.NewCode("yang/path-parse")
	// ErrCodeValueParse marks a leaf value whose lexical or JSON form
	// does not parse under its YANG type.
	ErrCodeValueParse = errs.NewCode("yang/value-parse")
	// ErrCodeUnionNoMatch marks a union value that no member type
	// accepts.
	ErrCodeUnionNoMatch = errs.NewCode("yang/union-no-match")
	// ErrCodeValueRange marks a numeric value outside its YANG type's
	// representable range.
	ErrCodeValueRange = errs.NewCode("yang/value-range")
)
