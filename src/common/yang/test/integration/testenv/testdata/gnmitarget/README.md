# gNMI fixture target

Run the standalone server from this directory:

```sh
go run . -addr 127.0.0.1:9339
```

The server starts with `edge-1:8080` and `edge-2:9090` under
`/servers/server[name]/port`. It uses plaintext gRPC for the local integration
environment and advertises the `fixture-main` model with JSON_IETF values.
State is in memory and resets when the process restarts.

Get and Subscribe match modern PathElem paths, including request prefixes and
literal list keys. A path ending at a container or list selects its descendants;
an omitted list key selects all rows. Responses contain absolute paths. An empty
path selects the dataset. Wildcard expressions and legacy string paths are not
implemented.

Set supports JSON_IETF integer port updates/replacements in `1..65535` and
deletion of an entire named server row. A request applies deletes, replacements,
then updates as one transaction. Invalid paths or values reject the whole
request without changing rows; union-replace returns Unimplemented. For example,
a single request deleting `edge-1`, replacing its port with `3333`, then updating
it to `4444` leaves its port at `4444`.

Subscribe supports ONCE and STREAM; other modes return Unimplemented. It sends a
matching snapshot and a sync marker, or only the sync marker when updates-only
is requested. Every STREAM subscriber increments the shared `edge-1` port once
per second and emits the update only if its requested paths include that leaf.
The port wraps from `65535` to `1`. A deleted `edge-1` stays absent. This synthetic
change source exercises client watchers; it does not implement sample intervals
or per-subscription gNMI change modes.

The fixture tests call handlers in memory, including a cancellable subscription
stream, so they need neither Docker nor a listening socket:

```sh
go test -race ./...
go build -o /tmp/gnmitarget .
```
