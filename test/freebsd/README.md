---
name: FreeBSD VM tests
last_updated: 2026-10-14
---

# FreeBSD VM tests

Tests that need a FreeBSD kernel run in a Vagrant guest. The guest holds no Go
toolchain. The host cross-compiles each suite's test binary, uploads it, and
runs it as root.

```text
uv run tools/scripts/run.py freebsd vmtest --suite bpfdev
uv run tools/scripts/run.py freebsd vmtest --suite all
```

## Guest

The `Vagrantfile` uses `bento/freebsd-14` pinned to `202510.26.0`. Chef's
Bento project publishes the box, see
[its dependency statement](../../docs/dependencies/statements/vagrant/bento-freebsd-14.md).
The catalogue lists arm64 builds for the `virtualbox`, `utm`, `parallels`, and
`vmware_desktop` providers. `vmtest` passes no `--provider`, so Vagrant's
default applies. A host can set `VAGRANT_DEFAULT_PROVIDER`.

Observed on 2026-10-14 on a macOS arm64 host with Vagrant 2.4.9, provider
`utm` through the `vagrant_utm` 0.2.0 plugin, and UTM installed from the
Homebrew cask:

- The guest is 14.3-RELEASE (`freebsd-version`), `uname -p` is `aarch64`, and
  `uname -m` is `arm64`. The catalogue also lists `bento/freebsd-14.3` at the
  same version.
- `net.bpf.maxbufsize` is 524288, and `tcpdump` and `sudo` are installed. User
  `vagrant` is in `wheel`.
- `vagrant up` failed with `utmctl` "OSStatus error -609" until UTM.app was
  running. Open UTM.app first.
- `vagrant-parallels` refused Parallels Desktop Standard, which it accepts
  only as Pro or Business.

## First `vagrant up`

The first `vagrant up` downloads the box into the host's Vagrant box cache,
and the first boot takes longer than later ones. `vmtest` runs `vagrant up`
when `vagrant status` does not report `running`, so a plain `vmtest` run
starts the machine. The machine stays up between runs. Stop it with
`vagrant halt` from this directory.

## Suites

The table `SUITES` in `tools/scripts/freebsd/vmtest.py` lists them. Each suite
builds with `CGO_ENABLED=0 GOOS=freebsd` and the `GOARCH` that the guest's
`uname -p` maps to, then runs as root. A suite with a setup script has it
run with `up` before the binary and with `down` after, including after a
failed run.

| Suite | Package | Tag | Setup | Runs |
| --- | --- | --- | --- | --- |
| `bpfdev` | `./src/common/net/bpfdev` | `bpfdev_vm` | `bpfdev-setup.sh` | every test, on `epair0a` and `epair0b` |
| `service` | `./src/common/service` | none | none | `TestStartLocalBusIsListenerFreeDurableAndExclusive` |

`bpfdev-setup.sh` creates `epair0` and brings both ends up with `inet6
ifdisabled`, so no MLD report or neighbour solicitation adds to the counters
the tests assert on.
