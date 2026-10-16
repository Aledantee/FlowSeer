import hashlib
import json
import secrets
import sqlite3
from contextlib import contextmanager
from typing import Iterator

from harness.config import AgentModel, Config, outside_checkout


def encode(value: object) -> str:
    return json.dumps(value, sort_keys=True, separators=(",", ":"), ensure_ascii=False, allow_nan=False)


class Store:
    def __init__(self, config: Config):
        self.config = config
        directory = outside_checkout(config.state_dir)
        directory.mkdir(mode=0o700, parents=True, exist_ok=True)
        self.path = outside_checkout(directory / "state.sqlite3")
        with self._write() as connection:
            version = connection.execute("PRAGMA user_version").fetchone()[0]
            if version not in (0, 1):
                raise ValueError(f"unsupported schema version: {version}")
            if version == 0:
                self._schema(connection)
                connection.execute("PRAGMA user_version = 1")
        self.path.chmod(0o600)

    def _connect(self) -> sqlite3.Connection:
        connection = sqlite3.connect(self.path, timeout=10, isolation_level=None)
        connection.row_factory = sqlite3.Row
        connection.execute("PRAGMA foreign_keys = ON")
        connection.execute("PRAGMA busy_timeout = 10000")
        return connection

    @contextmanager
    def _write(self) -> Iterator[sqlite3.Connection]:
        connection = self._connect()
        try:
            connection.execute("BEGIN IMMEDIATE")
            yield connection
            connection.commit()
        except BaseException:
            connection.rollback()
            raise
        finally:
            connection.close()

    def _schema(self, connection: sqlite3.Connection) -> None:
        statements = (
            "CREATE TABLE tasks (id TEXT PRIMARY KEY, brief TEXT NOT NULL, agent TEXT NOT NULL, model TEXT NOT NULL, revision INTEGER NOT NULL DEFAULT 1, commit_id TEXT, diff_digest TEXT, status TEXT NOT NULL DEFAULT 'active')",
            "CREATE TABLE topics (task_id TEXT PRIMARY KEY REFERENCES tasks(id), chat_id INTEGER NOT NULL, thread_id INTEGER NOT NULL, UNIQUE(chat_id, thread_id))",
            "CREATE TABLE decisions (id TEXT PRIMARY KEY, task_id TEXT NOT NULL REFERENCES tasks(id), revision INTEGER NOT NULL, commit_id TEXT, diff_digest TEXT, request_id TEXT NOT NULL, payload TEXT NOT NULL, status TEXT NOT NULL DEFAULT 'pending' CHECK(status IN ('pending','answered','cancelled')), UNIQUE(task_id, request_id))",
            "CREATE TABLE drafts (chat_id INTEGER NOT NULL, thread_id INTEGER NOT NULL, draft_id INTEGER NOT NULL, task_id TEXT NOT NULL REFERENCES tasks(id), PRIMARY KEY(chat_id, thread_id, draft_id))",
            "CREATE TABLE messages (chat_id INTEGER NOT NULL, thread_id INTEGER NOT NULL, message_id INTEGER NOT NULL, task_id TEXT NOT NULL REFERENCES tasks(id), decision_id TEXT REFERENCES decisions(id), PRIMARY KEY(chat_id, message_id))",
            "CREATE TABLE callbacks (id TEXT PRIMARY KEY, task_id TEXT REFERENCES tasks(id), decision_id TEXT REFERENCES decisions(id), revision INTEGER, option_id TEXT, agent TEXT, model TEXT, brief TEXT, card_id TEXT, used INTEGER NOT NULL DEFAULT 0)",
            "CREATE TABLE inbound (update_id TEXT PRIMARY KEY, payload TEXT NOT NULL, result TEXT NOT NULL)",
            "CREATE TABLE events (id INTEGER PRIMARY KEY, task_id TEXT NOT NULL REFERENCES tasks(id), kind TEXT NOT NULL, payload TEXT NOT NULL, event_key TEXT UNIQUE)",
            "CREATE TABLE operations (id TEXT PRIMARY KEY, task_id TEXT NOT NULL REFERENCES tasks(id), kind TEXT NOT NULL, operation_key TEXT NOT NULL UNIQUE, state TEXT NOT NULL CHECK(state IN ('intended','running','succeeded','failed','blocked')))",
            "CREATE UNIQUE INDEX one_open_operation ON operations(task_id) WHERE state IN ('intended','running')",
            "CREATE TABLE answers (decision_id TEXT PRIMARY KEY REFERENCES decisions(id), revision INTEGER NOT NULL, option_id TEXT, text TEXT, CHECK((option_id IS NULL) != (text IS NULL)))",
            "CREATE TABLE outbound (id INTEGER PRIMARY KEY, task_id TEXT NOT NULL REFERENCES tasks(id), kind TEXT NOT NULL, idempotency_key TEXT NOT NULL UNIQUE, digest TEXT NOT NULL, payload TEXT NOT NULL, state TEXT NOT NULL DEFAULT 'queued')",
        )
        for statement in statements:
            connection.execute(statement)

    def _read(self, query: str, args: tuple = ()) -> dict | None:
        connection = self._connect()
        try:
            row = connection.execute(query, args).fetchone()
            return dict(row) if row else None
        finally:
            connection.close()

    def task(self, task_id: str) -> dict:
        task = self._read("SELECT * FROM tasks WHERE id = ?", (task_id,))
        if task is None:
            raise ValueError("unknown task")
        return task

    def _task(self, connection: sqlite3.Connection, task_id: str) -> sqlite3.Row:
        task = connection.execute("SELECT * FROM tasks WHERE id = ?", (task_id,)).fetchone()
        if task is None:
            raise ValueError("unknown task")
        return task

    def _create_task(self, connection: sqlite3.Connection, brief: str, pair: AgentModel, thread_id: int | None) -> str:
        if not isinstance(brief, str) or not brief.strip():
            raise ValueError("task brief must be nonempty")
        task_id = secrets.token_hex(16)
        connection.execute("INSERT INTO tasks (id, brief, agent, model) VALUES (?,?,?,?)", (task_id, brief, pair.agent, pair.model))
        if thread_id is not None:
            connection.execute("INSERT INTO topics VALUES (?,?,?)", (task_id, self.config.private_chat_id, thread_id))
        return task_id

    def create_task(self, brief: str, thread_id: int | None = None) -> str:
        with self._write() as connection:
            return self._create_task(connection, brief, self.config.default, thread_id)

    def attach_topic(self, task_id: str, thread_id: int) -> None:
        with self._write() as connection:
            self._task(connection, task_id)
            connection.execute("INSERT INTO topics VALUES (?,?,?)", (task_id, self.config.private_chat_id, thread_id))

    def revise_task(self, task_id: str, commit: str | None = None, diff_digest: str | None = None) -> int:
        with self._write() as connection:
            task = self._task(connection, task_id)
            revision = task["revision"] + 1
            connection.execute("UPDATE tasks SET revision = ?, commit_id = ?, diff_digest = ? WHERE id = ?", (revision, commit, diff_digest, task_id))
            connection.execute("UPDATE decisions SET status = 'cancelled' WHERE task_id = ? AND status = 'pending'", (task_id,))
            return revision

    def request_decision(self, task_id: str, payload: dict) -> str:
        with self._write() as connection:
            task = self._task(connection, task_id)
            old = connection.execute("SELECT * FROM decisions WHERE task_id = ? AND request_id = ?", (task_id, payload["request_id"])).fetchone()
            if old:
                if old["payload"] != encode(payload):
                    raise ValueError("request_id already names a different question")
                return old["id"]
            decision_id = secrets.token_hex(16)
            connection.execute("INSERT INTO decisions (id, task_id, revision, commit_id, diff_digest, request_id, payload) VALUES (?,?,?,?,?,?,?)", (decision_id, task_id, task["revision"], task["commit_id"], task["diff_digest"], payload["request_id"], encode(payload)))
            self._queue(connection, task_id, "decision", payload | {"decision": decision_id}, f"decision:{decision_id}")
            return decision_id

    def decision(self, decision_id: str) -> dict:
        row = self._read("SELECT * FROM decisions WHERE id = ?", (decision_id,))
        if row is None:
            raise ValueError("unknown decision")
        return row

    def answer(self, decision_id: str) -> dict | None:
        row = self._read("SELECT decision_id AS decision, revision, option_id AS option, text FROM answers WHERE decision_id = ?", (decision_id,))
        return row

    def cancel_decision(self, decision_id: str) -> None:
        with self._write() as connection:
            connection.execute("UPDATE decisions SET status = 'cancelled' WHERE id = ? AND status = 'pending'", (decision_id,))

    def issue_callback(self, task_id: str, decision_id: str, option: str | None) -> str:
        with self._write() as connection:
            decision = connection.execute("SELECT * FROM decisions WHERE id = ? AND task_id = ?", (decision_id, task_id)).fetchone()
            if not decision or decision["status"] != "pending":
                raise ValueError("no pending decision for task")
            options = json.loads(decision["payload"])["options"]
            if option is not None and option not in {item["id"] for item in options}:
                raise ValueError("unknown option")
            callback = secrets.token_hex(16)
            connection.execute("INSERT INTO callbacks (id, task_id, decision_id, revision, option_id) VALUES (?,?,?,?,?)", (callback, task_id, decision_id, decision["revision"], option))
            return callback

    def issue_agent_option(self, pair: AgentModel, brief: str | None = None) -> str:
        if pair not in (self.config.default, *self.config.alternatives):
            raise ValueError("unconfigured agent and model")
        return self._issue_agent_options((pair,), brief)[0]

    def issue_agent_options(self, brief: str) -> tuple[str, ...]:
        return self._issue_agent_options((self.config.default, *self.config.alternatives), brief)

    def _issue_agent_options(self, pairs: tuple[AgentModel, ...], brief: str | None) -> tuple[str, ...]:
        if brief is not None and (not isinstance(brief, str) or not brief.strip()):
            raise ValueError("task brief must be nonempty")
        with self._write() as connection:
            card_id = secrets.token_hex(16)
            callbacks = []
            for pair in pairs:
                callback = secrets.token_hex(16)
                connection.execute("INSERT INTO callbacks (id, agent, model, brief, card_id) VALUES (?,?,?,?,?)", (callback, pair.agent, pair.model, brief, card_id))
                callbacks.append(callback)
            return tuple(callbacks)

    def _topic(self, connection: sqlite3.Connection, task_id: str) -> sqlite3.Row:
        topic = connection.execute("SELECT * FROM topics WHERE task_id = ?", (task_id,)).fetchone()
        if topic is None:
            raise ValueError("task has no topic")
        return topic

    def map_message(self, task_id: str, message_id: int, decision_id: str | None = None) -> None:
        with self._write() as connection:
            topic = self._topic(connection, task_id)
            if decision_id is not None and not connection.execute("SELECT 1 FROM decisions WHERE id = ? AND task_id = ?", (decision_id, task_id)).fetchone():
                raise ValueError("decision belongs to another task")
            connection.execute("INSERT INTO messages VALUES (?,?,?,?,?)", (topic["chat_id"], topic["thread_id"], message_id, task_id, decision_id))

    def map_draft(self, task_id: str, draft_id: int) -> None:
        with self._write() as connection:
            topic = self._topic(connection, task_id)
            connection.execute("INSERT INTO drafts VALUES (?,?,?,?)", (topic["chat_id"], topic["thread_id"], draft_id, task_id))

    def _event(self, connection: sqlite3.Connection, task_id: str, kind: str, payload: dict, key: str | None = None) -> bool:
        cursor = connection.execute("INSERT INTO events (task_id, kind, payload, event_key) VALUES (?,?,?,?) ON CONFLICT(event_key) DO NOTHING", (task_id, kind, encode(payload), key))
        return cursor.rowcount == 1

    def _queue(self, connection: sqlite3.Connection, task_id: str, kind: str, payload: dict, key: str) -> bool:
        content = encode(payload)
        digest = hashlib.sha256(content.encode()).hexdigest()
        if kind == "progress":
            last = connection.execute("SELECT digest FROM outbound WHERE task_id = ? AND kind = 'progress' ORDER BY id DESC LIMIT 1", (task_id,)).fetchone()
            if last and last["digest"] == digest:
                return False
        cursor = connection.execute("INSERT INTO outbound (task_id, kind, idempotency_key, digest, payload) VALUES (?,?,?,?,?) ON CONFLICT(idempotency_key) DO NOTHING", (task_id, kind, key, digest, content))
        return cursor.rowcount == 1

    def queue_notification(self, task_id: str, kind: str, payload: dict, key: str) -> bool:
        with self._write() as connection:
            self._task(connection, task_id)
            return self._queue(connection, task_id, kind, payload, key)

    def record_event(self, task_id: str, kind: str, payload: dict) -> None:
        with self._write() as connection:
            self._task(connection, task_id)
            key = f"bridge:{task_id}:{kind}:{payload['request_id']}"
            old = connection.execute("SELECT payload FROM events WHERE event_key = ?", (key,)).fetchone()
            if old and old["payload"] != encode(payload):
                raise ValueError("request_id already names a different event")
            if self._event(connection, task_id, kind, payload, key) and kind in ("progress", "result"):
                content = {name: value for name, value in payload.items() if name not in ("version", "request_id")}
                self._queue(connection, task_id, kind, content, key)

    def start_operation(self, task_id: str, kind: str, key: str) -> str:
        with self._write() as connection:
            self._task(connection, task_id)
            if connection.execute("SELECT 1 FROM operations WHERE task_id = ? AND state IN ('intended','running')", (task_id,)).fetchone():
                raise ValueError("task already has an open operation")
            operation_id = secrets.token_hex(16)
            connection.execute("INSERT INTO operations VALUES (?,?,?,?,?)", (operation_id, task_id, kind, key, "intended"))
            return operation_id

    def finish_operation(self, operation_id: str, state: str) -> None:
        if state not in ("succeeded", "failed", "blocked"):
            raise ValueError("operation needs a terminal state")
        with self._write() as connection:
            cursor = connection.execute("UPDATE operations SET state = ? WHERE id = ? AND state IN ('intended','running')", (state, operation_id))
            if cursor.rowcount != 1:
                raise ValueError("operation is not open")

    def ingest(self, update: dict) -> dict:
        if type(update.get("update_id")) is not int:
            raise ValueError("update_id must be an integer")
        update_id = str(update["update_id"])
        with self._write() as connection:
            old = connection.execute("SELECT result FROM inbound WHERE update_id = ?", (update_id,)).fetchone()
            if old:
                return json.loads(old["result"])
            connection.execute("SAVEPOINT inbound_effect")
            try:
                result = self._apply(connection, update)
            except sqlite3.IntegrityError:
                connection.execute("ROLLBACK TO inbound_effect")
                result = self._result("state_conflict")
            connection.execute("RELEASE inbound_effect")
            connection.execute("INSERT INTO inbound VALUES (?,?,?)", (update_id, encode(update), encode(result)))
            return result

    def _result(self, reason: str, **fields) -> dict:
        return {"accepted": reason == "accepted", "reason": reason, **fields}

    def _apply(self, connection: sqlite3.Connection, update: dict) -> dict:
        kind = update.get("kind")
        if type(update.get("chat_id")) is not int or update["chat_id"] != self.config.private_chat_id:
            return self._result("unauthorized")
        if kind != "stop" and (type(update.get("sender_id")) is not int or update["sender_id"] != self.config.allowed_user_id):
            return self._result("unauthorized")
        thread_id = update.get("thread_id")
        if thread_id is not None and (type(thread_id) is not int or not 0 < thread_id < 2 ** 63):
            return self._result("invalid_topic")
        if kind == "stop":
            draft_id = update.get("draft_id")
            if type(draft_id) is not int or not 0 < draft_id < 2 ** 63:
                return self._result("draft_mismatch")
            draft = connection.execute("SELECT task_id FROM drafts WHERE chat_id = ? AND thread_id = ? AND draft_id = ?", (update["chat_id"], update.get("thread_id"), update.get("draft_id"))).fetchone()
            if not draft:
                return self._result("draft_mismatch")
            task_id = draft["task_id"]
            self._event(connection, task_id, "cancel_requested", update)
            connection.execute("UPDATE tasks SET status = 'cancel_requested' WHERE id = ?", (task_id,))
            return self._result("accepted", task_id=task_id)
        if kind == "new_task":
            return self._new_task(connection, update, self.config.default)
        if kind == "callback":
            if not isinstance(update.get("callback_id"), str):
                return self._result("unknown_callback")
            callback = connection.execute("SELECT * FROM callbacks WHERE id = ?", (update.get("callback_id"),)).fetchone()
            if callback is None:
                return self._result("unknown_callback")
            if callback["agent"] is not None:
                if callback["used"]:
                    return self._result("callback_used")
                request = update if callback["brief"] is None else update | {"brief": callback["brief"]}
                result = self._new_task(connection, request, AgentModel(callback["agent"], callback["model"]))
                if result["accepted"]:
                    connection.execute("UPDATE callbacks SET used = 1 WHERE card_id = ?", (callback["card_id"],))
                return result
            return self._respond(connection, update, callback["task_id"], callback["decision_id"], callback["option_id"], None)
        if kind == "message":
            message_id = update.get("reply_to_message_id")
            if type(message_id) is not int or not 0 < message_id < 2 ** 63:
                return self._result("message_mismatch")
            message = connection.execute("SELECT * FROM messages WHERE chat_id = ? AND message_id = ? AND thread_id = ?", (update["chat_id"], update.get("reply_to_message_id"), update.get("thread_id"))).fetchone()
            if message is None or message["decision_id"] is None:
                return self._result("message_mismatch")
            text = update.get("text")
            if not isinstance(text, str) or not text.strip():
                return self._result("empty_answer")
            return self._respond(connection, update, message["task_id"], message["decision_id"], None, text)
        return self._result("unsupported_update")

    def _new_task(self, connection: sqlite3.Connection, update: dict, pair: AgentModel) -> dict:
        brief = update.get("brief")
        thread_id = update.get("thread_id")
        if not isinstance(brief, str) or not brief.strip():
            return self._result("missing_brief")
        if thread_id is not None and (type(thread_id) is not int or thread_id <= 0):
            return self._result("invalid_topic")
        if thread_id is not None and connection.execute("SELECT 1 FROM topics WHERE chat_id = ? AND thread_id = ?", (update["chat_id"], thread_id)).fetchone():
            return self._result("topic_in_use")
        task_id = self._create_task(connection, brief, pair, thread_id)
        self._event(connection, task_id, "task_created", {"brief": brief})
        return self._result("accepted", task_id=task_id, thread_id=thread_id)

    def _respond(self, connection: sqlite3.Connection, update: dict, task_id: str, decision_id: str, option: str | None, text: str | None) -> dict:
        topic = connection.execute("SELECT * FROM topics WHERE task_id = ?", (task_id,)).fetchone()
        if topic is None:
            return self._result("no_topic")
        if topic["chat_id"] != update["chat_id"] or topic["thread_id"] != update.get("thread_id"):
            return self._result("topic_mismatch")
        decision = connection.execute("SELECT * FROM decisions WHERE id = ? AND task_id = ?", (decision_id, task_id)).fetchone()
        task = self._task(connection, task_id)
        if decision["revision"] != task["revision"] or decision["commit_id"] != task["commit_id"] or decision["diff_digest"] != task["diff_digest"]:
            return self._result("obsolete_revision")
        if decision["status"] == "answered":
            return self._result("already_answered")
        if decision["status"] == "cancelled":
            return self._result("cancelled")
        if option is None and text is None:
            self._event(connection, task_id, "custom_answer_requested", {"decision": decision_id})
            return self._result("accepted", task_id=task_id, decision=decision_id)
        connection.execute("INSERT INTO answers VALUES (?,?,?,?)", (decision_id, decision["revision"], option, text))
        connection.execute("UPDATE decisions SET status = 'answered' WHERE id = ?", (decision_id,))
        self._event(connection, task_id, "decision_answered", {"decision": decision_id, "option": option, "text": text})
        return self._result("accepted", task_id=task_id, decision=decision_id)
