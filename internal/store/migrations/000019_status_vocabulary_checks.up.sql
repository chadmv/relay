-- Normalize any historically-drifted priority before constraining (jobspec.Validate
-- never validated priority until 000019's companion change, so a typo could have
-- been persisted). All other constrained columns have only bounded writers and
-- need no cleanup; overlap_policy is validated at the handler.
UPDATE jobs SET priority = 'normal'
WHERE priority NOT IN ('low','normal','high');

ALTER TABLE workers
  ADD CONSTRAINT workers_status_check
  CHECK (status IN ('online','offline','stale','revoked'));

ALTER TABLE jobs
  ADD CONSTRAINT jobs_status_check
  CHECK (status IN ('pending','running','done','failed','cancelled'));

-- This set MUST stay identical to the priority switch in jobspec.Validate.
ALTER TABLE jobs
  ADD CONSTRAINT jobs_priority_check
  CHECK (priority IN ('low','normal','high'));

ALTER TABLE tasks
  ADD CONSTRAINT tasks_status_check
  CHECK (status IN ('pending','dispatched','running','done','failed','timed_out'));

ALTER TABLE task_logs
  ADD CONSTRAINT task_logs_stream_check
  CHECK (stream IN ('stdout','stderr'));

ALTER TABLE scheduled_jobs
  ADD CONSTRAINT scheduled_jobs_overlap_policy_check
  CHECK (overlap_policy IN ('skip','allow'));
