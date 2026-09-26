package testdata

import "fmt"

func Direct() error {
	return fmt.Errorf("direct failure")
}
