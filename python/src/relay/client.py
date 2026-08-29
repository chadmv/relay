from __future__ import annotations

import time
from collections.abc import Iterator
from pathlib import Path
from typing import Any, Optional, TypeVar, Union, cast
from urllib.parse import quote

import httpx
from pydantic import BaseModel

from .config import Config, resolve_config
from .errors import (
    AuthError,
    ProtocolError,
    ValidationError,
    raise_for_response,
)
from .errors import (
    TimeoutError as RelayTimeoutError,
)
from .events import parse_sse_stream
from .models import (
    AgentEnrollment,
    Event,
    Job,
    JobStatus,
    LogPage,
    LogRecord,
    OverlapPolicy,
    Page,
    Reservation,
    ScheduledJob,
    Task,
    User,
    Worker,
)

_TERMINAL_JOB_STATUSES = frozenset(
    {JobStatus.DONE.value, JobStatus.FAILED.value, JobStatus.CANCELLED.value}
)

M = TypeVar("M", bound=BaseModel)

# How much of a server-supplied cursor a ProtocolError message may quote.
#
# The cursor is chosen by the SERVER and its length is unbounded, so a message
# that interpolates it whole is unbounded too - the same "provenance says
# nothing about content" argument that makes the stops below necessary, applied
# to the diagnostic rather than the loop.
#
# 200 characters. A real relay cursor is base64url of a ~96-byte {t,i,s} JSON
# (encodeCursorV2, internal/api/pagination.go), so ~128 characters: every
# legitimate cursor is quoted in full, including a text-sort cursor that carries
# a row's name, and only a cursor no correct server emits is ever cut.
_CURSOR_MESSAGE_CHARS = 200


def _quote_cursor(cursor: str) -> str:
    """Render a server-supplied cursor for an error message, bounded.

    Longer than the bound, the prefix is quoted and the TRUE length reported -
    a truncated string with no length would let a 5 MB cursor and a 201-byte one
    produce the same message.
    """
    if len(cursor) <= _CURSOR_MESSAGE_CHARS:
        return repr(cursor)
    head = cursor[:_CURSOR_MESSAGE_CHARS]
    return f"{head!r} (truncated from {len(cursor)} characters)"



class Client:
    """Synchronous client for the relay REST API.

    Configuration is resolved at construction in this order: explicit kwargs,
    environment (``RELAY_URL`` / ``RELAY_TOKEN``), then the CLI config file
    at ``~/.relay/config.json`` (or ``%APPDATA%\\relay\\config.json`` on
    Windows). If no token is found, methods that require authentication
    raise :class:`AuthError` with a hint pointing at ``relay login``.

    ``timeout`` applies only to the client the SDK builds for itself. When you
    pass ``http_client=``, that client's own timeout policy is used unchanged
    and ``timeout`` is IGNORED - the caller who injects a client owns its
    policy. httpx's default for a bare ``httpx.Client()`` is 5 s; a client
    built with ``timeout=None`` has no bound at all and the SDK will not add
    one. The injected client is also MUTATED once, at construction: BOTH
    ``Authorization`` and ``Accept: application/json`` are written onto it
    permanently, and :meth:`close` does not undo either, correctly, because
    the SDK does not own that client's lifetime. So the object you passed in
    carries the bearer token for as long as you keep it - and note the
    ``Accept`` write is DESTRUCTIVE where the ``Authorization`` write is
    merely additive: an ``Accept`` you had set on that client is overwritten.
    (httpx redacts ``authorization`` to ``[secure]`` in ``Headers.__repr__``,
    so the token does not reach tracebacks or ``repr()``.)
    """

    # Per-request page size used when auto-paginating. Matches the server's
    # max limit and relayclient.PageRequestLimit so we minimize round-trips.
    _PAGE_REQUEST_LIMIT = 200

    # Bounds the NUMBER OF REQUESTS the log paging loop makes against a server
    # whose next_seq keeps advancing but which never reports the log as
    # drained. 10000 pages at _PAGE_REQUEST_LIMIT rows is 2,000,000 rows.
    #
    # Requests is all it bounds. It was described here as a "hang bound" and
    # it is not one, so read it as the count it is - the three axes it leaves
    # open were measured:
    #
    #   - Wall clock. httpx's read timeout applies per SOCKET READ, not per
    #     request, so a server that answers steadily enough need not trip it
    #     at all: measured, one request dribbling a byte every 0.4 s completed
    #     in 14.3 s under a 0.5 s read timeout, 29x. Multiply by 10000
    #     sequential pages.
    #   - Bytes. httpx sends `accept-encoding: gzip, deflate` by default and
    #     decodes with no bound: measured, 89 KiB on the wire materialised as
    #     31 MB, a 343x ratio. Again per page.
    #   - Memory. A LogRecord retains roughly 0.5-1 KB depending on line
    #     length, so a full 2,000,000-row walk is well over a gigabyte,
    #     retained, before task_logs returns.
    #
    # Do NOT point a reader at Client(timeout=) for the first two. That
    # remedy was written here and it does not exist: httpx has no total-time
    # and no response-size setting, and Client(timeout=) sets exactly the four
    # per-operation values (connect, read, write, pool) that the 14.3 s
    # measurement above defeats. Closing either axis needs a caller-supplied
    # httpx.BaseTransport wrapper passed as http_client=, or a deadline
    # enforced outside the SDK. Only the third axis has an in-SDK bound.
    #
    # That bound is `task_logs(limit=N)`, and a private total-row budget is
    # DECLINED deliberately rather than omitted: limit= already is that
    # budget, it short-circuits the walk, and it is public and caller-chosen -
    # a second private one would bound the same axis twice while still
    # advertising nothing about the other two.
    #
    # A CLASS attribute, like _PAGE_REQUEST_LIMIT, so a test can shrink
    # it - which means the loop must read it off `self`, not off a module global.
    _MAX_LOG_PAGES = 10000
    # Bounds the NUMBER OF REQUESTS the LIST paging loop (_fetch_all) makes
    # against a server whose next_cursor keeps advancing but which never reports
    # the list as drained. 10000 pages at _PAGE_REQUEST_LIMIT rows is 2,000,000
    # rows - a jobs table on a long-lived farm can plausibly reach that, and a
    # cap that truncates a legitimate list is worse than the hang it prevents is
    # frequent, so this is the wrong place to be clever with a smaller number.
    # The public, caller-chosen bound on ROWS is `limit=`.
    #
    # Requests is all it bounds. Wall clock, response bytes and the memory of a
    # single response are all open; those three axes are MEASURED in the
    # _MAX_LOG_PAGES comment above - read them there. They are not restated
    # here, because a second copy is a second thing that can go stale. Closing
    # them belongs to
    # bug-2026-08-26-relayclient-has-no-response-bound-and-no-client-timeout.
    #
    # SEPARATE from _MAX_LOG_PAGES rather than a shared _MAX_PAGES: the two
    # loops bound different populations, and that comment's measurements are
    # log-specific and would become wrong if the constant were shared.
    #
    # A CLASS attribute, like _MAX_LOG_PAGES, so a test can shrink it - which
    # means the loop must read it off `self`, not off a module global.
    _MAX_LIST_PAGES = 10000


    def __init__(
        self,
        *,
        url: Optional[str] = None,
        token: Optional[str] = None,
        config_path: Optional[Path] = None,
        timeout: float = 30.0,
        http_client: Optional[httpx.Client] = None,
    ) -> None:
        cfg: Config = resolve_config(url=url, token=token, config_path=config_path)
        if not cfg.server_url:
            cfg.server_url = "http://localhost:8080"
        self._config = cfg
        self._owns_client = http_client is None
        if http_client is None:
            http_client = httpx.Client(
                base_url=cfg.server_url,
                timeout=timeout,
                headers=self._auth_headers(cfg.token, required=False),
            )
        else:
            http_client.headers.update(self._auth_headers(cfg.token, required=False))
        self._http = http_client

    @staticmethod
    def _auth_headers(token: str, *, required: bool = True) -> dict[str, str]:
        headers = {"Accept": "application/json"}
        if token:
            headers["Authorization"] = f"Bearer {token}"
        elif required:
            raise AuthError(
                "no relay token configured; run `relay login` or set RELAY_TOKEN"
            )
        return headers

    def _require_token(self) -> None:
        if not self._config.token:
            raise AuthError(
                "no relay token configured; run `relay login` or set RELAY_TOKEN"
            )

    def close(self) -> None:
        if self._owns_client:
            self._http.close()

    def __enter__(self) -> Client:
        return self

    def __exit__(self, *_exc: object) -> None:
        self.close()

    # ─── Pagination helpers ───────────────────────────────────────────────

    def _get_page(
        self,
        path: str,
        model: type[M],
        *,
        params: Optional[dict[str, str]] = None,
        sort: Optional[str] = None,
        limit: Optional[int] = None,
        cursor: Optional[str] = None,
    ) -> Page[M]:
        """Fetch a single page envelope and validate each item into ``model``."""
        self._require_token()
        p: dict[str, str] = dict(params or {})
        if sort is not None:
            p["sort"] = sort
        if limit is not None:
            p["limit"] = str(limit)
        if cursor is not None:
            p["cursor"] = cursor
        response = self._http.get(path, params=p)
        raise_for_response(response)
        body = response.json()
        items = [model.model_validate(item) for item in body["items"]]
        return cast(
            "Page[M]",
            Page(items=items, next_cursor=body.get("next_cursor", ""), total=body.get("total", 0)),
        )

    def _fetch_all(
        self,
        path: str,
        model: type[M],
        *,
        params: Optional[dict[str, str]] = None,
        sort: Optional[str] = None,
        limit: Optional[int] = None,
    ) -> list[M]:
        """Walk ?cursor= until next_cursor is empty, or ``limit`` rows collected.

        ``limit`` caps the TOTAL rows returned across pages (None = all). Each
        request fetches ``_PAGE_REQUEST_LIMIT`` rows.

        A limit below 1 is rejected here rather than passed on, for the same
        reason task_logs() rejects it: the walk ends in ``out[:limit]``, and
        Python slice semantics turn a negative limit into "everything but the
        last N rows" - a plausible-looking short list rather than an error.
        This guard is NOT duplicated in _get_page, where ``limit`` is the page
        size and travels to the server, which answers 400 for anything outside
        1..200. Validate locally only where the server never sees the value.

        BELOW ``_require_token()``, not above it. A client with no token cannot
        make the request whatever the limit says, and every other method on it
        reports :class:`AuthError` with a ``relay login`` hint; a limit guard in
        front would make these six the only ones that answer a different
        question first.
        """
        self._require_token()
        if limit is not None and limit < 1:
            raise ValidationError(f"limit must be >= 1, got {limit}")
        p: dict[str, str] = dict(params or {})
        if sort is not None:
            p["sort"] = sort
        p["limit"] = str(self._PAGE_REQUEST_LIMIT)
        out: list[M] = []
        cursor: Optional[str] = None
        pages = 0
        seen: set[str] = set()
        while True:
            pages += 1
            if cursor:
                p["cursor"] = cursor
            response = self._http.get(path, params=p)
            raise_for_response(response)
            body = response.json()
            # Bound to a local rather than re-read below: the emptiness test
            # must ask about THIS page, and `out` is cumulative. (These six
            # routes never send JSON null here - buildPage returns a non-nil
            # empty slice - so no null-coercion is needed.)
            items = body["items"]
            out.extend(model.model_validate(item) for item in items)
            if limit is not None and len(out) >= limit:
                return out[:limit]
            # NOT changed by this slice: a MISSING next_cursor key still reads
            # as drained here, and that is the subject of the open item
            # bug-2026-08-27-python-sdk-page-cursor-defaults-to-drained, which
            # also covers _get_page and is therefore wider than this loop.
            cursor = body.get("next_cursor", "")
            if not cursor:
                return out
            # This arm MUST stay above the empty-page stop below. A list with no
            # matching rows legitimately answers items: [] - and it reports
            # itself drained, so it never reaches the stop. Inverted, list_jobs()
            # against an empty jobs table raises.
            if not items:
                raise ProtocolError(
                    "server returned an empty page while still advertising more "
                    f"rows (next_cursor {_quote_cursor(cursor)})",
                    records=out,
                )
            # The stop is: this walk already requested this cursor. A SET, not a
            # comparison against the previous cursor - the two catch different
            # things and a two-cycle (A,B,A,B, which two replicas behind a load
            # balancer produce) is invisible to the comparison and runs to the
            # page cap. This is not a second stop; it is the one stop, with the
            # container that implements it. Previous-cursor-only is this set
            # restricted to its last element.
            #
            # A repeated cursor is UNREACHABLE on a correct server: the server's
            # cursor (encodeCursorV2, internal/api/pagination.go) encodes the
            # LAST KEPT row's key and the next page's predicate is strictly past
            # it with id as tiebreaker, so cursor keys strictly decrease along a
            # walk. Comparison is byte-exact on the base64 string; the SDK never
            # decodes it, and deliberately so - decoding would make a
            # server-internal encoding a cross-language contract to keep in step.
            #
            # Memory, stated rather than hidden: at most one entry per page, so
            # the entry COUNT is bounded by _MAX_LIST_PAGES. The BYTE cost is
            # entries x cursor length, and cursor length is server-supplied and
            # unbounded - roughly 0.1% of a real walk (~128 bytes against ~100 KB
            # of models per page), and dominant only against a server sending
            # one-item pages with multi-megabyte cursors. A digest per entry
            # would close that term and is DECLINED: the same attacker already
            # has an equal retention channel through `items`, and the
            # unbounded-response-bytes axis belongs to
            # bug-2026-08-26-relayclient-has-no-response-bound-and-no-client-timeout
            # at the right layer.
            if cursor in seen:
                raise ProtocolError(
                    "server cursor did not advance - it repeated a cursor this "
                    f"walk had already requested ({_quote_cursor(cursor)}) after "
                    f"{pages} pages",
                    records=out,
                )
            if pages >= self._MAX_LIST_PAGES:
                # Count DISTINCT ids, never len(out). `total` is server-supplied
                # and so is the cursor, so a server that re-serves a page behind
                # an ADVANCING cursor drives len(out) up to total while half the
                # list was never sent - and the repeated-cursor stop above cannot
                # see that, because the cursor genuinely advances.
                #
                # M is bound only to BaseModel, so nothing structural guarantees
                # an id: the accessor is getattr with a default. Five of the six
                # walked models declare `id: str` (required, undefaulted), so
                # against them a row without an id fails in model_validate long
                # before this line. `Job` declares `id: Optional[str] = None`
                # because it is the authoring model too, so list_jobs is the one
                # method that can reach the "no id" arm below.
                #
                # The failure direction is UNDER-count, which is safe: it can
                # only push the code into the blaming arm. It can never
                # over-count - the set holds at most one entry per row received.
                #
                # Built ONCE, inside a block every path of which raises. The
                # common path - a walk that finishes - pays nothing. Do not
                # accumulate this per page: that would put a string per row on
                # 100% of walks to serve a message that fires at 2,000,000 rows.
                ids: set[str] = set()
                for row in out:
                    row_id = getattr(row, "id", None)
                    if isinstance(row_id, str) and row_id:
                        ids.add(row_id)
                distinct = len(ids)
                # From the CURRENT page, matching task_logs' use of page.total.
                # A MISSING total defaults to 0 and so falls into the blaming arm
                # below - the safe direction, and deliberately NOT the same class
                # of default as the next_cursor one above, where a missing key
                # reads as "drained" and silently truncates.
                total = body.get("total", 0)
                if distinct == 0:
                    # Reaching here means every collected row lacked a usable id;
                    # `out` itself is non-empty, because the empty-page stop above
                    # rejects any page that contributed no rows. Do NOT print
                    # "0 distinct rows collected" - that is a computed-looking
                    # number standing in for a measurement that did not happen.
                    raise ProtocolError(
                        f"truncated after {self._MAX_LIST_PAGES} pages - hit the "
                        f"client's page cap; {len(out)} rows were collected and "
                        f"the server reported {total}, but completeness could not "
                        "be checked because the rows carry no id",
                        records=out,
                    )
                if total > 0 and distinct >= total:
                    # Do not blame the server here. A list of exactly
                    # _MAX_LIST_PAGES * _PAGE_REQUEST_LIMIT rows drains correctly,
                    # but its last page is full and so carries a cursor: we
                    # stopped one request short of learning we were done, having
                    # collected every row. The envelope's own total settles it.
                    raise ProtocolError(
                        f"truncated after {self._MAX_LIST_PAGES} pages - hit the "
                        f"client's page cap; the server reported {total} rows and "
                        f"every one was collected ({distinct} distinct row ids), "
                        "but it had not yet reported the list as drained",
                        records=out,
                    )
                raise ProtocolError(
                    f"truncated after {self._MAX_LIST_PAGES} pages - hit the "
                    "client's page cap; the list may be longer than "
                    f"{self._MAX_LIST_PAGES * self._PAGE_REQUEST_LIMIT} rows, or "
                    "the server may never report it as drained "
                    f"({distinct} distinct row ids collected, server reported "
                    f"{total})",
                    records=out,
                )
            seen.add(cursor)

    # ─── Jobs ─────────────────────────────────────────────────────────────

    def submit(self, job: Job) -> Job:
        """Submit a Job. Validates locally, then POSTs to ``/v1/jobs`` and
        returns the server's response as a populated :class:`Job`.
        """
        self._require_token()
        job.validate_spec()
        response = self._http.post("/v1/jobs", json=job.to_spec_dict())
        raise_for_response(response)
        return Job.model_validate(response.json())

    def get_job(self, job_id: str) -> Job:
        self._require_token()
        response = self._http.get(f"/v1/jobs/{job_id}")
        raise_for_response(response)
        return Job.model_validate(response.json())

    def list_jobs(
        self,
        *,
        status: Optional[Union[str, JobStatus]] = None,
        scheduled_job_id: Optional[str] = None,
        sort: Optional[str] = None,
        limit: Optional[int] = None,
    ) -> list[Job]:
        """List jobs, auto-paginating across all pages.

        ``limit`` caps the TOTAL number of jobs returned (None = all).
        ``sort`` is forwarded to ?sort= and validated server-side; an
        unknown key raises :class:`ValidationError`.

        A walk that cannot be completed raises :class:`ProtocolError` with the
        rows collected so far on ``.records``.
        """
        return self._fetch_all(
            "/v1/jobs", Job,
            params=self._job_filters(status, scheduled_job_id), sort=sort, limit=limit,
        )

    def list_jobs_page(
        self,
        *,
        status: Optional[Union[str, JobStatus]] = None,
        scheduled_job_id: Optional[str] = None,
        sort: Optional[str] = None,
        limit: Optional[int] = None,
        cursor: Optional[str] = None,
    ) -> Page[Job]:
        """Fetch a single page of jobs.

        ``limit`` is the PAGE SIZE (1-200). Use the returned ``next_cursor``
        as ``cursor=`` to page forward.
        """
        return self._get_page(
            "/v1/jobs", Job,
            params=self._job_filters(status, scheduled_job_id),
            sort=sort, limit=limit, cursor=cursor,
        )

    @staticmethod
    def _job_filters(
        status: Optional[Union[str, JobStatus]],
        scheduled_job_id: Optional[str],
    ) -> dict[str, str]:
        params: dict[str, str] = {}
        if status is not None:
            params["status"] = status.value if isinstance(status, JobStatus) else status
        if scheduled_job_id is not None:
            params["scheduled_job_id"] = scheduled_job_id
        return params

    def cancel_job(self, job_id: str, *, force: bool = False) -> Job:
        """Cancel a job. Graceful by default; ``force=True`` asks the agent to
        kill the running task immediately.

        The returned :class:`Job` carries NO task list. ``handleCancelJob``
        serializes with ``tasks`` omitted (the field is ``omitempty``), so
        ``job.tasks`` is ``[]`` here for EVERY job - including jobs that have
        tasks, which is all of them, since the server rejects a spec with none.
        Do not read ``.tasks`` off this value; call :meth:`get_tasks` instead.
        """
        self._require_token()
        params = {"force": "true"} if force else {}
        response = self._http.delete(f"/v1/jobs/{job_id}", params=params)
        raise_for_response(response)
        return Job.model_validate(response.json())

    # ─── Tasks ────────────────────────────────────────────────────────────

    def get_tasks(self, job_id: str) -> list[Task]:
        self._require_token()
        response = self._http.get(f"/v1/jobs/{job_id}/tasks")
        raise_for_response(response)
        return [Task.model_validate(item) for item in response.json()]

    def get_task(self, task_id: str) -> Task:
        self._require_token()
        response = self._http.get(f"/v1/tasks/{task_id}")
        raise_for_response(response)
        return Task.model_validate(response.json())

    def task_logs_page(
        self, task_id: str, *, since_seq: int = 0, limit: Optional[int] = None
    ) -> LogPage:
        """Fetch one page of a task's log.

        ``limit`` is the PAGE SIZE (1-200); omitted, the server's default of 50
        applies, which is visible here because ``next_seq`` tells the caller
        there is more. Pass the returned ``next_seq`` back as ``since_seq=`` to
        page forward - VERBATIM, never ``+ 1``: the server's predicate is
        ``id > $2``, so the cursor is exclusive already.
        """
        self._require_token()
        params: dict[str, str] = {}
        if since_seq:
            params["since_seq"] = str(since_seq)
        if limit is not None:
            params["limit"] = str(limit)
        # quote(safe="") escapes AT LEAST as much as Go's url.PathEscape,
        # which printTaskLogs (internal/cli/logs.go:714) applies to this same
        # argument. Not an identity: Go leaves + : @ = & $ unescaped and
        # Python percent-encodes them. The extra is harmless here (the two
        # agree exactly on a UUID, and over-escaping a path segment is the
        # safe direction), but they are not the same function. Raw, an id
        # of "../../v1/users" resolves to /v1/users/logs - same host, bearer
        # attached - and one containing "?" swallows the /logs suffix and the
        # paging params into a query string. The escape means the request shape
        # does not rest on where the id came from.
        response = self._http.get(
            f"/v1/tasks/{quote(task_id, safe='')}/logs", params=params
        )
        raise_for_response(response)
        # The WHOLE body goes through the model, never a hand-picked
        # body["items"]. The model is the pin: a missing next_seq or total
        # raises here rather than reading as "drained".
        return LogPage.model_validate(response.json())

    def task_logs(self, task_id: str, *, limit: Optional[int] = None) -> list[LogRecord]:
        """Fetch a task's complete log, auto-paginating across pages.

        ``limit`` caps the TOTAL number of records returned; None means all,
        and anything below 1 raises :class:`ValidationError`. Each
        request fetches ``_PAGE_REQUEST_LIMIT`` rows; without that explicit
        limit the server's default is 50 and a long log is silently truncated.

        This accumulates the whole log in memory. ``relay logs`` does not - it
        prints each page as it arrives - and this cannot, while returning a
        list. On a very large log use :meth:`task_logs_page` (O(one page)) or
        pass ``limit=``.

        A walk that cannot be completed raises :class:`ProtocolError` with the
        records collected so far on ``.records``. printTaskLogs, which this
        ports, has printed every row by the time it returns the equivalent
        error, so the error is a caveat on output the operator already has;
        returning a list is what makes the caveat and the rows arrive
        separately here.
        """
        # Locally, before the loop. The cap is applied as `out[:limit]`, and
        # Python slice semantics turn a negative limit into "all but the last
        # N" - limit=-1 on a 5-row log returned 4 records, dropping the newest
        # line rather than capping anything. limit=0 spent a request to
        # return [].
        #
        # The token check is first and is repeated here on purpose. Every other
        # method answers "no token" before anything else, and task_logs_page
        # would too - but only on the first request, which the limit guard
        # returns before. Asking here keeps the precedence the same on both
        # walks; the second, redundant check inside task_logs_page costs
        # nothing.
        self._require_token()
        if limit is not None and limit < 1:
            raise ValidationError(f"limit must be >= 1, got {limit}")
        out: list[LogRecord] = []
        since = 0
        pages = 0
        while True:
            pages += 1
            page = self.task_logs_page(
                task_id, since_seq=since, limit=self._PAGE_REQUEST_LIMIT
            )
            out.extend(page.items)
            if limit is not None and len(out) >= limit:
                return out[:limit]
            # Break on next_seq, never on len(items) < limit: the two agree
            # today, but the second re-derives a rule the server already applied
            # and desynchronizes the moment the drain rule moves.
            #
            # This arm MUST stay above the empty-page stop below. A log whose
            # length is an exact multiple of the page size legitimately produces
            # a final empty page, and that page reports drained.
            if page.next_seq == 0:
                return out
            # Three stops beyond the server's own drained signal, and all three
            # are needed. The cursor is server-supplied and drives a client
            # loop, and the provenance of a value says nothing about who
            # controls its content or the timing of the writes behind it.
            if not page.items:
                raise ProtocolError(
                    "server returned an empty page without reporting the log as "
                    f"drained (next_seq {page.next_seq} after since_seq {since})",
                    records=out,
                )
            if page.next_seq <= since:
                raise ProtocolError(
                    "server cursor did not advance "
                    f"(next_seq {page.next_seq} after since_seq {since})",
                    records=out,
                )
            if pages >= self._MAX_LOG_PAGES:
                # Count DISTINCT seqs, never len(out). `total` is server-supplied
                # and so is the cursor, so a server that re-serves a page behind
                # an advancing cursor drives len(out) up to total while half the
                # log was never sent - and the message below would then tell the
                # operator every row was collected. This is the first place
                # LogRecord.seq is load-bearing for CORRECTNESS rather than for
                # correlating a record with a cursor, and it is what the field's
                # required-and-undefaulted declaration buys: a defaulted
                # `seq: int = 0` would collapse every row of a seq-less page into
                # one set member and under-count instead.
                #
                # This set is the one line in a memory-critical walk that adds
                # memory: up to 2,000,000 ints, roughly 35-70 MB. It is built
                # once, inside a block every path of which raises, so it is a
                # transient peak at the end and not a per-page cost - which is
                # why it is affordable against the gigabyte-plus of LogRecords
                # already retained by then.
                collected = len({r.seq for r in out})
                if page.total > 0 and collected >= page.total:
                    # Do not blame the server here. A log of exactly
                    # _MAX_LOG_PAGES * _PAGE_REQUEST_LIMIT rows drains
                    # correctly, but its last page is full and so carries a
                    # non-zero cursor: we stopped one request short of learning
                    # we were done, having collected every row. The envelope's
                    # own total settles it, so do not re-raise the ambiguity.
                    raise ProtocolError(
                        f"truncated after {self._MAX_LOG_PAGES} pages - hit the "
                        f"client's page cap; the server reported {page.total} rows "
                        f"and every one was collected ({collected} distinct rows), "
                        "but it had not yet reported the log as drained",
                        records=out,
                    )
                raise ProtocolError(
                    f"truncated after {self._MAX_LOG_PAGES} pages - hit the client's "
                    "page cap; the log may be longer than "
                    f"{self._MAX_LOG_PAGES * self._PAGE_REQUEST_LIMIT} rows, or the "
                    "server may never report it as drained "
                    f"({collected} distinct rows collected, server reported "
                    f"{page.total})",
                    records=out,
                )
            since = page.next_seq

    # ─── Following progress ───────────────────────────────────────────────

    def follow_job(self, job_id: str) -> Iterator[Event]:
        """Stream events for a single job over SSE.

        **The server does not end the stream when the job finishes.**
        ``handleEvents`` closes on exactly two conditions - the request context
        is done, or the broker drops this subscriber for falling behind - and
        it has no notion of job terminality. This iterator sets no read
        timeout, so a caller that iterates to exhaustion blocks forever after
        the job is done. Break out on a terminal ``job`` frame yourself, or use
        :meth:`wait`, which polls and is immune.

        A ``dropped`` frame (:attr:`relay.EventType.DROPPED`) means the broker
        dropped this subscriber for falling behind: frames published in the gap
        are gone, and the recovery is to re-read the job with :meth:`get_job`
        and resume each task's log with
        ``task_logs_page(task_id, since_seq=<last seq seen>)``. Not
        :meth:`task_logs`, which takes no ``since_seq`` and would restart at
        row 0 and re-walk the whole log.

        The underlying HTTP connection is closed on generator exit.
        """
        self._require_token()
        return self._stream_events(job_id)

    def _stream_events(self, job_id: str) -> Iterator[Event]:
        # All FOUR parameters, explicitly. httpx.Timeout takes its
        # four-explicit branch only when connect, read, write and pool are all
        # set; anything less and it raises ValueError, which is what
        # `Timeout(connect=..., read=None)` did on every call. And it must be
        # this form rather than `Timeout(self._http.timeout, read=None)`: the
        # single-Timeout branch `assert read is UNSET`s, so that spelling
        # raises AssertionError - and under `python -O`, where asserts are
        # stripped, silently reinstates the read timeout the stream must not
        # have. Only read is dropped; the caller's connect/write/pool stand.
        base = self._http.timeout
        with self._http.stream(
            "GET",
            "/v1/events",
            params={"job_id": job_id},
            headers={"Accept": "text/event-stream"},
            timeout=httpx.Timeout(
                connect=base.connect, read=None, write=base.write, pool=base.pool
            ),
        ) as response:
            raise_for_response(response)
            yield from parse_sse_stream(response.iter_lines())

    def wait(
        self,
        job_id: str,
        *,
        timeout: Optional[float] = None,
        poll_interval: float = 1.0,
    ) -> Job:
        """Block until the job reaches a terminal state, then return it.

        Polls ``GET /v1/jobs/{id}`` every ``poll_interval`` seconds. Polling
        is preferred over SSE here for simplicity and correctness: SSE has
        no replay, so a stream that drops would silently never report
        completion. ``timeout`` is wall-clock; raises :class:`TimeoutError`.
        """
        deadline = None if timeout is None else time.monotonic() + timeout
        while True:
            job = self.get_job(job_id)
            if job.status in _TERMINAL_JOB_STATUSES:
                return job
            if deadline is not None and time.monotonic() >= deadline:
                raise RelayTimeoutError(
                    f"wait timed out after {timeout}s; job status was {job.status!r}"
                )
            time.sleep(poll_interval)

    # ─── Scheduled jobs ───────────────────────────────────────────────────

    def create_schedule(
        self,
        *,
        name: str,
        cron_expr: str,
        job_spec: Union[Job, dict[str, Any]],
        timezone: str = "UTC",
        overlap_policy: Union[str, OverlapPolicy] = OverlapPolicy.SKIP,
        enabled: Optional[bool] = None,
    ) -> ScheduledJob:
        self._require_token()
        if isinstance(job_spec, Job):
            job_spec.validate_spec()
            spec_dict = job_spec.to_spec_dict()
        elif isinstance(job_spec, dict):
            spec_dict = job_spec
        else:
            raise ValidationError(
                f"job_spec must be Job or dict, got {type(job_spec).__name__}"
            )
        body: dict[str, Any] = {
            "name": name,
            "cron_expr": cron_expr,
            "timezone": timezone,
            "overlap_policy": (
                overlap_policy.value
                if isinstance(overlap_policy, OverlapPolicy)
                else overlap_policy
            ),
            "job_spec": spec_dict,
        }
        if enabled is not None:
            body["enabled"] = enabled
        response = self._http.post("/v1/scheduled-jobs", json=body)
        raise_for_response(response)
        return ScheduledJob.model_validate(response.json())

    def list_schedules(
        self,
        *,
        sort: Optional[str] = None,
        limit: Optional[int] = None,
    ) -> list[ScheduledJob]:
        """List scheduled jobs, auto-paginating across all pages.

        ``limit`` caps the TOTAL rows returned (None = all). ``sort`` is
        validated server-side; an unknown key raises :class:`ValidationError`.

        A walk that cannot be completed raises :class:`ProtocolError` with the
        rows collected so far on ``.records``.
        """
        return self._fetch_all("/v1/scheduled-jobs", ScheduledJob, sort=sort, limit=limit)

    def list_schedules_page(
        self,
        *,
        sort: Optional[str] = None,
        limit: Optional[int] = None,
        cursor: Optional[str] = None,
    ) -> Page[ScheduledJob]:
        """Fetch a single page of scheduled jobs. ``limit`` is the page size (1-200)."""
        return self._get_page(
            "/v1/scheduled-jobs", ScheduledJob, sort=sort, limit=limit, cursor=cursor
        )

    def get_schedule(self, schedule_id: str) -> ScheduledJob:
        self._require_token()
        response = self._http.get(f"/v1/scheduled-jobs/{schedule_id}")
        raise_for_response(response)
        return ScheduledJob.model_validate(response.json())

    def update_schedule(
        self,
        schedule_id: str,
        *,
        name: Optional[str] = None,
        cron_expr: Optional[str] = None,
        timezone: Optional[str] = None,
        overlap_policy: Optional[Union[str, OverlapPolicy]] = None,
        enabled: Optional[bool] = None,
        job_spec: Optional[Union[Job, dict[str, Any]]] = None,
    ) -> ScheduledJob:
        self._require_token()
        body: dict[str, Any] = {}
        if name is not None:
            body["name"] = name
        if cron_expr is not None:
            body["cron_expr"] = cron_expr
        if timezone is not None:
            body["timezone"] = timezone
        if overlap_policy is not None:
            body["overlap_policy"] = (
                overlap_policy.value
                if isinstance(overlap_policy, OverlapPolicy)
                else overlap_policy
            )
        if enabled is not None:
            body["enabled"] = enabled
        if job_spec is not None:
            if isinstance(job_spec, Job):
                job_spec.validate_spec()
                body["job_spec"] = job_spec.to_spec_dict()
            else:
                body["job_spec"] = job_spec
        response = self._http.patch(f"/v1/scheduled-jobs/{schedule_id}", json=body)
        raise_for_response(response)
        return ScheduledJob.model_validate(response.json())

    def delete_schedule(self, schedule_id: str) -> None:
        self._require_token()
        response = self._http.delete(f"/v1/scheduled-jobs/{schedule_id}")
        raise_for_response(response)

    def run_schedule_now(self, schedule_id: str) -> Job:
        """Fire a schedule immediately. Allowed for the schedule's owner or an
        admin; other callers get :class:`AuthError`.
        """
        self._require_token()
        response = self._http.post(f"/v1/scheduled-jobs/{schedule_id}/run-now")
        raise_for_response(response)
        return Job.model_validate(response.json())

    # ─── Workers ──────────────────────────────────────────────────────────

    def list_workers(
        self, *, sort: Optional[str] = None, limit: Optional[int] = None
    ) -> list[Worker]:
        """List workers, auto-paginating across all pages. ``limit`` caps total rows.

        A walk that cannot be completed raises :class:`ProtocolError` with the
        rows collected so far on ``.records``.
        """
        return self._fetch_all("/v1/workers", Worker, sort=sort, limit=limit)

    def list_workers_page(
        self, *, sort: Optional[str] = None, limit: Optional[int] = None,
        cursor: Optional[str] = None,
    ) -> Page[Worker]:
        """Fetch a single page of workers. ``limit`` is the page size (1-200)."""
        return self._get_page("/v1/workers", Worker, sort=sort, limit=limit, cursor=cursor)

    # ─── Users (admin-only) ───────────────────────────────────────────────

    def list_users(
        self, *, sort: Optional[str] = None, limit: Optional[int] = None
    ) -> list[User]:
        """List users, auto-paginating. Admin-only: a non-admin token raises AuthError.

        A walk that cannot be completed raises :class:`ProtocolError` with the
        rows collected so far on ``.records``.
        """
        return self._fetch_all("/v1/users", User, sort=sort, limit=limit)

    def list_users_page(
        self, *, sort: Optional[str] = None, limit: Optional[int] = None,
        cursor: Optional[str] = None,
    ) -> Page[User]:
        """Fetch a single page of users (admin-only). ``limit`` is the page size (1-200)."""
        return self._get_page("/v1/users", User, sort=sort, limit=limit, cursor=cursor)

    # ─── Reservations (admin-only) ────────────────────────────────────────

    def list_reservations(
        self, *, sort: Optional[str] = None, limit: Optional[int] = None
    ) -> list[Reservation]:
        """List reservations, auto-paginating. Admin-only: non-admin raises AuthError.

        A walk that cannot be completed raises :class:`ProtocolError` with the
        rows collected so far on ``.records``.
        """
        return self._fetch_all("/v1/reservations", Reservation, sort=sort, limit=limit)

    def list_reservations_page(
        self, *, sort: Optional[str] = None, limit: Optional[int] = None,
        cursor: Optional[str] = None,
    ) -> Page[Reservation]:
        """Fetch a single page of reservations (admin-only). ``limit`` is the page size (1-200)."""
        return self._get_page(
            "/v1/reservations", Reservation, sort=sort, limit=limit, cursor=cursor
        )

    # ─── Agent enrollments (admin-only) ───────────────────────────────────

    def list_agent_enrollments(
        self, *, sort: Optional[str] = None, limit: Optional[int] = None
    ) -> list[AgentEnrollment]:
        """List active agent enrollments, auto-paginating. Admin-only: non-admin raises AuthError.

        A walk that cannot be completed raises :class:`ProtocolError` with the
        rows collected so far on ``.records``.
        """
        return self._fetch_all("/v1/agent-enrollments", AgentEnrollment, sort=sort, limit=limit)

    def list_agent_enrollments_page(
        self, *, sort: Optional[str] = None, limit: Optional[int] = None,
        cursor: Optional[str] = None,
    ) -> Page[AgentEnrollment]:
        """Fetch a single page of agent enrollments (admin-only). ``limit`` is the page size (1-200)."""
        return self._get_page(
            "/v1/agent-enrollments", AgentEnrollment, sort=sort, limit=limit, cursor=cursor
        )
