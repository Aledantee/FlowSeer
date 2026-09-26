package testdata

import f "fmt"

func Aliased() error {
	return f.Errorf("aliased failure")
}
