# stream

`stream.Spec` describes finite Ethernet traffic. A source returns frames one at a
time, so a consumer can use its own clock without expanding the whole stream.

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
needed. Bit-rate spacing uses the template frame's wire size, so a size sweep
can change the bits emitted in each interval. `UDPPortVariation` steps a source
or destination port in an IP/UDP
template. A UDP port change passes through `udp.Encode` and `ip.Header.Encode`
so both checksums are recomputed; it never patches a byte in place. The
template must decode as IP/UDP before `Source` can be constructed.

MAC and UDP variations can draw an offset from `SplitMix64` instead of stepping.
`Spec.Seed` sets the sequence, so two sources built from the same spec emit the
same bytes. A source clone copies the cursor and generator state. `Spec.Clone`
copies the variation list; the frame template and variation values, including
any `Sizes` slice, remain shared. Keep these values immutable while a source
uses them.

This package imports only Ethernet and other codec values under
`src/common/net` plus the Go standard library. It has no dependency on the
simulator's fabric or switch, so the same spec can drive an edge transmitter.
