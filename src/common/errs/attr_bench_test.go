package errs

import (
	"fmt"
	"testing"
)

func BenchmarkAttributesDeepChain(b *testing.B) {
	for _, depth := range []int{100, 1000} {
		b.Run(fmt.Sprint(depth), func(b *testing.B) {
			err := New().Attr("origin", true).Msg("origin")
			for range depth {
				err = &Error{causes: []error{err}}
			}
			for b.Loop() {
				Attributes(err)
			}
		})
	}
}
