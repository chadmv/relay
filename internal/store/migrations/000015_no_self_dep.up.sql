ALTER TABLE task_dependencies
    ADD CONSTRAINT no_self_dep CHECK (task_id <> depends_on_task_id);
