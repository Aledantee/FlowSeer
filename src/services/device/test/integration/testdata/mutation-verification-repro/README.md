# Reproduction: a mutation applies and is never verified

These are not tests. They are the three files that reproduce the defect, kept
under `testdata/` so the Go tool ignores them and nothing tries to run them,
and with a `.repro` suffix so nothing mistakes one for a test that was
disabled. To use them, copy the three over their namesakes in the parent
directory and run `TestAnAppliedDescriptionReachesTheDeviceAndComesBack`.

They differ from the committed fixture in three ways, each of which earned its
place while the defect was being found:

- `e2e_test.go.repro` adds the apply scenario, and a sampler that prints the
  device's session and command counts every ten seconds. The sampler is what
  showed the counts flat across a hundred seconds.
- `device_test.go.repro` records every shell read as well as every write, so
  the device's whole traffic is visible. That is what showed there is no read
  after the write.
- `fixture_test.go.repro` runs central at `LOG_LEVEL_DEBUG`. Central grades a
  refused call's reason at DEBUG, so this is the difference between seeing
  that a call failed and seeing why.

What they print against the defect:

```
commands=[READ(ethernet 1/1/1)->as found   ethernet 1/1/1=uplink to core]
t+10s: sessions acquired=4 shell ops=2
...
t+1m40s: sessions acquired=4 shell ops=2

rpc.method: flowseer.edge.attach.v1.EdgeService/AcquireReadCredential
rpc.response.status_code: invalid_argument
error: request fails its schema rules: access_policy: value is required
```

A caution about the second line, because it misled the investigation once. The
counter labelled `sessions acquired` counts SNMP *session opens*, not
credential acquisitions from central. A flat count therefore does not mean the
lane's `Read` closure was never entered — it means no session was opened,
which is also what happens when the closure is entered and fails at the
acquisition before reaching a session. The central-side log is what settled
it.
