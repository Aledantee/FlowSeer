package analysis

// Difference describes the first behavioral observable that differed between two
// evaluations, naming the observable and the values observed on both sides.
// Difference is a plain value that holds no shared state, so concurrent reads
// are safe as long as no goroutine writes its fields. The zero value describes
// no difference.
type Difference struct {
	Observable string
	Current    string
	Expected   string
}

// String returns a human-readable representation of the difference, or empty if none.
func (d Difference) String() string {
	if d.Observable == "" {
		return ""
	}
	return d.Observable + ": current=" + d.Current + ", expected=" + d.Expected
}
