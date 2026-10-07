// Package openfga provides the OpenFGA client and authorization model for the
// device service.
package openfga

import (
	_ "embed"

	openfgav1 "github.com/openfga/api/proto/openfga/v1"
	"google.golang.org/protobuf/encoding/protojson"

	"go.aledante.io/FlowSeer/src/common/errs"
)

//go:embed model.json
var modelJSON []byte

// Model returns a fresh instance of the embedded OpenFGA authorization model.
func Model() (*openfgav1.AuthorizationModel, error) {
	return parseModel(modelJSON)
}

// parseModel parses an authorization model in protojson form, rejecting any
// unknown field so a misspelled member fails at load instead of reading as
// absent.
func parseModel(data []byte) (*openfgav1.AuthorizationModel, error) {
	var m openfgav1.AuthorizationModel
	opts := protojson.UnmarshalOptions{
		DiscardUnknown: false,
	}
	if err := opts.Unmarshal(data, &m); err != nil {
		return nil, errs.Wrap(err, "parse authorization model")
	}
	return &m, nil
}
