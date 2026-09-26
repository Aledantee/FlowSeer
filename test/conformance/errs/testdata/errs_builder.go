package testdata

import "go.aledante.io/FlowSeer/src/common/errs"

func ErrsBuilder() error {
	return errs.New().Msg("builder error")
}
