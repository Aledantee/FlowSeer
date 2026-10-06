---
name: bcrypt
ecosystem: pypi
required_by:
  - deploy/lab/write-lab-secrets.py
criteria: run
verdict: keep
approved: 2026-10-06
---

## Why it is required

`deploy/lab/write-lab-secrets.py` writes a bcrypt hash of each lab user's password into `dex.env`, and the plan that introduces it ([phase 6 plan](../../../plans/2026-10-05-2216-refactor-uv-python-scripts-phase6-plan.md), Decisions) pins `bcrypt==5.0.0` in the script's metadata block. Python 3.13 has no bcrypt: `import crypt` fails with `No module named 'crypt'`. `cryptography` does not replace it, since its only use of the name is the optional `ssh` extra.

## Why it is safe

The publisher is the Python Cryptographic Authority, which the PyPI record names as the author ([PyPI record](https://pypi.org/pypi/bcrypt/5.0.0/json)). Version `5.0.0` was first uploaded at `2025-09-25T19:49:05Z` and was 376 days old on 2026-10-06. Its 14-day wait ended at `2025-10-09T19:49:05Z`. The OSV batch lookup dated 2026-10-06 returned no advisory for `bcrypt` at `5.0.0`. The package declares no runtime requirement. Its `tests` and `typecheck` extras are not installed. The script's dependency tree contains 4 versions with `cryptography`, and 0 of them are only reachable through this direct dependency. Source not yet reviewed.

## Why not owned code

FlowSeer would have to implement the bcrypt key schedule and cost handling in Python, which is password-hashing code outside the repository's domain. Dex rejects a hash below cost 10, so a home-made hash that gets the cost or the encoding wrong fails at login and not at generation.
