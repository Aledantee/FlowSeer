package foreign

// NewCode here belongs to this package, not to errs.
func NewCode(name string) string { return name }

var NotAnErrsCode = NewCode("NOT A CODE AT ALL")
