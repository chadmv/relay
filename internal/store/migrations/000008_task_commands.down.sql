-- Reverse 000008. Backfill restores command from the first element of commands.
-- Rows with multi-command tasks will fail loudly here (intentional: the column
-- type can't represent them).
ALTER TABLE tasks ADD COLUMN command TEXT[];

UPDATE tasks
SET command = ARRAY(
    SELECT jsonb_array_elements_text(commands->0)
);

ALTER TABLE tasks ALTER COLUMN command SET NOT NULL;
ALTER TABLE tasks DROP COLUMN commands;
