---
title: get_tasks reads a raw response body, so a null or non-list body raises TypeError outside RelayError
type: bug
status: open
created: 2026-08-29
priority: medium
source: Phase 4 re-verify lens while fixing bug-2026-08-27-python-sdk-fetch-all-has-no-termination-stops
---

# `get_tasks` reads a raw response body

## Summary
`Client.get_tasks` does `[Task.model_validate(item) for item in response.json()]` - it iterates the
decoded body directly instead of handing it to a model in one piece. A body that is not a list
raises a raw builtin before any model sees it.

## Repro / Symptoms
Measured against mock 200 responses:

```
body null -> builtins.TypeError: 'NoneType' object is not iterable   RelayError: False
body 7    -> builtins.TypeError: 'int' object is not iterable         RelayError: False
```

## Context
Found while closing [[bug-2026-08-27-python-sdk-fetch-all-has-no-termination-stops]], which routed
`_fetch_all` and `_get_page` through `Page[model]` for exactly this reason. A structural enumeration
of decoders in `python/src/relay/` at that point found **twelve** `response.json()` sites: ten are
`Model.model_validate(response.json())`, and two hand-pick. The other hand-picker is
`errors.py`'s `_extract_message`, which is guarded at every step (`try/except ValueError` around the
parse, `isinstance` on both the payload and the field, a `response.text` fallback) and so cannot
raise. `get_tasks` is the one that widens the escape list.

Not a live drift today: `handleListTasks` uses `make([]taskResponse, len(tasks))`, so a correct
server never emits `null`. The point is the same one that slice was built on - the provenance of a
value says nothing about who controls its content.

`python/README.md` was corrected during that slice to name this site rather than claim the class is
closed, so the documentation is already honest. This item is the code half.

## Acceptance / Done When
- A non-list body from `get_tasks` raises something under `RelayError`, or under the same
  `pydantic.ValidationError` the paged walks now raise - not a bare `TypeError`.
- The README's twelve/ten/two count is re-measured and updated, since closing this changes it.

## Related
- `python/src/relay/client.py` `get_tasks`
- [[bug-2026-08-27-python-sdk-exceptions-escape-the-relayerror-hierarchy]] - the wider item; its
  proposed `_read_json` chokepoint would cover the decode but not this iteration
- [[bug-2026-08-27-python-sdk-fetch-all-has-no-termination-stops]] - closed; routed the paged walks
