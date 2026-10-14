---
name: bento/freebsd-14
ecosystem: vagrant
required_by:
  - test/freebsd/Vagrantfile
criteria: run
verdict: keep
approved:
---

## Why it is required

`test/freebsd/Vagrantfile` boots this box so the FreeBSD-only tests run on a FreeBSD kernel, which the Linux and macOS hosts cannot provide. The `bpfdev` suite needs `bpf(4)` and `epair(4)`, and the `service` suite needs the FreeBSD build of the store lock. The Vagrantfile pins version `202510.26.0`.

## Why it is safe

Chef's Bento project publishes the box ([Vagrant catalogue](https://portal.cloud.hashicorp.com/vagrant/discover/bento/freebsd-14), [Bento repository](https://github.com/chef/bento)). The version pin keeps every host on one image. Observed on 2026-10-14, not from vendor documentation: the guest reports `freebsd-version` 14.3-RELEASE and `uname -p` `aarch64` under the `utm` provider. The guest only runs test binaries that the host built from this repository, and it mounts no host folder. Source and build recipe of the box not yet reviewed. Advisory lookup for the image not done.

## Why not owned code

Building a FreeBSD image is outside the repository's domain, and a FreeBSD kernel cannot be replaced by code in the repository.
