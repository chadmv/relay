from __future__ import annotations

import re
from pathlib import Path

from relay import Client, __version__

_PYTHON_DIR = Path(__file__).resolve().parents[2]


def test_readme_client_api_table_documents_every_public_method() -> None:
    """D6: the table listed 15 of 25 methods. The pagination work added ten
    siblings and did not update it, and nothing noticed for three months.

    The search is scoped to the "## Client API" section on purpose. Searching
    the whole README would match the quickstart's `client.submit(job)` and the
    authoring section's `add_task(...)`, and the guard would then pass against
    an EMPTY table - a guard that cannot see its own subject.

    Section-scoping is necessary and NOT sufficient, which is how this guard
    was fail-open for the method the same PR added. `### Reading a task's log`
    is a SUBsection - the split is on "\n## " and does not stop at "###" - so
    its code sample is inside the scope, and a bare `(\w+)\(` matched
    `client.task_logs_page(...)` there. Deleting the task_logs_page TABLE ROW
    left the guard green; only deleting the row and the code sample together
    turned it red. So the match is anchored to the first cell of a table row,
    where a method is DOCUMENTED, rather than to any word followed by a paren
    anywhere in the section. Splitting on "|" rather than anchoring "^\\|`"
    keeps the rows that document several siblings in one cell
    (`get_schedule(id)` / `update_schedule(id, ...)` / `delete_schedule(id)`).
    """
    readme = (_PYTHON_DIR / "README.md").read_text(encoding="utf-8")
    section = readme.split("## Client API", 1)[1].split("\n## ", 1)[0]
    documented: set[str] = set()
    for line in section.splitlines():
        if not line.startswith("|"):
            continue
        first_cell = line.split("|")[1]
        documented.update(re.findall(r"`(\w+)\(", first_cell))
    public = {
        name
        for name in dir(Client)
        if not name.startswith("_") and callable(getattr(Client, name))
    }
    assert sorted(public - documented) == []


def test_version_files_are_in_lockstep() -> None:
    """pyproject.toml and _version.py are two hand-maintained copies of one
    number. This is what makes bumping one of them RED.
    """
    pyproject = (_PYTHON_DIR / "pyproject.toml").read_text(encoding="utf-8")
    match = re.search(r'^version = "([^"]+)"', pyproject, re.MULTILINE)
    assert match is not None, "pyproject.toml has no [project] version"
    assert match.group(1) == __version__
