# netpen t2: live vendor lab

T2 injects OSPF from a Linux host and checks the IOS-XE target's own neighbor
table over SSH. The test passes only when router ID `10.0.0.99` appears after
injection and clears within the 40-second dead interval. The other seven
superset attacks are logged as pending and provide no vendor evidence.

Run the offline harness checks from the repository root:

```sh
go -C src/edge/netpen test -race -short -tags=netpen_t2 ./test/integration
```

The operator entry point is opt-in and never a gate. Configure the lab, then run
it from `src/edge/netpen`:

```sh
NETPEN_LAB_INJECTOR_HOST=172.16.0.21 \
NETPEN_LAB_INJECTOR_USER=aledante \
NETPEN_LAB_INJECTOR_PRIVATE_KEY="$HOME/.ssh/lab_ed25519" \
NETPEN_LAB_INJECTOR_HOST_KEY_SHA256='SHA256:...' \
NETPEN_LAB_INJECTOR_SUDO_PASSWORD='...' \
NETPEN_LAB_INJECTION_INTERFACE=eth0 \
NETPEN_LAB_TARGET_HOST=172.16.0.42 \
NETPEN_LAB_TARGET_USER=lab \
NETPEN_LAB_TARGET_PASSWORD='...' \
NETPEN_LAB_TARGET_HOST_KEY_SHA256='SHA256:...' \
NETPEN_LAB_TARGET_PLATFORM=iosxe \
task tier-t2
```

The injector must have the static `netpen` binary on `PATH`. Its SSH account
must be allowed to run that binary through `sudo`; the harness supplies the sudo
password on stdin and suppresses the prompt. The target must have an OSPF
interface in `10.0.0.0/24`, area 0, with broadcast network type, hello interval
10 seconds, dead interval 40 seconds, and no authentication. That interface and
the injector's `NETPEN_LAB_INJECTION_INTERFACE` must share a data segment.
Management SSH remains separate from that segment.

The test verifies that OSPF is active before injection and skips with a
prerequisite message when it is not. Missing environment variables also skip the
live tier and name the first missing variable. Authentication, command, finding,
and restoration failures fail the test. Record a passing evidence line by hand
in [the validation matrix](../VALIDATION_MATRIX.md); the test never edits it.
