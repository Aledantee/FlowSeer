import contextlib
import io
import json
import sqlite3
import tempfile
import unittest
from pathlib import Path
from unittest.mock import patch

from harness import bridge
from harness.config import load
from harness.state import Store
from tests.harness import configuration, question


class BridgeTest(unittest.TestCase):
    def setUp(self):
        self.directory = tempfile.TemporaryDirectory()
        self.addCleanup(self.directory.cleanup)
        self.root = Path(self.directory.name)
        self.config_path = configuration(self.root)
        self.store = Store(load(self.config_path))
        self.task = self.store.create_task("Storage", thread_id=3)
        self.request = self.root / "request.json"
        self.request.write_text(json.dumps(question()))

    def invoke(self, command="ask", extra=()):
        output, errors = io.StringIO(), io.StringIO()
        with contextlib.redirect_stdout(output), contextlib.redirect_stderr(errors):
            result = bridge.main([command, "--config", str(self.config_path), "--task", self.task, "--request", str(self.request), *extra])
        return result, output.getvalue(), errors.getvalue()

    def assert_empty(self):
        with sqlite3.connect(self.store.path) as connection:
            for table in ("decisions", "events", "outbound"):
                self.assertEqual(connection.execute(f"SELECT count(*) FROM {table}").fetchone()[0], 0)

    def test_no_options(self):
        self.request.write_text(json.dumps(question() | {"options": []}))
        code, output, error = self.invoke(extra=("--timeout", "0"))
        self.assertEqual((code, output), (2, ""))
        self.assertIn("options", error)
        self.assert_empty()

    def test_oversized_request(self):
        self.request.write_text(json.dumps(question() | {"question": "q" * 65536}))
        code, _, error = self.invoke(extra=("--timeout", "0"))
        self.assertEqual(code, 2)
        self.assertIn("64 KiB", error)
        self.assert_empty()

    def test_unknown_payload_version(self):
        self.request.write_text(json.dumps(question() | {"version": 2}))
        code, _, error = self.invoke(extra=("--timeout", "0"))
        self.assertEqual(code, 2)
        self.assertIn("version", error)
        self.assert_empty()

    def test_option_answer_stdout(self):
        decision = self.store.request_decision(self.task, question())
        callback = self.store.issue_callback(self.task, decision, "sqlite")
        self.store.ingest({"update_id": 1, "kind": "callback", "sender_id": 7, "chat_id": 11, "thread_id": 3, "callback_id": callback})
        code, output, errors = self.invoke()
        self.assertEqual((code, errors), (0, ""))
        self.assertEqual(json.loads(output), {"decision": decision, "revision": 1, "option": "sqlite", "text": None})
        self.assertEqual(len(output.splitlines()), 1)

    def test_custom_answer_stdout(self):
        decision = self.store.request_decision(self.task, question())
        self.store.map_message(self.task, 12, decision)
        self.store.ingest({"update_id": 1, "kind": "message", "sender_id": 7, "chat_id": 11, "thread_id": 3, "reply_to_message_id": 12, "text": "Use both"})
        code, output, errors = self.invoke()
        self.assertEqual((code, errors), (0, ""))
        self.assertEqual(json.loads(output), {"decision": decision, "revision": 1, "option": None, "text": "Use both"})

    def test_cancelled_decision(self):
        decision = self.store.request_decision(self.task, question())
        self.store.cancel_decision(decision)
        self.assertEqual(self.invoke()[:2], (3, ""))

    def test_timeout(self):
        self.assertEqual(self.invoke(extra=("--timeout", "0"))[:2], (4, ""))
        with sqlite3.connect(self.store.path) as connection:
            self.assertEqual(connection.execute("SELECT status FROM decisions").fetchone()[0], "pending")

    def test_polling_waits_for_answer(self):
        def answer(_seconds):
            decision = self.store.request_decision(self.task, question())
            self.store.cancel_decision(decision)

        with patch.object(bridge.time, "sleep", side_effect=answer) as sleep:
            self.assertEqual(self.invoke()[0], 3)
        sleep.assert_called_once_with(1)

    def test_events_and_result_no_checkpoint(self):
        payloads = {
            "progress": {"version": 1, "request_id": "p", "summary": "Working"},
            "result": {"version": 1, "request_id": "r", "status": "completed", "artifacts": ["/tmp/report.md"], "evidence": ["12 tests passed"]},
            "ack": {"version": 1, "request_id": "a", "action": "cancel"},
        }
        for command, payload in payloads.items():
            self.request.write_text(json.dumps(payload))
            self.assertEqual(self.invoke(command), (0, "", ""))
        with sqlite3.connect(self.store.path) as connection:
            self.assertEqual([row[0] for row in connection.execute("SELECT kind FROM events ORDER BY id")], ["progress", "result", "ack"])
            result = json.loads(connection.execute("SELECT payload FROM events WHERE kind = 'result'").fetchone()[0])
            self.assertEqual(result["evidence"], ["12 tests passed"])
            self.assertEqual(result["artifacts"], ["/tmp/report.md"])
            names = [row[0] for row in connection.execute("SELECT name FROM sqlite_master WHERE type = 'table'")]
            self.assertNotIn("checkpoints", names)

    def test_malformed_payloads_store_nothing(self):
        cases = [("ask", {"options": [{"id": "x"}]}), ("progress", {"summary": 2}), ("result", {"status": "completed", "artifacts": [], "evidence": []}), ("ack", {"action": "commit"})]
        for command, fields in cases:
            self.request.write_text(json.dumps({"version": 1, "request_id": "bad", **fields}))
            self.assertEqual(self.invoke(command, ("--timeout", "0") if command == "ask" else ())[0], 2)
            self.assert_empty()

    def test_invalid_json(self):
        for content in (b"{", b"\xff", b"[]"):
            with self.subTest(content=content):
                self.request.write_bytes(content)
                self.assertEqual(self.invoke(extra=("--timeout", "0"))[0], 2)
                self.assert_empty()

    def test_progress_digest_ignores_request_identity(self):
        for request_id in ("first", "second"):
            self.request.write_text(json.dumps({"version": 1, "request_id": request_id, "summary": "Testing"}))
            self.assertEqual(self.invoke("progress"), (0, "", ""))
        with sqlite3.connect(self.store.path) as connection:
            self.assertEqual(connection.execute("SELECT count(*) FROM events").fetchone()[0], 2)
            self.assertEqual(connection.execute("SELECT count(*) FROM outbound").fetchone()[0], 1)
