from pathlib import Path


def configuration(root: Path) -> Path:
    token = root / "credential"
    token.write_text("dummy-fixture-credential", encoding="utf-8")
    token.chmod(0o600)
    path = root / "config.toml"
    path.write_text(
        f'state_dir = "{root / "state"}"\n'
        f'bot_token_file = "{token}"\n'
        'allowed_user_id = 7\nprivate_chat_id = 11\n'
        '[default]\nagent = "claude"\nmodel = "default-model"\n'
        '[[alternatives]]\nagent = "codex"\nmodel = "gpt-5.5"\n',
        encoding="utf-8",
    )
    return path


def question() -> dict:
    return {
        "version": 1,
        "request_id": "design-storage",
        "question": "Which storage?",
        "options": [
            {"id": "sqlite", "label": "SQLite", "tradeoff": "One host"},
            {"id": "files", "label": "Files", "tradeoff": "Manual locking"},
        ],
        "recommendation": "sqlite",
    }
