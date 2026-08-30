from __future__ import annotations

import time

import relay


def test_submit_and_wait(client: relay.Client) -> None:
    """End-to-end: submit a trivial echo job and wait for it to finish.

    Requires at least one agent online and able to satisfy the empty
    requirements selector. The test polls with a generous timeout so it
    works whether the agent is idle or busy.
    """
    job = relay.Job(name=f"sdk-smoke-{int(time.time())}")
    job.add_task("echo", commands=[["echo", "hello-from-sdk"]])

    submitted = client.submit(job)
    assert submitted.id is not None

    final = client.wait(submitted.id, timeout=120, poll_interval=2)
    assert final.status == relay.JobStatus.DONE, f"job ended {final.status!r}"

    tasks = client.get_tasks(submitted.id)
    assert len(tasks) == 1
    logs = client.task_logs(tasks[0].id)
    assert any("hello-from-sdk" in record.content for record in logs)


def test_cancel_running_job(client: relay.Client) -> None:
    """Submit a long-running job, cancel it, verify terminal cancelled state."""
    job = relay.Job(name=f"sdk-cancel-{int(time.time())}")
    # 30s sleep so we have time to cancel before it finishes.
    job.add_task("sleep", commands=[["sleep", "30"]])
    submitted = client.submit(job)
    assert submitted.id is not None
    try:
        cancelled = client.cancel_job(submitted.id, force=True)
        assert cancelled.status == relay.JobStatus.CANCELLED
    finally:
        # Best-effort cleanup if cancel failed; ignore secondary errors.
        try:
            client.cancel_job(submitted.id, force=True)
        except relay.RelayError:
            pass


def test_list_jobs_includes_recent_submission(client: relay.Client) -> None:
    job = relay.Job(name=f"sdk-list-{int(time.time())}")
    job.add_task("echo", commands=[["echo", "list-test"]])
    submitted = client.submit(job)
    assert submitted.id is not None

    jobs = client.list_jobs()
    ids = {j.id for j in jobs}
    assert submitted.id in ids


def test_a_list_with_no_matching_rows_returns_empty_and_does_not_raise(
    client: relay.Client,
) -> None:
    """The ONE assertion in this slice whose truth depends on what the SERVER
    puts on the wire, rather than on a fixture.

    Inverting the drained return and the empty-page stop is a one-line mutation
    that turns every list call against an empty result set into a ProtocolError.
    A fixture proves the client handles `{"items": [], "next_cursor": ""}`; only
    a live handler proves that is what buildPage actually sends for zero rows.

    The filter is a schedule created here and never fired, so the result set is
    empty on any server regardless of what else is in the database.

    NOT a random UUID, which is what this test was first written against and
    which does NOT reach the empty page: handleListJobs' scheduled_job_id branch
    (internal/api/jobs.go) calls ownedScheduledJob BEFORE it paginates, and that
    answers 404 "scheduled job not found" for an id no row carries. Measured -
    the random-uuid version failed with relay.errors.NotFound, never touching
    buildPage. The schedule has to exist and be owned by this token for the walk
    to get as far as the zero-row page this test is about.

    Since the strict-envelope slice this test carries a SECOND job: Page
    requires next_cursor and total as KEYS, so an `omitempty` creeping onto
    internal/api's page[T] would make this call raise pydantic.ValidationError
    instead of returning []. No fixture can see that - the server's serializer
    is what is under test, and the zero-row page is the shape where an
    omitempty would most plausibly bite. Under the old model this proved only
    that the cursor was empty-or-absent; it now proves present-and-empty.

    A NOTE, NOT A GATE. This lane is manual: conftest skips every test here
    unless RELAY_INTEGRATION=1 and a live server is configured, and
    .github/workflows/python.yml runs `pytest tests/unit -v` only. The
    executable guard on the tag is on the Go side, where the tags live -
    TestPageEnvelope_AllThreeKeysArePresentOnAZeroValuePage in
    internal/api/pagination_test.go, which runs in the lane CI does run.

    test_list_jobs_includes_recent_submission is NOT the same proof. It is an
    ASYMMETRIC one that pins next_cursor alone. Measured 2026-08-29 against a
    real server with `,omitempty` on next_cursor and total but NOT on items:
    this test failed on both of those fields (the wire body was literally
    `{'items': []}`), while the list-jobs test failed on next_cursor only - its
    `total` was 3, and omitempty does not drop a non-zero int. The mutation
    scope is stated because it is load-bearing for the body: under an all-three
    variant the zero-row body is `{}` and this test raises on three fields. That asymmetry is why the
    zero-row page is the one that has to exist.
    """
    schedule = client.create_schedule(
        name=f"sdk-empty-list-{int(time.time())}",
        # 03:00 on the 1st of January: it will not fire during this test.
        cron_expr="0 3 1 1 *",
        job_spec=_never_fires_job_spec(),
        enabled=False,
    )
    assert schedule.id is not None
    try:
        jobs = client.list_jobs(scheduled_job_id=schedule.id)
        assert jobs == []
    finally:
        client.delete_schedule(schedule.id)


def _never_fires_job_spec() -> relay.Job:
    job = relay.Job(name="sdk-empty-list-spec")
    job.add_task("echo", commands=[["echo", "never-runs"]])
    return job
