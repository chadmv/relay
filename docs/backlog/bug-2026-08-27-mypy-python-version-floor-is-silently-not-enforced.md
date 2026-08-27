---
title: mypy silently ignores python_version = "3.9", so the SDK's declared type-check floor is not enforced in CI
type: bug
status: open
created: 2026-08-27
priority: low
source: Phase 4, all three review lenses, while fixing bug-2026-08-25-python-sdk-task-logs-iterates-envelope-keys
---

# The mypy 3.9 floor is warned about, not enforced

## Summary
`python/pyproject.toml` sets `python_version = "3.9"` under `[tool.mypy]`. mypy 2.x rejects that
value, prints a warning, and exits 0 - so the lint job is green while type-checking actually runs at
the default (3.13). `requires-python = ">=3.9"`, the classifiers, and the CI matrix all still claim
3.9.

## Repro / Symptoms
```
$ mypy --version
mypy 2.3.1 (compiled: yes)
$ mypy src
pyproject.toml: [mypy]: python_version: Python 3.9 is not supported (must be 3.10 or higher)
Success: no issues found in 7 source files
$ echo $?
0
```

CI's lint job installs unpinned `mypy>=1.10` on py3.13, so it resolves the same 2.x and gets the
same warning with the same green exit.

## Context
Nothing is broken today - `src/relay` was grepped for `match` and PEP 604 unions with zero hits, and
the suite was run on a real Python 3.9.5 interpreter (134 passed) during the slice above. So the
runtime half of the floor does hold; it is the TYPE half that is unverified.

Two mitigations already in place and worth not double-counting: the pytest matrix does include 3.9,
and `[tool.ruff] target-version = "py39"` is honoured. What is missing is type-level checking at the
floor, which is the lane that would catch a 3.10-only annotation before it reaches a 3.9 user.

## Proposal
Either pin `mypy>=1.10,<2` in the dev extra so the configured floor is honoured, or drop 3.9 from
`requires-python`, the classifiers and the matrix together. Do not leave the config claiming a floor
nothing enforces.

## Acceptance / Done When
- `mypy src` either type-checks at the declared floor, or the declared floor moves to what is
  actually checked.
- No gate passes while emitting a diagnostic about its own configuration.

## Related
- `python/pyproject.toml` `[tool.mypy]`, `requires-python`
- `.github/workflows/python.yml` - the lint job
