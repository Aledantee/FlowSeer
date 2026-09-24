# pcap

`pcap.Reader` reads packet records from classic pcap (version 2.4, either byte
order, microsecond or nanosecond timestamps) and pcapng (Section Header,
Interface Description, and Enhanced Packet blocks). It returns the captured
bytes without assuming a link type. Callers that need Ethernet must check
`Record.LinkType == 1` before decoding `Record.Data`.

```go
func readCapture(path string) ([]pcap.Record, error) {
    file, err := os.Open(path)
    if err != nil {
        return nil, err
    }
    defer file.Close()

    reader, err := pcap.NewReader(file)
    if err != nil {
        return nil, err
    }
    var records []pcap.Record
    for {
        record, err := reader.Next()
        if err == io.EOF {
            return records, nil
        }
        if err != nil {
            return nil, err
        }
        records = append(records, record)
    }
}
```

`Next` returns `io.EOF` only at a record or block boundary. Truncated headers,
packet data, options, and trailers return errors. Captured packet data is
limited to 1 MiB before allocation; a nonzero snapshot length is enforced.
Unknown pcapng metadata blocks are skipped, while Simple Packet and obsolete
Packet blocks are refused because skipping their frames would understate
traffic.

`HasFCS` reports a nonzero FCS length declared by classic pcap header metadata,
pcapng interface options, or packet flags. A false value means no such metadata
declares an FCS. It does not prove the bytes are FCS-free, so a replay caller
must make that decision from the capture source it trusts.
