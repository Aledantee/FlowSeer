package netsimtest

import (
	"math/rand/v2"
	"testing"

	"go.aledante.io/FlowSeer/src/common/netsim/analysis"
)

func TestComparisonCorpusInvariants(t *testing.T) {
	corpus := ComparisonCorpus()
	if len(corpus) == 0 {
		t.Fatal("ComparisonCorpus returned no cases")
	}

	for _, tc := range corpus {
		t.Run(tc.Name, func(t *testing.T) {
			// 1. Current-first execution baseline
			resCur, err := tc.Execute(false, nil)
			if err != nil {
				t.Fatalf("current-first execution failed: %v", err)
			}
			if resCur.Disposition != tc.ExpectedDisposition {
				t.Errorf("current-first disposition = %v, want %v", resCur.Disposition, tc.ExpectedDisposition)
			}
			if resCur.Observable != tc.ExpectedObservable {
				t.Errorf("current-first observable = %q, want %q", resCur.Observable, tc.ExpectedObservable)
			}

			// 2. Candidate-first execution
			resCand, err := tc.Execute(true, nil)
			if err != nil {
				t.Fatalf("candidate-first execution failed: %v", err)
			}
			if resCand.Disposition != tc.ExpectedDisposition {
				t.Errorf("candidate-first disposition = %v, want %v", resCand.Disposition, tc.ExpectedDisposition)
			}
			if resCand.Observable != tc.ExpectedObservable {
				t.Errorf("candidate-first observable = %q, want %q", resCand.Observable, tc.ExpectedObservable)
			}

			// On Different, candidate-first swaps current and expected values.
			if tc.ExpectedDisposition == analysis.Different {
				if resCand.Current != resCur.Expected {
					t.Errorf("candidate-first Current = %q, want %q (current-first Expected)", resCand.Current, resCur.Expected)
				}
				if resCand.Expected != resCur.Current {
					t.Errorf("candidate-first Expected = %q, want %q (current-first Current)", resCand.Expected, resCur.Current)
				}
			}

			// 3. Non-consuming verification on the baseline entities
			if tc.VerifyNonConsuming != nil {
				if err := tc.VerifyNonConsuming(); err != nil {
					t.Errorf("non-consuming invariant violated: %v", err)
				}
			}

			// 4. Determinism under 10 randomized trials with shuffled map and slice order
			for trial := 0; trial < 10; trial++ {
				rng := rand.New(rand.NewPCG(uint64(trial+1), uint64(trial*31+17)))
				resShuffled, err := tc.Execute(false, rng)
				if err != nil {
					t.Fatalf("trial %d execution failed: %v", trial, err)
				}
				if resShuffled.Disposition != tc.ExpectedDisposition {
					t.Errorf("trial %d disposition = %v, want %v", trial, resShuffled.Disposition, tc.ExpectedDisposition)
				}
				if resShuffled.Observable != tc.ExpectedObservable {
					t.Errorf("trial %d observable = %q, want %q", trial, resShuffled.Observable, tc.ExpectedObservable)
				}
				if resShuffled.Current != resCur.Current {
					t.Errorf("trial %d current = %q, want %q", trial, resShuffled.Current, resCur.Current)
				}
				if resShuffled.Expected != resCur.Expected {
					t.Errorf("trial %d expected = %q, want %q", trial, resShuffled.Expected, resCur.Expected)
				}
			}
		})
	}
}
