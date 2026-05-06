from __future__ import annotations

import json
from pathlib import Path

import pytest

from relay.config import resolve_config


@pytest.fixture(autouse=True)
def _clean_env(monkeypatch: pytest.MonkeyPatch) -> None:
    monkeypatch.delenv("RELAY_URL", raising=False)
    monkeypatch.delenv("RELAY_TOKEN", raising=False)


def _write_config(tmp_path: Path, url: str = "", token: str = "") -> Path:
    path = tmp_path / "config.json"
    path.write_text(json.dumps({"server_url": url, "token": token}))
    return path


def test_kwargs_take_precedence_over_env_and_file(
    tmp_path: Path, monkeypatch: pytest.MonkeyPatch
) -> None:
    monkeypatch.setenv("RELAY_URL", "http://env.example")
    monkeypatch.setenv("RELAY_TOKEN", "env-token")
    path = _write_config(tmp_path, "http://file.example", "file-token")

    cfg = resolve_config(
        url="http://kw.example",
        token="kw-token",
        config_path=path,
    )
    assert cfg.server_url == "http://kw.example"
    assert cfg.token == "kw-token"


def test_env_takes_precedence_over_file(
    tmp_path: Path, monkeypatch: pytest.MonkeyPatch
) -> None:
    monkeypatch.setenv("RELAY_URL", "http://env.example")
    monkeypatch.setenv("RELAY_TOKEN", "env-token")
    path = _write_config(tmp_path, "http://file.example", "file-token")

    cfg = resolve_config(config_path=path)
    assert cfg.server_url == "http://env.example"
    assert cfg.token == "env-token"


def test_file_used_when_no_kwargs_or_env(tmp_path: Path) -> None:
    path = _write_config(tmp_path, "http://file.example", "file-token")
    cfg = resolve_config(config_path=path)
    assert cfg.server_url == "http://file.example"
    assert cfg.token == "file-token"


def test_missing_file_returns_empty(tmp_path: Path) -> None:
    cfg = resolve_config(config_path=tmp_path / "nope.json")
    assert cfg.server_url == ""
    assert cfg.token == ""


def test_malformed_file_returns_empty(tmp_path: Path) -> None:
    path = tmp_path / "config.json"
    path.write_text("{not json")
    cfg = resolve_config(config_path=path)
    assert cfg.server_url == ""
    assert cfg.token == ""


def test_partial_env_override(tmp_path: Path, monkeypatch: pytest.MonkeyPatch) -> None:
    """RELAY_TOKEN alone overrides token but file URL still wins for URL."""
    path = _write_config(tmp_path, "http://file.example", "file-token")
    monkeypatch.setenv("RELAY_TOKEN", "env-token")
    cfg = resolve_config(config_path=path)
    assert cfg.server_url == "http://file.example"
    assert cfg.token == "env-token"
