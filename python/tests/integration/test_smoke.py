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
