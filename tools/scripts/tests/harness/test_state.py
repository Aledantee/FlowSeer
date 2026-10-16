import concurrent.futures
import json
import os
import sqlite3
import subprocess
import sys
import tempfile
import threading
import unittest
from pathlib import Path
from unittest.mock import patch

from harness.config import load
from harness.state import Store
from tests.harness import configuration, question


class StateTest(unittest.TestCase):
    def setUp(self):
        self.directory = tempfile.TemporaryDirectory()
        self.addCleanup(self.directory.cleanup)
        self.root = Path(self.directory.name)
        self.config_path = configuration(self.root)
        self.config = load(self.config_path)
        self.store = Store(self.config)
        self.task = self.store.create_task("Storage", thread_id=3)
        self.decision = self.store.request_decision(self.task, question())
        self.callback = self.store.issue_callback(self.task, self.decision, "sqlite")

    def rows(self, table):
        with sqlite3.connect(self.store.path) as connection:
            connection.row_factory = sqlite3.Row
            return [dict(row) for row in connection.execute(f"SELECT * FROM {table}")]

    def update(self, update_id=1, **fields):
        return {"update_id": update_id, "kind": "callback", "sender_id": 7, "chat_id": 11, "thread_id": 3, "callback_id": self.callback, **fields}

    def test_rejected_sender(self):
        queued = self.rows("outbound")
        for number, fields in enumerate(({"sender_id": 42}, {"chat_id": 42}), 1):
            result = self.store.ingest(self.update(number, **fields))
            self.assertEqual(result["reason"], "unauthorized")
        self.assertEqual(len(self.rows("inbound")), 2)
        self.assertEqual(self.rows("events"), [])
        self.assertEqual(self.rows("outbound"), queued)

    def test_replayed_task_update(self):
        update = self.update(kind="new_task", brief="Test replay", thread_id=4)
        first = self.store.ingest(update)
        self.assertTrue(first["accepted"])
        self.assertEqual(self.store.ingest(update), first)
        reopened = Store(self.config)
        self.assertEqual(reopened.ingest(update), first)
        self.assertEqual(len(self.rows("tasks")), 2)
        self.assertEqual(first["thread_id"], 4)

    def test_double_answer(self):
        first = self.store.ingest(self.update())
        self.assertTrue(first["accepted"])
        self.assertEqual(self.store.ingest(self.update(2))["reason"], "already_answered")
        self.assertEqual(len(self.rows("answers")), 1)
        self.assertEqual(self.store.answer(self.decision)["option"], "sqlite")

    def test_cross_task_topic(self):
        self.store.create_task("Other", thread_id=4)
        self.assertEqual(self.store.ingest(self.update(thread_id=4))["reason"], "topic_mismatch")
        self.assertEqual(self.rows("answers"), [])
        self.assertEqual(self.rows("events"), [])

    def test_obsolete_revision(self):
        self.store.revise_task(self.task, commit="abc", diff_digest="changed")
        self.assertEqual(self.store.ingest(self.update())["reason"], "obsolete_revision")
        self.assertEqual(self.rows("answers"), [])

    def test_unissued_callback(self):
        self.assertEqual(self.store.ingest(self.update(callback_id="forged"))["reason"], "unknown_callback")
        self.assertEqual(self.rows("answers"), [])

    def test_topicless_callback_is_durably_rejected(self):
        task = self.store.create_task("No topic")
        decision = self.store.request_decision(task, question())
        callback = self.store.issue_callback(task, decision, "sqlite")
        update = self.update(callback_id=callback, thread_id=None)
        try:
            result = self.store.ingest(update)
        except ValueError as error:
            self.fail(f"ingest raised instead of recording rejection: {error}")
        self.assertEqual(result["reason"], "no_topic")
        self.assertEqual(Store(self.config).ingest(update), result)
        self.assertEqual(len(self.rows("inbound")), 1)
        self.assertEqual(self.rows("events"), [])
        self.assertEqual(self.rows("answers"), [])
        self.assertTrue(self.store.ingest(self.update(2))["accepted"])

    def test_malformed_routing_ids_are_durably_rejected(self):
        self.store.map_draft(self.task, 9)
        self.store.map_message(self.task, 12, self.decision)
        cases = []
        for value in ([], {}, True, 1.5, "3", 2 ** 80):
            cases.extend((
                {"callback_id": value},
                {"thread_id": value},
                {"kind": "stop", "thread_id": value, "draft_id": 9},
                {"kind": "message", "thread_id": value, "reply_to_message_id": 12, "text": "Answer"},
                {"kind": "stop", "draft_id": value},
                {"kind": "message", "reply_to_message_id": value, "text": "Answer"},
            ))
        for number, fields in enumerate(cases, 1):
            with self.subTest(fields=fields):
                update = self.update(number, **fields)
                try:
                    result = self.store.ingest(update)
                except (sqlite3.Error, OverflowError, ValueError) as error:
                    self.fail(f"ingest raised instead of recording rejection: {error}")
                self.assertFalse(result["accepted"])
                self.assertEqual(Store(self.config).ingest(update), result)
        self.assertEqual(len(self.rows("inbound")), len(cases))
        self.assertEqual(self.rows("events"), [])
        self.assertEqual(self.rows("answers"), [])

    def test_topic_constraint_rejection_rolls_back_task(self):
        create = self.store._create_task

        def occupied_topic(connection, brief, pair, thread_id):
            return create(connection, brief, pair, 3)

        update = self.update(kind="new_task", brief="Conflict", thread_id=4)
        with patch.object(self.store, "_create_task", side_effect=occupied_topic):
            try:
                result = self.store.ingest(update)
            except sqlite3.IntegrityError as error:
                self.fail(f"ingest raised instead of recording rejection: {error}")
        self.assertEqual(result["reason"], "state_conflict")
        self.assertEqual(Store(self.config).ingest(update), result)
        self.assertEqual(len(self.rows("tasks")), 1)
        self.assertEqual(len(self.rows("inbound")), 1)
        self.assertEqual(self.rows("events"), [])

    def test_large_integer_update_id_is_durable(self):
        update = self.update(2 ** 80, callback_id="unknown")
        try:
            result = self.store.ingest(update)
        except (sqlite3.Error, OverflowError) as error:
            self.fail(f"ingest raised instead of recording rejection: {error}")
        self.assertEqual(result["reason"], "unknown_callback")
        self.assertEqual(Store(self.config).ingest(update), result)
        self.assertEqual(len(self.rows("inbound")), 1)

    def test_concurrent_writers_one_answer(self):
        barrier = threading.Barrier(2)

        def submit(number):
            store = Store(self.config)
            barrier.wait(timeout=5)
            return store.ingest(self.update(number))

        with concurrent.futures.ThreadPoolExecutor(2) as pool:
            results = list(pool.map(submit, (1, 2)))
        self.assertEqual(sorted(result["reason"] for result in results), ["accepted", "already_answered"])
        self.assertEqual(len(self.rows("answers")), 1)
        self.assertEqual(len(self.rows("inbound")), 2)

    def test_second_open_operation(self):
        operation = self.store.start_operation(self.task, "launch", "launch-1")
        with self.assertRaisesRegex(ValueError, "open operation"):
            self.store.start_operation(self.task, "commit", "commit-1")
        self.store.finish_operation(operation, "succeeded")
        self.assertNotEqual(self.store.start_operation(self.task, "commit", "commit-1"), operation)

    def test_process_death_rolls_back_real_write(self):
        code = """
import os, sys
from harness.config import load
from harness.state import Store
store = Store(load(sys.argv[1]))
def die(connection, *args):
    assert connection.execute("SELECT count(*) FROM tasks").fetchone()[0] == 2
    os._exit(23)
store._event = die
store.ingest({"update_id": 99, "kind": "new_task", "sender_id": 7,
              "chat_id": 11, "thread_id": 9, "brief": "Uncommitted"})
"""
        result = subprocess.run(
            [sys.executable, "-c", code, str(self.config_path)],
            env={
                **os.environ,
                "PYTHONPATH": str(Path(__file__).resolve().parents[2]),
                "PYTHONDONTWRITEBYTECODE": "1",
            },
            capture_output=True,
            text=True,
            timeout=10,
        )
        self.assertEqual(result.returncode, 23, result.stderr)
        self.assertEqual(len(self.rows("tasks")), 1)
        self.assertEqual(self.rows("inbound"), [])
        self.assertEqual(self.rows("events"), [])
        self.assertEqual([row["thread_id"] for row in self.rows("topics")], [3])

    def test_duplicate_notification_key(self):
        self.assertTrue(self.store.queue_notification(self.task, "complete", {"summary": "Done"}, "one"))
        self.assertFalse(self.store.queue_notification(self.task, "complete", {"summary": "Changed"}, "one"))
        queued = [row for row in self.rows("outbound") if row["kind"] == "complete"]
        self.assertEqual(len(queued), 1)
        self.assertEqual(json.loads(queued[0]["payload"]), {"summary": "Done"})

    def test_repeated_progress_digest(self):
        self.assertTrue(self.store.queue_notification(self.task, "progress", {"summary": "Working"}, "a"))
        self.assertFalse(self.store.queue_notification(self.task, "progress", {"summary": "Working"}, "b"))
        self.assertTrue(self.store.queue_notification(self.task, "progress", {"summary": "Testing"}, "c"))
        self.assertTrue(self.store.queue_notification(self.task, "progress", {"summary": "Working"}, "d"))
        self.assertEqual(len([row for row in self.rows("outbound") if row["kind"] == "progress"]), 3)

    def test_matching_stop(self):
        self.store.map_draft(self.task, 9)
        result = self.store.ingest(self.update(kind="stop", sender_id=None, draft_id=9))
        self.assertTrue(result["accepted"])
        self.assertEqual([(row["task_id"], row["kind"]) for row in self.rows("events")], [(self.task, "cancel_requested")])
        self.assertEqual(self.store.task(self.task)["status"], "cancel_requested")

    def test_mismatched_stop(self):
        self.store.map_draft(self.task, 9)
        for number, fields in enumerate(({"thread_id": 4}, {"draft_id": 8}, {"chat_id": 42}), 1):
            update = self.update(number, kind="stop", sender_id=None, draft_id=9) | fields
            result = self.store.ingest(update)
            self.assertFalse(result["accepted"])
        self.assertEqual(self.rows("events"), [])
        self.assertEqual(self.store.task(self.task)["status"], "active")

    def test_stored_agent_model_choice(self):
        callback = self.store.issue_agent_option(self.config.alternatives[0])
        self.assertLessEqual(len(callback.encode()), 32)
        result = self.store.ingest(self.update(callback_id=callback, thread_id=4, brief="Alternate"))
        self.assertTrue(result["accepted"])
        task = self.store.task(result["task_id"])
        self.assertEqual((task["agent"], task["model"]), ("codex", "gpt-5.5"))
        self.assertEqual(self.store.ingest(self.update(2, callback_id=callback, thread_id=4))["reason"], "callback_used")

    def test_agent_callback_uses_stored_brief(self):
        callback = self.store.issue_agent_option(self.config.alternatives[0], brief="Build durable storage")
        update = {"update_id": 1, "kind": "callback", "sender_id": 7, "chat_id": 11, "thread_id": None, "message_id": 81, "callback_id": callback}
        result = Store(self.config).ingest(update)
        self.assertTrue(result["accepted"], result)
        task = self.store.task(result["task_id"])
        self.assertEqual((task["brief"], task["agent"], task["model"]), ("Build durable storage", "codex", "gpt-5.5"))
        self.assertEqual(Store(self.config).ingest(update), result)
        self.assertEqual(len(self.rows("tasks")), 2)
        self.assertEqual(self.store.ingest(update | {"update_id": 2})["reason"], "callback_used")

    def test_custom_reply_binding(self):
        self.store.map_message(self.task, 12, self.decision)
        refused = self.store.ingest(self.update(kind="message", reply_to_message_id=12, thread_id=4, text="Custom"))
        self.assertEqual(refused["reason"], "message_mismatch")
        accepted = self.store.ingest(self.update(2, kind="message", reply_to_message_id=12, text="Custom"))
        self.assertTrue(accepted["accepted"])
        self.assertEqual(self.store.answer(self.decision)["text"], "Custom")

    def test_decision_retry_and_binding(self):
        self.assertEqual(self.store.request_decision(self.task, question()), self.decision)
        changed = question() | {"question": "Different?"}
        with self.assertRaisesRegex(ValueError, "request_id"):
            self.store.request_decision(self.task, changed)
        self.assertEqual(len(self.rows("decisions")), 1)

    def test_schema_version_refused(self):
        with sqlite3.connect(self.store.path) as connection:
            connection.execute("PRAGMA user_version = 999")
        with self.assertRaisesRegex(ValueError, "schema version"):
            Store(self.config)

    def test_transaction_exception_rolls_back(self):
        with patch.object(self.store, "_event", side_effect=RuntimeError("before commit")):
            with self.assertRaisesRegex(RuntimeError, "before commit"):
                self.store.ingest(self.update(kind="new_task", brief="Rollback", thread_id=4))
        self.assertEqual(len(self.rows("tasks")), 1)
        self.assertEqual(self.rows("inbound"), [])
        result = self.store.ingest(self.update(kind="new_task", brief="Retry", thread_id=4))
        self.assertTrue(result["accepted"])
        self.assertEqual(len(self.rows("tasks")), 2)

    def test_custom_button_and_cancelled_refusal(self):
        custom = self.store.issue_callback(self.task, self.decision, None)
        self.assertTrue(self.store.ingest(self.update(callback_id=custom))["accepted"])
        self.assertEqual(self.rows("answers"), [])
        self.assertEqual(self.rows("events")[0]["kind"], "custom_answer_requested")
        self.store.cancel_decision(self.decision)
        self.assertEqual(self.store.ingest(self.update(2))["reason"], "cancelled")
        self.assertEqual(self.rows("answers"), [])

    def test_decision_stores_proposal_fingerprint(self):
        self.store.revise_task(self.task, commit="abc", diff_digest="digest")
        request = question() | {"request_id": "commit-proposal"}
        decision = self.store.request_decision(self.task, request)
        row = self.store.decision(decision)
        self.assertEqual((row["revision"], row["commit_id"], row["diff_digest"]), (2, "abc", "digest"))
        callback = self.store.issue_callback(self.task, decision, "sqlite")
        self.assertTrue(self.store.ingest(self.update(callback_id=callback))["accepted"])
        self.assertEqual(self.store.answer(decision)["revision"], 2)
