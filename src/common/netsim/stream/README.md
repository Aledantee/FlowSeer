# stream

`stream.Source` returns Ethernet frames one at a time at offsets from its own
start. A source can replay a capture or generate traffic from a `stream.Spec`.

## Replay a capture

Read the file completely before attaching it. `Source.Next` cannot return a
read error, so `NewCaptureSource` validates the records and snapshots their
bytes while errors can still be returned to the caller.

```go
func attachCapture(fab *fabric.Fabric, path string) error {
    file, err := os.Open(path)
    if err != nil {
        return err
    }
    defer file.Close()

    reader, err := pcap.NewReader(file)
    if err != nil {
        return err
    }
    var records []pcap.Record
    for {
        record, err := reader.Next()
        if err == io.EOF {
            break
        }
        if err != nil {
            return err
        }
        records = append(records, record)
    }
    source, err := stream.NewCaptureSource(records)
    if err != nil {
        return err
    }
    return fab.AttachStream(fabric.StreamAttachment{
        Origin: fabric.Endpoint{Node: "h1"},
        Source: source,
        Start: 0,
        Flow: 1,
    })
}
```

The first record maps to offset zero; later records retain their spacing and
file order. Source and destination MACs are preserved. At a host origin, the
fabric replaces captured VLAN tags with one tag for the host's VLAN, with
priority and DEI bits set to zero, or removes all tags if the host is untagged.
An added tag adds four wire octets; a removed tag saves four. A capture with a
non-Ethernet link type, a truncated frame, or an explicitly declared FCS is
refused before attachment. Without FCS metadata, the adapter assumes the
captured bytes exclude the FCS; it cannot infer that from packet bytes. The
source keeps all records in memory, so memory use scales with capture size.

## Generate traffic from a spec

```go
func firstFrame() (time.Duration, ethernet.Frame, error) {
    spec := stream.Spec{
        Frame: ethernet.Frame{Payload: make([]byte, 46)},
        Rate: stream.Rate{BitsPerSecond: 1_000_000_000},
        Burst: 10,
        Gap: time.Millisecond,
        Count: 20,
        Variations: []stream.Variation{
            stream.MACVariation{Field: stream.MACDestination, Step: 1, Count: 256},
        },
    }
    source, err := spec.Source()
    if err != nil {
        return 0, ethernet.Frame{}, err
    }
    at, frame, _ := source.Next() // first offset is zero
    return at, frame, nil
}
```

The first ten frames are 672 ns apart: an untagged minimum frame occupies 84
wire octets. The next burst begins at 1.00672 ms. `Next` returns offsets from
the stream's start; a consumer adds `Spec.Start` to its own epoch. It returns
`ok == false` after the count. `Clone` copies the current cursor, so advancing
one iterator does not advance the other.

Exactly one rate and one end (`Count` or `Duration`) must be set. `Normalize`
turns a duration into a ceiling count and defaults a zero burst to one. A frame
whose `Encode` fails, an invalid variation, negative burst or gap, and timing
beyond `time.Duration` are rejected by `Validate` and `Source`.

Variations run in list order on each frame, after the template. `MACVariation`
steps a source or destination address within 48 bits; `Count` is the number of
offsets before it repeats. `SizeVariation` cycles through sizes that include
the four-octet FCS. It adjusts the Ethernet payload length for the header and
each VLAN tag, preserving the original payload prefix and zero-padding when
needed. A size variation requires a frames-per-second rate because bit-rate
spacing cannot use the template's wire size for frames of varying sizes.
`UDPPortVariation` steps a source or destination port in an IP/UDP template.
A UDP port change passes through `udp.Encode` and `ip.Header.Encode`
so both checksums are recomputed; it never patches a byte in place. The
template must re-encode to an IP packet of the same length, with its UDP length
covering the full IP payload. Earlier size variations must leave the whole IP
packet intact.
It cannot follow a custom variation whose effect on the IP packet is unknown.
The port variation preserves bytes after the IP packet, including Ethernet
padding, and refuses fragmented IPv4 templates.

MAC and UDP variations can draw an offset from `SplitMix64` instead of stepping.
`Spec.Seed` sets the sequence, so two sources built from the same spec emit the
same bytes. A source clone copies the cursor and generator state. `Spec.Clone`
copies the variation list; the frame template and variation values, including
any `Sizes` slice, remain shared. Keep these values immutable while a source
uses them.

This package imports only Ethernet and other codec values under
`src/common/net` plus the Go standard library. It has no dependency on the
simulator's fabric or switch, so the same spec can drive an edge transmitter.
