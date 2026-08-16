package snmp

import (
	"errors"
	"fmt"
	"strings"
	"testing"
)

func TestPDUErrorStatus_String(t *testing.T) {
	// Spot-check that every named status maps to a non-default string.
	statuses := []PDUErrorStatus{
		NoError, TooBig, NoSuchName, BadValue, ReadOnly, GenErr,
		NoAccess, WrongType, WrongLength, WrongEncoding, WrongValue,
		NoCreation, InconsistentValue, ResourceUnavailable, CommitFailed,
		UndoFailed, AuthorizationError, NotWritable, InconsistentName,
	}
	for _, s := range statuses {
		if got := s.String(); strings.HasPrefix(got, "PDUErrorStatus(") {
			t.Errorf("status %d rendered as %q (no named mapping)", int(s), got)
		}
	}
	// Out-of-range falls through to the numeric form.
	if got := PDUErrorStatus(999).String(); !strings.HasPrefix(got, "PDUErrorStatus(") {
		t.Errorf("unknown status rendered as %q, want numeric form", got)
	}
}

func TestPDUError_ErrorMessage(t *testing.T) {
	oid, _ := ParseOID("1.3.6.1.2.1.1.5.0")
	e := &PDUError{Status: NoAccess, Index: 2, OID: oid}
	msg := e.Error()
	if !strings.Contains(msg, "noAccess") {
		t.Errorf("error message %q missing status name", msg)
	}
	if !strings.Contains(msg, "1.3.6.1.2.1.1.5.0") {
		t.Errorf("error message %q missing OID", msg)
	}
	if !strings.Contains(msg, "index 2") {
		t.Errorf("error message %q missing index", msg)
	}

	// No OID -> message omits the OID clause.
	bare := &PDUError{Status: GenErr, Index: 1}
	if strings.Contains(bare.Error(), "oid") {
		t.Errorf("bare PDUError error %q should not mention oid", bare.Error())
	}
}

func TestPDUError_Unwrap(t *testing.T) {
	e := &PDUError{Status: GenErr, Index: 1}
	if got := e.Unwrap(); got != nil {
		t.Errorf("Unwrap = %v, want nil (leaf error)", got)
	}
}

func TestPDUError_AsTypeWrapped(t *testing.T) {
	oid, _ := ParseOID("1.3.6.1")
	inner := &PDUError{Status: BadValue, Index: 3, OID: oid}
	wrapped := fmt.Errorf("set request failed: %w", inner)

	got, ok := errors.AsType[*PDUError](wrapped)
	if !ok {
		t.Fatal("errors.AsType[*PDUError] failed to extract wrapped PDUError")
	}
	if got != inner {
		t.Errorf("extracted = %p, want %p", got, inner)
	}
	if got.Status != BadValue || got.Index != 3 || !got.OID.Equal(oid) {
		t.Errorf("extracted PDUError content mismatch: %+v", got)
	}
}

func TestPDUError_JoinAggregate(t *testing.T) {
	oidA, _ := ParseOID("1.3.6.1.1")
	oidB, _ := ParseOID("1.3.6.1.2")
	a := &PDUError{Status: NoAccess, Index: 1, OID: oidA}
	b := &PDUError{Status: NotWritable, Index: 2, OID: oidB}
	joined := errors.Join(a, b)

	// AsType extracts the first matching child in depth-first order.
	got, ok := errors.AsType[*PDUError](joined)
	if !ok {
		t.Fatal("errors.AsType extracted no PDUError from join")
	}
	if got != a {
		t.Errorf("first extracted = %p, want %p", got, a)
	}

	// Iterating via Unwrap() []error must surface both leaves.
	u, ok := joined.(interface{ Unwrap() []error })
	if !ok {
		t.Fatal("errors.Join result does not implement Unwrap() []error")
	}
	children := u.Unwrap()
	if len(children) != 2 {
		t.Fatalf("Unwrap returned %d children, want 2", len(children))
	}
	found := map[*PDUError]bool{}
	for _, c := range children {
		pe, ok := errors.AsType[*PDUError](c)
		if !ok {
			t.Errorf("child %v not a PDUError", c)
			continue
		}
		found[pe] = true
	}
	if !found[a] || !found[b] {
		t.Errorf("not all PDUErrors found: %v", found)
	}
}

func TestErrException_IsSentinel(t *testing.T) {
	wrapped := fmt.Errorf("decode: %w", ErrException)
	if !errors.Is(wrapped, ErrException) {
		t.Error("errors.Is should match wrapped ErrException")
	}
	if errors.Is(wrapped, ErrTypeMismatch) {
		t.Error("ErrException must not match ErrTypeMismatch sentinel")
	}
}

func TestErrTypeMismatch_IsSentinel(t *testing.T) {
	wrapped := fmt.Errorf("decode: %w", ErrTypeMismatch)
	if !errors.Is(wrapped, ErrTypeMismatch) {
		t.Error("errors.Is should match wrapped ErrTypeMismatch")
	}
}
