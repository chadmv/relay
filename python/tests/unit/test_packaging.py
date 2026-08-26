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
    """
    readme = (_PYTHON_DIR / "README.md").read_text(encoding="utf-8")
    section = readme.split("## Client API", 1)[1].split("\n## ", 1)[0]
    documented = set(re.findall(r"(\w+)\(", section))
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
