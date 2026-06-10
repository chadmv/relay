-- Reverse 000008. Backfill restores command from the first element of commands.
-- Guard: command TEXT[] cannot represent more than one argv, so refuse to
-- downgrade while multi-command tasks exist rather than silently dropping
-- their remaining commands.
DO $$
DECLARE
    n bigint;
BEGIN
    SELECT COUNT(*) INTO n FROM tasks WHERE jsonb_array_length(commands) > 1;
    IF n > 0 THEN
        RAISE EXCEPTION 'cannot downgrade 000008_task_commands: % multi-command task(s) exist; command TEXT[] cannot represent them', n;
    END IF;
END $$;

ALTER TABLE tasks ADD COLUMN command TEXT[];

UPDATE tasks
SET command = ARRAY(
    SELECT jsonb_array_elements_text(commands->0)
);

ALTER TABLE tasks ALTER COLUMN command SET NOT NULL;
ALTER TABLE tasks DROP COLUMN commands;
