package connecterr_test

import (
	"slices"
	"testing"

	connect "connectrpc.com/connect"

	"go.aledante.io/FlowSeer/src/common/errs"
	"go.aledante.io/FlowSeer/src/services/device/internal/auditapi"
	"go.aledante.io/FlowSeer/src/services/device/internal/connecterr"
	"go.aledante.io/FlowSeer/src/services/device/internal/deviceapi"
	"go.aledante.io/FlowSeer/src/services/device/internal/dispatchapi"
	"go.aledante.io/FlowSeer/src/services/device/internal/edgeapi"
)

// A code two services both return has to answer the same thing in both, or a
// caller learns a different answer for one condition depending which handler
// it reached — a disagreement that reads as deliberate and so is worse than an
// unmapped code. The tables are per-service because a single switch would have
// to import the packages that import it; this is what the split owes in
// exchange. The check lives here because this is the one package every table
// already depends on.
func TestNoCodeAnswersTwoDifferentThings(t *testing.T) {
	for _, conflict := range conflicts(map[string]connecterr.Table{
		"auditapi":    auditapi.ClientErrors,
		"deviceapi":   deviceapi.ClientErrors,
		"dispatchapi": dispatchapi.ClientErrors,
		"edgeapi":     edgeapi.ClientErrors,
	}) {
		t.Error(conflict)
	}
}

// The tables do share codes — deviceapi and dispatchapi both answer the
// journal's conflict, store, state, holds-full and decode codes, and agree on
// all five — so the check above passes on real data. What it cannot show is
// that it would fail on bad data, which is what this proves: two tables that
// disagree, one on the Connect code and one on the message.
func TestTheConsistencyCheckCatchesADisagreement(t *testing.T) {
	code := errs.NewCode("connecterrtest/shared")
	other := errs.NewCode("connecterrtest/shared-text")

	found := conflicts(map[string]connecterr.Table{
		"one": {
			code:  {Code: connect.CodeUnavailable, UserMsg: "retry"},
			other: {Code: connect.CodeNotFound, UserMsg: "no such edge"},
		},
		"two": {
			code:  {Code: connect.CodeInternal, UserMsg: "retry"},
			other: {Code: connect.CodeNotFound, UserMsg: "no such device"},
		},
	})
	if len(found) != 2 {
		t.Fatalf("caught %d disagreements, want 2: %v", len(found), found)
	}
}

// conflicts reports, in a stable order, every code two of the named tables map
// differently.
func conflicts(tables map[string]connecterr.Table) []string {
	type seen struct {
		table   string
		mapping connecterr.Mapping
	}
	first := map[errs.Code]seen{}
	var found []string

	for _, name := range sortedNames(tables) {
		for _, code := range sortedCodes(tables[name]) {
			mapping := tables[name][code]
			prior, ok := first[code]
			if !ok {
				first[code] = seen{table: name, mapping: mapping}
				continue
			}
			if prior.mapping != mapping {
				found = append(found, code.String()+": "+prior.table+" answers "+
					prior.mapping.Code.String()+" "+prior.mapping.UserMsg+"; "+name+" answers "+
					mapping.Code.String()+" "+mapping.UserMsg)
			}
		}
	}

	return found
}

func sortedNames(tables map[string]connecterr.Table) []string {
	names := make([]string, 0, len(tables))
	for name := range tables {
		names = append(names, name)
	}
	slices.Sort(names)

	return names
}

func sortedCodes(table connecterr.Table) []errs.Code {
	codes := make([]errs.Code, 0, len(table))
	for code := range table {
		codes = append(codes, code)
	}
	slices.Sort(codes)

	return codes
}
