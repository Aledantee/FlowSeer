import os
import stat
import tomllib
from dataclasses import dataclass
from pathlib import Path


@dataclass(frozen=True)
class AgentModel:
    agent: str
    model: str


@dataclass(frozen=True)
class Config:
    state_dir: Path
    bot_token_file: Path
    allowed_user_id: int
    private_chat_id: int
    default: AgentModel
    alternatives: tuple[AgentModel, ...]


def outside_checkout(path: Path) -> Path:
    path = path.expanduser().resolve()
    if path.is_relative_to(Path(__file__).resolve().parents[3]):
        raise ValueError("configuration, state, and credentials must be outside the checkout")
    return path


def _pair(value: object) -> AgentModel:
    if not isinstance(value, dict) or set(value) != {"agent", "model"}:
        raise ValueError("agent and model must be present")
    if any(not isinstance(item, str) or not item.strip() for item in value.values()):
        raise ValueError("agent and model must be nonempty strings")
    return AgentModel(**value)


def load(path: Path | str | None = None) -> Config:
    selected = path if path is not None else os.environ.get("FLOWSEER_HARNESS_CONFIG")
    if not selected:
        raise ValueError("set FLOWSEER_HARNESS_CONFIG or pass --config")
    source = outside_checkout(Path(selected))
    with source.open("rb") as stream:
        data = tomllib.load(stream)
    fields = {"state_dir", "bot_token_file", "allowed_user_id", "private_chat_id", "default", "alternatives"}
    missing = fields - data.keys()
    if missing:
        raise ValueError(f"missing configuration fields: {', '.join(sorted(missing))}")
    if data.keys() - fields:
        raise ValueError("unknown configuration field")
    for field in ("allowed_user_id", "private_chat_id"):
        if type(data[field]) is not int or data[field] <= 0:
            raise ValueError(f"{field} must be a positive integer")
    paths = {}
    for field in ("state_dir", "bot_token_file"):
        if not isinstance(data[field], str) or not data[field].strip():
            raise ValueError(f"{field} must be a path")
        paths[field] = outside_checkout(source.parent / Path(data[field]).expanduser())
    token_stat = paths["bot_token_file"].stat()
    if not stat.S_ISREG(token_stat.st_mode) or token_stat.st_mode & 0o077:
        raise ValueError("bot token file permissions must exclude group and others")
    if not isinstance(data["alternatives"], list):
        raise ValueError("alternatives must be an array")
    default = _pair(data["default"])
    alternatives = tuple(_pair(item) for item in data["alternatives"])
    if len(set((default, *alternatives))) != 1 + len(alternatives):
        raise ValueError("agent and model alternatives must be distinct")
    return Config(**paths, allowed_user_id=data["allowed_user_id"], private_chat_id=data["private_chat_id"], default=default, alternatives=alternatives)
