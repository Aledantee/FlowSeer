// Package drain delivers the records of a [seglog.Log] to central. One drain
// serves one log: it takes the oldest segment, sends the records still
// unsettled in one call, advances by the count central settled, and
// acknowledges the segment once every record is settled.
//
// The resume offset lives in memory, keyed to the segment's sequence. A
// restart resends a partly settled segment from its first record, and so does
// a segment the log's byte or age bound deleted between two attempts, since
// the next sequence starts at zero. Delivery is at least once. A failed call
// never drops a record. Only the log's bounds discard.
//
// For example, a segment of five records answered with a settled count of two
// is followed by a call with records three to five, and Ack runs once after
// that call settles them.
package drain
