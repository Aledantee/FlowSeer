package testdata

import "errors"

func DirectErrorsNew() error {
	return errors.New("direct new error")
}
