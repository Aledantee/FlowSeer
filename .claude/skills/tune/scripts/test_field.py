"""Fixture tests for field scoring; no host transcript or database is read."""

from contextlib import redirect_stdout
from datetime import datetime, timedelta, timezone
import io
import json
from pathlib import Path
import sqlite3
import tempfile
import unittest

import field


BASE = datetime(2026, 1, 1, tzinfo=timezone.utc)


def stamp(offset):
    return (BASE + timedelta(seconds=offset)).isoformat().replace("+00:00", "Z")


def write_lines(path, records):
    path.parent.mkdir(parents=True, exist_ok=True)
    path.write_text("".join(json.dumps(record) + "\n" for record in records))


class FieldTest(unittest.TestCase):
    def setUp(self):
        self.temp = tempfile.TemporaryDirectory()
        self.addCleanup(self.temp.cleanup)
        self.root = Path(self.temp.name)
        self.log = self.root / "runs.jsonl"
        self.claude = self.root / "claude"
        self.codex = self.root / "codex"
        self.db = self.root / "opencode.db"
        self.registry = self.root / "registry.yaml"
        self.claude.mkdir()
        self.codex.mkdir()
        self.registry.write_text("models:\n"
                                 "  gpt-6-sol: {pool: codex, price: [2, 10]}\n"
                                 "  claude-sonnet-5: {pool: claude, price: [2, 10]}\n"
                                 "  gemini-3.8-flash: {pool: google, price: [0.75, 3.75], id_format: \"gemini-3.8-flash-<effort>\"}\n"
                                 "  kimi-k3: {pool: synthetic, price: [3, 15], agent: kimi-agent}\n")

    def run_field(self):
        output = io.StringIO()
        with redirect_stdout(output):
            field.main(["--since", "2025-12-31", "--match", "l1", "--runlog", str(self.log),
                        "--claude-projects", str(self.claude), "--codex-sessions", str(self.codex),
                        "--opencode-db", str(self.db), "--registry", str(self.registry)])
        return json.loads(output.getvalue())

    def lane(self, run="r1", cli="codex", model="gpt-6-sol", start=-10, grade=100,
             role="execute", agent=None):
        item = {"v": 1, "event": "start", "run": run, "at": stamp(start), "lane": "l1",
                "cli": cli, "model": model, "role": role, "worktree": "/w/l1",
                "branch": "u/l1", "base": "abc"}
        if agent:
            item["agent"] = agent
        return [item, {"v": 1, "event": "grade", "run": run, "at": stamp(grade),
                       "outcome": "accepted", "verify": "pass"}]

    def test_codex_join_normalizes_cached_input_and_estimates_cost(self):
        write_lines(self.log, self.lane())
        for name, when in (("inside", 10), ("later", 86410)):
            write_lines(self.codex / (name + ".jsonl"), [
                {"type": "session_meta", "timestamp": stamp(when),
                 "payload": {"id": name, "cwd": "/w/l1", "timestamp": stamp(when)}},
                {"type": "response_item", "timestamp": stamp(when + 1),
                 "payload": {"type": "message", "role": "user",
                             "internal_chat_message_metadata_passthrough": {
                                 "content_item_kinds": ["agents_md.instructions"]}}},
                {"type": "response_item", "timestamp": stamp(when + 5),
                 "payload": {"type": "message", "role": "user",
                             "internal_chat_message_metadata_passthrough": {"content_item_kinds": ["user.text"]}}},
                {"type": "response_item", "timestamp": stamp(when + 20),
                 "payload": {"type": "message", "role": "assistant"}},
                {"type": "event_msg", "timestamp": stamp(when + 20),
                 "payload": {"type": "token_count", "info": {"total_token_usage": {
                     "input_tokens": 1500, "cached_input_tokens": 500,
                     "output_tokens": 200, "reasoning_output_tokens": 50}}}}])
        report = self.run_field()
        self.assertEqual(1, len(report["runs"]))
        run = report["runs"][0]
        self.assertEqual(1000, run["tokens"]["input"])
        self.assertEqual(500, run["tokens"]["cache_read"])
        self.assertEqual(50, run["tokens"]["reasoning"])
        self.assertEqual(15, run["active_s"])
        self.assertAlmostEqual(0.0041, run["cost_usd"])
        self.assertTrue(run["est"])

    def test_codex_activity_joins_when_terminal_opens_before_log_start(self):
        write_lines(self.log, self.lane(start=0, grade=100))
        write_lines(self.codex / "early.jsonl", [
            {"type": "session_meta", "timestamp": stamp(-2),
             "payload": {"id": "early", "cwd": "/w/l1", "timestamp": stamp(-2)}},
            {"type": "response_item", "timestamp": stamp(5),
             "payload": {"type": "message", "role": "user",
                         "internal_chat_message_metadata_passthrough": {
                             "content_item_kinds": ["user.text"]}}},
            {"type": "response_item", "timestamp": stamp(20),
             "payload": {"type": "message", "role": "assistant"}},
            {"type": "event_msg", "timestamp": stamp(20),
             "payload": {"type": "token_count", "info": {"total_token_usage": {
                 "input_tokens": 100, "output_tokens": 10}}}}])
        run = self.run_field()["runs"][0]
        self.assertEqual(15, run["active_s"])
        self.assertEqual(100, run["tokens"]["input"])

    def test_native_model_and_review_role_come_from_transcript_and_event(self):
        write_lines(self.log, [{"v": 1, "event": "review", "run": "review-1", "at": stamp(90),
                               "model": "claude-sonnet-5", "role": "review-unit", "agent": "a1",
                               "findings": 4, "held": 3, "unverified": 1}])
        path = self.claude / "-w-l1" / "session" / "subagents" / "agent-a1.jsonl"
        write_lines(path, [
            {"type": "user", "timestamp": stamp(0), "cwd": "/w/l1", "agentId": "a1",
             "message": {"role": "user", "content": "review"}},
            {"type": "assistant", "timestamp": stamp(12), "cwd": "/w/l1", "agentId": "a1",
             "message": {"id": "msg1", "model": "claude-sonnet-5", "usage": {"input_tokens": 100,
                         "output_tokens": 20}}}])
        path.with_suffix(".meta.json").write_text(json.dumps({"agentType": "independent-reviewer",
                                                                "model": "sonnet"}))
        run = self.run_field()["runs"][0]
        self.assertEqual(("native", "claude-sonnet-5", "review-unit"),
                         (run["source"], run["model"], run["role"]))
        self.assertEqual((4, 3), (run["findings"], run["held"]))

    def test_group_marks_three_runs_small_and_five_sufficient(self):
        events = []
        for index in range(3):
            events.extend(self.lane(run="r" + str(index), cli="agy", model="gemini-3.8-flash-high"))
        write_lines(self.log, events)
        group = self.run_field()["groups"][0]
        self.assertEqual(3, group["counts"]["accepted"])
        self.assertEqual("small", group["sample"])
        for index in range(3, 5):
            events.extend(self.lane(run="r" + str(index), cli="agy", model="gemini-3.8-flash-high"))
        write_lines(self.log, events)
        group = self.run_field()["groups"][0]
        self.assertEqual(5, group["graded"])
        self.assertNotIn("sample", group)

    def test_agy_maps_registry_model_and_keeps_elapsed_time(self):
        write_lines(self.log, self.lane(cli="agy", model="gemini-3.8-flash-high", start=0, grade=42))
        run = self.run_field()["runs"][0]
        self.assertEqual("gemini-3.8-flash", run["model"])
        self.assertEqual("unsupported", run["transcript"])
        self.assertEqual(42, run["elapsed_s"])

    def test_claude_tool_result_stays_inside_turn_and_orca_session_is_unique(self):
        write_lines(self.log, self.lane(cli="claude", model="claude-sonnet-5"))
        path = self.claude / "-w-l1" / "session.jsonl"
        write_lines(path, [
            {"type": "user", "timestamp": stamp(0), "cwd": "/w/l1", "entrypoint": "cli",
             "message": {"role": "user", "content": "work"}},
            {"type": "assistant", "timestamp": stamp(10), "cwd": "/w/l1", "entrypoint": "cli",
             "message": {"id": "tool", "model": "claude-sonnet-5", "content": [{"type": "tool_use"}]}},
            {"type": "user", "timestamp": stamp(70), "cwd": "/w/l1", "entrypoint": "cli",
             "message": {"role": "user", "content": [{"type": "tool_result", "is_error": True}]}},
            {"type": "assistant", "timestamp": stamp(80), "cwd": "/w/l1", "entrypoint": "cli",
             "message": {"id": "final", "model": "claude-sonnet-5", "usage": {
                 "input_tokens": 1000, "cache_read_input_tokens": 2000,
                 "cache_creation": {"ephemeral_5m_input_tokens": 3000,
                                    "ephemeral_1h_input_tokens": 4000}, "output_tokens": 500}}}])
        report = self.run_field()
        self.assertEqual(1, len(report["runs"]))
        run = report["runs"][0]
        self.assertEqual("orca", run["source"])
        self.assertEqual(80, run["active_s"])
        self.assertEqual(1, run["tool_errors"])
        self.assertEqual((3000, 4000), (run["tokens"]["cache_write_5m"], run["tokens"]["cache_write_1h"]))
        self.assertAlmostEqual(0.0309, run["cost_usd"])

    def test_claude_directory_replaces_every_non_alphanumeric_character(self):
        self.assertEqual("-Users-aledante-orca-workspaces-FlowSeer-snipefish",
                         field.claude_directory("/Users/aledante/orca/workspaces/FlowSeer/snipefish"))
        self.assertEqual("-w-lane-one", field.claude_directory("/w/lane_one"))

    def test_opencode_uses_recorded_message_cost_and_agent_model(self):
        write_lines(self.log, self.lane(cli="opencode", model="", agent="kimi-agent"))
        with sqlite3.connect(self.db) as db:
            db.execute("CREATE TABLE session (id TEXT, directory TEXT, time_created INTEGER, agent TEXT, model TEXT)")
            db.execute("CREATE TABLE message (session_id TEXT, time_created INTEGER, time_updated INTEGER, data TEXT)")
            created = int((BASE + timedelta(seconds=5)).timestamp() * 1000)
            db.execute("INSERT INTO session VALUES (?, ?, ?, ?, ?)", ("oc1", "/w/l1", created, "kimi-agent", None))
            db.execute("INSERT INTO message VALUES (?, ?, ?, ?)", ("oc1", created, created,
                       json.dumps({"role": "user"})))
            db.execute("INSERT INTO message VALUES (?, ?, ?, ?)", ("oc1", created + 10000, created + 10000,
                       json.dumps({"role": "assistant", "cost": 0.123, "tokens": {
                           "input": 10, "output": 20, "reasoning": 5, "cache": {"read": 3, "write": 2}}})))
            db.execute("INSERT INTO message VALUES (?, ?, ?, ?)", ("oc1", created + 50000, created + 50000,
                       json.dumps({"role": "user"})))
            db.execute("INSERT INTO message VALUES (?, ?, ?, ?)", ("oc1", created + 60000, created + 60000,
                       json.dumps({"role": "assistant", "cost": 0})))
        run = self.run_field()["runs"][0]
        self.assertEqual("kimi-k3", run["model"])
        self.assertEqual(0.123, run["cost_usd"])
        self.assertFalse(run["est"])
        self.assertEqual(20, run["active_s"])


if __name__ == "__main__":
    unittest.main()
