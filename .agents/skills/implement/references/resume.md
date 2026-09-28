# Resume from an existing ledger

Load this when `$(git rev-parse --git-dir)/flowseer-plan-status.json` exists
before the first edit.

A ledger naming another plan is replaced only after the user confirms
(`ledger.py init --force`).

A ledger naming this plan is read before the first edit, and every `passed`
commit is checked:

```bash
git merge-base --is-ancestor <commit> HEAD
```

Any non-zero exit, the one for an unknown object included, means not an
ancestor: report the unit, compare its files with the tree, record the
mismatch under the plan's Open questions, and ask the user before rewinding a
unit. Then continue from `resume`.
