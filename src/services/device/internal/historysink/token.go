package historysink

import (
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"slices"
)

// batchToken is the insert deduplication token of a batch: the SHA-256 of its
// stream sequences in ascending order. A token built from only the first and
// last sequence would collide for two batches with equal ends and different
// members, which two sink replicas sharing a consumer can produce, and the
// server would drop the second.
func batchToken(sequences []uint64) string {
	sorted := slices.Sorted(slices.Values(sequences))
	h := sha256.New()
	var buf [8]byte
	for _, seq := range sorted {
		binary.BigEndian.PutUint64(buf[:], seq)
		h.Write(buf[:])
	}
	return hex.EncodeToString(h.Sum(nil))
}
