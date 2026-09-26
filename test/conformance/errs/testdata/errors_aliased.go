package testdata

import e "errors"

func AliasedErrorsNew() error {
	return e.New("aliased new error")
}
