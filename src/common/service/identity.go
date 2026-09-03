package service

import (
	"fmt"
	"regexp"
)

const maxIdentityLength = 128

var identityPattern = regexp.MustCompile(`^[a-z][a-z0-9]*(?:_[a-z0-9]+)*$`)

// Identity names one service independently of any process incarnation.
// Name and Namespace are lower-snake-case path segments. Version is an
// opaque release identifier. The zero value is invalid.
type Identity struct {
	// Name is the service's stable logical root path segment.
	Name string
	// Namespace separates independently named service families.
	Namespace string
	// Version identifies the running release in logs and telemetry.
	Version string
}

func validateIdentity(identity Identity) error {
	if err := validateIdentitySegment("name", identity.Name); err != nil {
		return err
	}
	if err := validateIdentitySegment("namespace", identity.Namespace); err != nil {
		return err
	}
	if identity.Version == "" {
		return fmt.Errorf("service version is empty")
	}
	if len(identity.Version) > maxIdentityLength {
		return fmt.Errorf("service version exceeds %d bytes", maxIdentityLength)
	}

	return nil
}

func validateIdentitySegment(label, value string) error {
	if !identityPattern.MatchString(value) {
		return fmt.Errorf("service %s %q is not lower snake case", label, value)
	}
	if len(value) > maxIdentityLength {
		return fmt.Errorf("service %s exceeds %d bytes", label, maxIdentityLength)
	}

	return nil
}
