package integration_test

import "testing"

const (
	openFGAImage  = "openfga/openfga:v1.21.0@sha256:2113c664a486b5da8d7a2cdab479e0d4e30639c80fd2c000540f645c1dbc1e55"
	postgresImage = "postgres:17@sha256:d74eeac9a635390a49bc21bd49fccd973de707e2a53a76ac49b552b8712ec46f"
)

func TestOpenFGAImageConstants(t *testing.T) {
	if openFGAImage == "" {
		t.Fatal("openFGAImage must not be empty")
	}
	if postgresImage == "" {
		t.Fatal("postgresImage must not be empty")
	}
}
