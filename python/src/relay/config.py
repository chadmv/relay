from __future__ import annotations

import json
import os
import sys
from dataclasses import dataclass
from pathlib import Path
from typing import Optional


@dataclass
class Config:
    """Resolved client config: server URL and bearer token."""

    server_url: str
    token: str


def default_config_file_path() -> Path:
    """Return the location the CLI uses for ``config.json``.

    On Windows: ``%APPDATA%\\relay\\config.json``. Elsewhere:
    ``~/.relay/config.json``. Mirrors :func:`internal/cli/config.go`.
    """
    if sys.platform == "win32":
        appdata = os.environ.get("APPDATA")
        if not appdata:
            raise RuntimeError("APPDATA is not set")
        return Path(appdata) / "relay" / "config.json"
    home = Path.home()
    return home / ".relay" / "config.json"


def _read_config_file(path: Path) -> tuple[str, str]:
    try:
        data = json.loads(path.read_text(encoding="utf-8"))
    except FileNotFoundError:
        return "", ""
    except (OSError, ValueError):
        # Mirror the CLI's behavior: silently ignore a malformed config.
        return "", ""
    if not isinstance(data, dict):
        return "", ""
    url = data.get("server_url", "") or ""
    token = data.get("token", "") or ""
    return str(url), str(token)


def resolve_config(
    *,
    url: Optional[str] = None,
    token: Optional[str] = None,
    config_path: Optional[Path] = None,
) -> Config:
    """Resolve URL and token in this order:

    1. Explicit kwargs (``url=``, ``token=``).
    2. Environment: ``RELAY_URL``, ``RELAY_TOKEN``.
    3. Config file at ``config_path`` (defaults to the CLI's location).

    Returns a :class:`Config`. Caller is responsible for validating that the
    fields are non-empty for whatever operation it intends to perform.
    """
    file_url, file_token = "", ""
    path = config_path if config_path is not None else _safe_default_path()
    if path is not None:
        file_url, file_token = _read_config_file(path)

    final_url = url or os.environ.get("RELAY_URL") or file_url
    final_token = token or os.environ.get("RELAY_TOKEN") or file_token
    return Config(server_url=final_url, token=final_token)


def _safe_default_path() -> Optional[Path]:
    try:
        return default_config_file_path()
    except RuntimeError:
        return None
