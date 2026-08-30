//go:build smi_corpus_full

package smi_test

import (
	"cmp"
	"runtime"
	"runtime/metrics"
	"slices"
	"testing"
	"time"
)

// The full tier reads the corpus as it is vendored, LANCOM's fifteen
// dropped LCOS releases included. It sits behind a build tag because
// those releases are near-copies that roughly double the bytes and the
// wall clock for coverage the deduplicated run already has, which is a
// trade worth making at merge time and not on every save.
//
// Run it with:
//
//	go test -tags=smi_corpus_full -run TestCorpus ./src/common/smi/

// widestSampleFiles is how many of the corpus's largest files it takes,
// per worker, to fill the pool several times over. A run over that
// sample is what the whole corpus is compared against, since if peak
// memory tracks the widest file then reading thirty-five times as many
// files should cost nothing extra.
const widestSampleFiles = 4

// peakGrowthHeadroom is how much higher the whole corpus's peak live
// heap may sit than the widest sample's. Anything above it means peak
// memory is following the file count, which is the failure this bound
// exists to catch; the slack is there because a peak sampled off a
// running collector is not a repeatable number to three digits.
const peakGrowthHeadroom = 2

// retentionHeadroom is how much of the largest file may still be live
// once the run is over and the collector has run. Anything above it
// means the harness kept a model, a source buffer or a per-file record
// alive across files.
const retentionHeadroom = 4

// TestCorpusFullReadsEveryFile loads every file the corpus ships,
// including the LCOS releases the default run collapses.
func TestCorpusFullReadsEveryFile(t *testing.T) {
	if testing.Short() {
		t.Skip("reads the whole corpus")
	}

	files := corpusFiles(t)

	dropped := len(files) - len(deduplicated(files))
	if dropped == 0 {
		t.Error("the full tier read the same files as the default tier, so it is checking nothing extra")
	}

	start := time.Now()
	byVendor, overall := scanCorpus(t, files, 0)
	t.Logf("loaded %d files (%.1f MB, %d of them dropped by the default tier) in %v across %d workers",
		overall.files, float64(overall.bytes)/(1<<20), dropped, time.Since(start), runtime.GOMAXPROCS(0))
	t.Logf("%d modules, %d declarations, %d unresolved, %d diagnostics, %d files with a fatal",
		overall.modules, overall.nodes, overall.unresolved, overall.diagnostics, overall.fatalFiles)

	if overall.files != len(files) {
		t.Errorf("counted %d of the %d files under %s", overall.files, len(files), corpusRoot)
	}
	for vendor, stats := range byVendor {
		if stats.files == 0 {
			t.Errorf("%s contributed no files", vendor)
		}
	}
}

// TestCorpusFullPeakMemoryTracksTheWidestFiles requires the whole
// corpus to peak no higher than a run over the handful of largest files
// alone, and the heap to fall back to where it started once the run is
// over.
//
// A harness that collected module sets as it went would pass every other
// test here and then fail on the machine of whoever next doubles the
// corpus. Reading a hundred and sixty-seven megabytes of MIB inside a
// test is only possible because nothing survives the file it came from,
// so that is measured rather than assumed.
//
// The measurement is live heap rather than allocated bytes. A dozen
// workers allocate faster than the collector frees, so allocated bytes
// track the allocation rate and say nothing about what the run is
// holding, which is the question being asked.
func TestCorpusFullPeakMemoryTracksTheWidestFiles(t *testing.T) {
	if testing.Short() {
		t.Skip("reads the whole corpus twice")
	}

	files := corpusFiles(t)

	widest := slices.Clone(files)
	slices.SortFunc(widest, func(a, b corpusFile) int { return cmp.Compare(b.size, a.size) })
	widest = widest[:min(len(widest), widestSampleFiles*runtime.GOMAXPROCS(0))]

	samplePeak, _ := peakLiveHeap(func() { scanCorpus(t, widest, 0) })
	fullPeak, retained := peakLiveHeap(func() { scanCorpus(t, files, 0) })

	var largest int64
	for _, cf := range files {
		largest = max(largest, cf.size)
	}

	t.Logf("peak live heap: %.1f MB over the %d widest files, %.1f MB over all %d, largest file %.1f MB",
		float64(samplePeak)/(1<<20), len(widest), float64(fullPeak)/(1<<20), len(files),
		float64(largest)/(1<<20))

	if fullPeak > samplePeak*peakGrowthHeadroom {
		t.Errorf("reading %d files peaked at %.1f MB against %.1f MB for the %d widest alone, "+
			"so peak memory is following the file count rather than the file size",
			len(files), float64(fullPeak)/(1<<20), float64(samplePeak)/(1<<20), len(widest))
	}
	if retained > uint64(largest)*retentionHeadroom {
		t.Errorf("%.1f MB was still live after the run and a collection, which is more than the largest file "+
			"— the run is accumulating rather than folding",
			float64(retained)/(1<<20))
	}
}

// peakLiveHeap runs work and returns the highest live heap seen while it
// ran, along with what was still live afterwards.
func peakLiveHeap(work func()) (peak, retained uint64) {
	live := []metrics.Sample{{Name: "/gc/heap/live:bytes"}}

	runtime.GC()
	metrics.Read(live)
	before := live[0].Value.Uint64()

	done := make(chan struct{})
	peaks := make(chan uint64, 1)
	go func() {
		sample := []metrics.Sample{{Name: "/gc/heap/live:bytes"}}
		var high uint64
		tick := time.NewTicker(time.Millisecond)
		defer tick.Stop()

		for {
			select {
			case <-done:
				peaks <- high

				return
			case <-tick.C:
				metrics.Read(sample)
				high = max(high, sample[0].Value.Uint64())
			}
		}
	}()

	work()

	close(done)
	peak = <-peaks

	runtime.GC()
	metrics.Read(live)
	after := live[0].Value.Uint64()

	return peak, after - min(after, before)
}
