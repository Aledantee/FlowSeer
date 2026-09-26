package testdata

import . "errors"

func DotImportErrorsNew() error {
	return New("dot-imported new error")
}
