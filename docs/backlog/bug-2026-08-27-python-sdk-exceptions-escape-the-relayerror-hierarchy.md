---
title: Python SDK decode failures escape the RelayError hierarchy that the README says is exhaustive
type: bug
status: open
created: 2026-08-27
priority: medium
source: Spec D8 plus the Phase 4 security lens, while fixing bug-2026-08-25-python-sdk-task-logs-iterates-envelope-keys
---

# SDK decode failures escape the `RelayError` hierarchy

## Summary
`python/README.md` states that all exceptions descend from `relay.RelayError`. Three classes of
server-driven failure do not, at roughly thirteen `response.json()` call sites plus every
`model_validate`. A caller who writes `except RelayError` around SDK calls is not covered.

## Repro / Symptoms
Measured against mock 200 responses:

```
not JSON (an ingress HTML error page) -> json.decoder.JSONDecodeError   RelayError: False
204 no content                        -> json.decoder.JSONDecodeError   RelayError: False
integer over 4300 digits              -> builtins.ValueError            RelayError: False
body shape mismatch                   -> pydantic_core.ValidationError  RelayError: False
bare list where an object is expected -> builtins.TypeError             RelayError: False
missing "items" key                   -> builtins.KeyError              RelayError: False
```

The non-JSON case is the realistic one: a proxy or ingress returning an HTML error page with a 200,
which is the same "the client shows nothing useful" family the envelope-drift bug came from.

## Context
The README's error section has been corrected to name `pydantic.ValidationError`,
`json.JSONDecodeError` and `ValueError` as escaping, so the documentation is no longer wrong. This
item is the code half: making them catchable.

Declined during that slice because it is a cross-cutting wrap at every call site and folding it in
would have made the diff two features.

`ProtocolError`, added in that slice, does descend from `RelayError`, so the new code did not add to
the count.

## Proposal
One `_read_json(response)` chokepoint, the Python analogue of CLAUDE.md's single JSON entry point
invariant, wrapping decode and validation failures in a `RelayError` subclass while preserving the
original as `__cause__`. That also gives the natural home for a response-size bound - see the
related item, which measured a 343x gzip amplification against this same unbounded `.json()`.

## Acceptance / Done When
- Every documented SDK entry point raises only `RelayError` subclasses for a malformed or
  undecodable server response.
- The original exception is reachable via `__cause__`.
- The README's error table matches, and a test pins that a non-JSON 200 is catchable as
  `RelayError`.

## Related
- `python/src/relay/client.py` - roughly 13 `response.json()` sites
- `python/README.md` - Errors section
- [[bug-2026-08-26-relayclient-has-no-response-bound-and-no-client-timeout]] - the same chokepoint
  would carry the byte bound
- [[bug-2026-08-25-python-sdk-task-logs-iterates-envelope-keys]]
