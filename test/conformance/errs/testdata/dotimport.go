package testdata

import . "fmt"

func DotImport() error {
	return Errorf("dot-imported failure")
}
