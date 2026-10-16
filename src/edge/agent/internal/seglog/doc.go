// Package seglog stores checksummed records in bounded segment files.
// Append order is preserved across segments and Open. Concurrent Append calls
// are ordered by the log's lock. One process owns a directory and one reader
// drains a log. The agent host opens separate ingest and telemetry logs,
// and internal/drain delivers their sealed records to central.
//
// For example, append a, b, c, then Close and Open the same directory.
// Next returns a segment containing a, b, c. Ack removes that file after all
// three records are settled. Until Ack, Next returns fresh copies of the same
// records, unless the byte or age bound has discarded the segment.
//
// # Disk layout
//
// Files are named with 16 lowercase hexadecimal sequence digits followed by
// .open for the active file or .seg for a sealed file. The next sequence is
// above every recognized name seen at Open, including removed damaged files.
// Sequences may be reused after reopen when higher files no longer exist.
// A sequence must therefore be acknowledged on the Log that returned it.
// The active file is created only when a record arrives, with mode 0600.
// Sealing closes and renames it before the reader can see its sealed name.
//
// The 20-byte file header is FSSL, little-endian uint16 version 1, uint16 zero,
// int64 creation time in Unix nanoseconds, and uint32 CRC-32C of those 16 bytes.
// Each record is a little-endian uint32 payload length, uint32 CRC-32C of the
// payload, and the nonempty payload. CRC-32C uses the Castagnoli polynomial.
// Append performs one write per record without a userspace write buffer.
// SyncInterval is checked on Append, and Close syncs the active file.
// Sealing and acknowledgement do not sync the directory. A power cut may lose
// a recent record or cause an acknowledged segment to be delivered again.
//
// # Bounds and recovery
//
// Append rolls a segment before the next record exceeds SegmentBytes or once
// its header reaches RollAge. With no sealed file, Next seals at Linger.
// Append and Next enforce MaxBytes and MaxAge by deleting whole sealed files
// in sequence order. Age uses the header, so a record may be discarded up to
// RollAge before its own MaxAge. Stats counts discards separately from Ack.
//
// Open scans every recognized file. A bad header is removed and counted as
// corrupt. A damaged sealed file retains its valid record prefix, and its
// rejected suffix contributes to CorruptBytes. Its bytes remain on disk until
// deletion. A torn open tail is truncated to its valid prefix, contributes to
// TruncatedBytes, and is sealed. Foreign names remain untouched. Recovery
// counters start anew at each Open. Files must not be modified by another
// process while the log is open.
package seglog
