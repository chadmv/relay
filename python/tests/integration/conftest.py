from __future__ import annotations

import os

import pytest

import relay

_INTEGRATION_DIR = os.path.dirname(__file__)


def pytest_collection_modifyitems(config: pytest.Config, items: list[pytest.Item]) -> None:
    """Skip integration tests unless RELAY_INTEGRATION=1 is set.

    Only items collected from this directory are touched, so unit tests run
    normally even when this conftest is in scope. Integration tests need a
    running relay-server reachable via RELAY_URL and a valid RELAY_TOKEN,
    plus at least one online agent able to run the submitted task.
    """
    if os.environ.get("RELAY_INTEGRATION") == "1":
        return
    skip = pytest.mark.skip(reason="set RELAY_INTEGRATION=1 to run")
    for item in items:
        try:
            item_path = str(item.path)
        except AttributeError:  # pytest < 7 fallback
            item_path = item.fspath.strpath
        if item_path.startswith(_INTEGRATION_DIR):
            item.add_marker(skip)


@pytest.fixture
def client() -> relay.Client:
    if not os.environ.get("RELAY_TOKEN"):
        pytest.skip("RELAY_TOKEN not set")
    return relay.Client()
