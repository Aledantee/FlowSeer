---
name: cryptography
ecosystem: pypi
required_by:
  - deploy/lab/write-lab-secrets.py
criteria: run
verdict: keep
approved:
---

## Why it is required

`deploy/lab/write-lab-secrets.py` creates the lab's RSA keys and its CA and server certificates, and the plan that introduces it ([phase 6 plan](../../../plans/2026-10-05-2216-refactor-uv-python-scripts-phase6-plan.md), Decisions) pins `cryptography==50.0.1` in the script's metadata block. Python 3.13 has no module that creates a key or a signature: `dir(ssl)` holds no such name. The script is started through `uv run`, as the [repository scripting record](../../../architecture/2026-10-05-repository-scripting-direction.md) decides.

## Why it is safe

The publisher is the Python Cryptographic Authority, which the PyPI record names as the author ([PyPI record](https://pypi.org/pypi/cryptography/50.0.1/json)). Version `50.0.1` was first uploaded at `2026-08-25T19:44:03Z` and was 42 days old on 2026-10-06. Its 14-day wait ended at `2026-09-08T19:44:03Z`. Version `50.0.2` was uploaded on 2026-09-30, and its wait ends on 2026-10-14, so it is not taken. The OSV batch lookup dated 2026-10-06 returned no advisory for `cryptography` at `50.0.1`. The package requires `cffi>=2.0.0` on CPython, and `cffi` `2.1.1` requires `pycparser` `3.0`. The OSV lookup returned no advisory for either. The script's dependency tree contains 4 versions with `bcrypt`, and 2 of them are only reachable through this direct dependency. Source not yet reviewed.

## Why not owned code

FlowSeer would have to implement RSA key generation, X.509 certificate encoding, and signing. That is security-sensitive code outside the repository's domain. The standard library offers no replacement, and shelling out to `openssl` is what the script stopped doing.
