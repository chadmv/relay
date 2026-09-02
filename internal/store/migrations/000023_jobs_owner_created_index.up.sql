-- Serves GET /v1/jobs?mine=true under the default sort and its count: the
-- keyset scan on (created_at DESC, id DESC) restricted to one submitter. Same
-- shape as idx_sched_jobs_owner_created on scheduled_jobs.
--
-- It does not help ?mine= under a non-default sort, and it does not help ?q=
-- at all: substring containment has no index that serves it.
--
-- Plain CREATE INDEX, never CONCURRENTLY: golang-migrate wraps each migration
-- in a transaction and CONCURRENTLY cannot run inside one. Operational
-- consequence, for whoever reads this during an incident: the build takes a
-- SHARE lock on jobs for its duration and golang-migrate holds it until the
-- whole migration commits, so reads keep serving but every write to jobs
-- blocks - no job submission and no status transition - until it finishes.
-- Startup migrations run before the server serves, so on a large jobs table
-- this is a submission outage for the duration, not just a slow boot.

CREATE INDEX idx_jobs_submitted_created_id ON jobs (submitted_by, created_at DESC, id DESC);
