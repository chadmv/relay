-- Replace tasks.command TEXT[] with tasks.commands JSONB so a task can carry
-- multiple commands that the agent runs sequentially in the same workspace.
ALTER TABLE tasks ADD COLUMN commands JSONB NOT NULL DEFAULT '[]'::jsonb;

-- Backfill: each existing single command becomes a one-element array of argvs.
UPDATE tasks
SET commands = jsonb_build_array(to_jsonb(command));

ALTER TABLE tasks DROP COLUMN command;
