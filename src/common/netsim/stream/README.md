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
whose `Encode` fails, negative burst or gap, and timing beyond `time.Duration`
are rejected by `Validate` and `Source`. The frame template and its slices are
shared across spec copies and sources; callers keep them immutable while any
source uses them.

This package imports only Ethernet and other codec values under
`src/common/net` plus the Go standard library. It has no dependency on the
simulator's fabric or switch, so the same spec can drive an edge transmitter.
