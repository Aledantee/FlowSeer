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

    def run_field(self, since="2025-12-31"):
        output = io.StringIO()
        with redirect_stdout(output):
            field.main(["--since", since, "--match", "l1", "--runlog", str(self.log),
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

    def test_codex_spanning_session_across_nonoverlapping_lanes_has_no_duplicate_attribution(self):
        events = []
        events.extend(self.lane(run="r1", cli="codex", model="gpt-6-sol", start=0, grade=100))
        events.extend(self.lane(run="r2", cli="codex", model="gpt-6-sol", start=200, grade=300))
        write_lines(self.log, events)
        write_lines(self.codex / "spanning.jsonl", [
            {"type": "session_meta", "timestamp": stamp(-2),
             "payload": {"id": "spanning", "cwd": "/w/l1", "timestamp": stamp(-2)}},
            {"type": "response_item", "timestamp": stamp(10),
             "payload": {"type": "message", "role": "user",
                         "internal_chat_message_metadata_passthrough": {"content_item_kinds": ["user.text"]}}},
            {"type": "response_item", "timestamp": stamp(50),
             "payload": {"type": "message", "role": "assistant"}},
            {"type": "event_msg", "timestamp": stamp(50),
             "payload": {"type": "token_count", "info": {"total_token_usage": {
                 "input_tokens": 1100, "cached_input_tokens": 100,
                 "output_tokens": 100, "reasoning_output_tokens": 0}}}},
            {"type": "response_item", "timestamp": stamp(210),
             "payload": {"type": "message", "role": "user",
                         "internal_chat_message_metadata_passthrough": {"content_item_kinds": ["user.text"]}}},
            {"type": "response_item", "timestamp": stamp(270),
             "payload": {"type": "message", "role": "assistant"}},
            {"type": "event_msg", "timestamp": stamp(270),
             "payload": {"type": "token_count", "info": {"total_token_usage": {
                 "input_tokens": 2200, "cached_input_tokens": 200,
                 "output_tokens": 200, "reasoning_output_tokens": 0}}}}])
        report = self.run_field()
        self.assertEqual(2, len(report["runs"]))
        r1, r2 = report["runs"]
        self.assertEqual("ambiguous", r1["transcript"])
        self.assertEqual("ambiguous", r2["transcript"])
        self.assertIsNone(r1["active_s"])
        self.assertIsNone(r2["active_s"])
        self.assertIsNone(r1["tokens"])
        self.assertIsNone(r2["tokens"])
        self.assertIsNone(r1["cost_usd"])
        self.assertIsNone(r2["cost_usd"])
        ambiguous = [u for u in report["unmatched"] if u.get("reason") == "transcript_ambiguous"]
        self.assertEqual(2, len(ambiguous))

    def test_claude_spanning_session_across_nonoverlapping_lanes_has_no_duplicate_attribution(self):
        events = []
        events.extend(self.lane(run="r1", cli="claude", model="claude-sonnet-5", start=0, grade=100))
        events.extend(self.lane(run="r2", cli="claude", model="claude-sonnet-5", start=200, grade=300))
        write_lines(self.log, events)
        write_lines(self.claude / "-w-l1" / "session.jsonl", [
            {"type": "user", "timestamp": stamp(10), "cwd": "/w/l1", "entrypoint": "cli",
             "message": {"role": "user", "content": "task 1"}},
            {"type": "assistant", "timestamp": stamp(50), "cwd": "/w/l1", "entrypoint": "cli",
             "message": {"id": "m1", "model": "claude-sonnet-5", "usage": {"input_tokens": 1000, "output_tokens": 100}}},
            {"type": "user", "timestamp": stamp(210), "cwd": "/w/l1", "entrypoint": "cli",
             "message": {"role": "user", "content": "task 2"}},
            {"type": "assistant", "timestamp": stamp(270), "cwd": "/w/l1", "entrypoint": "cli",
             "message": {"id": "m2", "model": "claude-sonnet-5", "usage": {"input_tokens": 1000, "output_tokens": 100}}}])
        report = self.run_field()
        self.assertEqual(2, len(report["runs"]))
        r1, r2 = report["runs"]
        self.assertEqual("ambiguous", r1["transcript"])
        self.assertEqual("ambiguous", r2["transcript"])
        self.assertIsNone(r1["active_s"])
        self.assertIsNone(r2["active_s"])
        self.assertIsNone(r1["tokens"])
        self.assertIsNone(r2["tokens"])
        self.assertIsNone(r1["cost_usd"])
        self.assertIsNone(r2["cost_usd"])
        ambiguous = [u for u in report["unmatched"] if u.get("reason") == "transcript_ambiguous"]
        self.assertEqual(2, len(ambiguous))

    def test_distinct_sessions_on_reused_path_attribute_to_respective_lanes(self):
        events = []
        events.extend(self.lane(run="r1", cli="codex", model="gpt-6-sol", start=0, grade=100))
        events.extend(self.lane(run="r2", cli="codex", model="gpt-6-sol", start=200, grade=300))
        write_lines(self.log, events)
        write_lines(self.codex / "s1.jsonl", [
            {"type": "session_meta", "timestamp": stamp(10),
             "payload": {"id": "s1", "cwd": "/w/l1", "timestamp": stamp(10)}},
            {"type": "response_item", "timestamp": stamp(10),
             "payload": {"type": "message", "role": "user",
                         "internal_chat_message_metadata_passthrough": {"content_item_kinds": ["user.text"]}}},
            {"type": "response_item", "timestamp": stamp(50),
             "payload": {"type": "message", "role": "assistant"}},
            {"type": "event_msg", "timestamp": stamp(50),
             "payload": {"type": "token_count", "info": {"total_token_usage": {
                 "input_tokens": 1000, "output_tokens": 100}}}}])
        write_lines(self.codex / "s2.jsonl", [
            {"type": "session_meta", "timestamp": stamp(210),
             "payload": {"id": "s2", "cwd": "/w/l1", "timestamp": stamp(210)}},
            {"type": "response_item", "timestamp": stamp(210),
             "payload": {"type": "message", "role": "user",
                         "internal_chat_message_metadata_passthrough": {"content_item_kinds": ["user.text"]}}},
            {"type": "response_item", "timestamp": stamp(270),
             "payload": {"type": "message", "role": "assistant"}},
            {"type": "event_msg", "timestamp": stamp(270),
             "payload": {"type": "token_count", "info": {"total_token_usage": {
                 "input_tokens": 1000, "output_tokens": 100}}}}])
        report = self.run_field()
        self.assertEqual(2, len(report["runs"]))
        r1, r2 = report["runs"]
        self.assertNotIn("transcript", r1)
        self.assertNotIn("transcript", r2)
        self.assertEqual(40, r1["active_s"])
        self.assertEqual(60, r2["active_s"])
        self.assertEqual(1100, r1["tokens"]["total"])
        self.assertEqual(1100, r2["tokens"]["total"])
        ambiguous = [u for u in report["unmatched"] if u.get("reason") == "transcript_ambiguous"]
        self.assertEqual(0, len(ambiguous))

    def test_missing_transcripts_exclude_unobserved_tokens_from_group_medians(self):
        events = []
        for i in range(5):
            events.extend(self.lane(run="r" + str(i), cli="codex", model="gpt-6-sol",
                                    start=i * 100, grade=i * 100 + 50))
        write_lines(self.log, events)
        for i in (0, 1):
            when = i * 100
            write_lines(self.codex / ("session_" + str(i) + ".jsonl"), [
                {"type": "session_meta", "timestamp": stamp(when),
                 "payload": {"id": "s" + str(i), "cwd": "/w/l1", "timestamp": stamp(when)}},
                {"type": "response_item", "timestamp": stamp(when + 5),
                 "payload": {"type": "message", "role": "user",
                             "internal_chat_message_metadata_passthrough": {"content_item_kinds": ["user.text"]}}},
                {"type": "response_item", "timestamp": stamp(when + 20),
                 "payload": {"type": "message", "role": "assistant"}},
                {"type": "event_msg", "timestamp": stamp(when + 20),
                 "payload": {"type": "token_count", "info": {"total_token_usage": {
                     "input_tokens": 1000, "output_tokens": 100}}}}])
        report = self.run_field()
        self.assertEqual(5, len(report["runs"]))
        for i in (0, 1):
            self.assertEqual(1100, report["runs"][i]["tokens"]["total"])
            self.assertEqual(15, report["runs"][i]["active_s"])
            self.assertIsNotNone(report["runs"][i]["cost_usd"])
        for i in (2, 3, 4):
            self.assertIsNone(report["runs"][i]["tokens"])
            self.assertIsNone(report["runs"][i]["active_s"])
            self.assertIsNone(report["runs"][i]["cost_usd"])
        missing = [u for u in report["unmatched"] if u.get("reason") == "transcript_missing"]
        self.assertEqual(3, len(missing))
        group = report["groups"][0]
        self.assertEqual(5, group["graded"])
        self.assertEqual(1100, group["medians"]["tokens"])
        self.assertEqual(15, group["medians"]["active_s"])
        self.assertIsNotNone(group["medians"]["cost_usd"])

    def test_opencode_spanning_session_across_nonoverlapping_lanes_has_no_duplicate_attribution(self):
        events = []
        events.extend(self.lane(run="r1", cli="opencode", model="", agent="kimi-agent", start=0, grade=100))
        events.extend(self.lane(run="r2", cli="opencode", model="", agent="kimi-agent", start=200, grade=300))
        write_lines(self.log, events)
        with sqlite3.connect(self.db) as db:
            db.execute("CREATE TABLE session (id TEXT, directory TEXT, time_created INTEGER, agent TEXT, model TEXT)")
            db.execute("CREATE TABLE message (session_id TEXT, time_created INTEGER, time_updated INTEGER, data TEXT)")
            created = int((BASE + timedelta(seconds=-2)).timestamp() * 1000)
            db.execute("INSERT INTO session VALUES (?, ?, ?, ?, ?)", ("oc1", "/w/l1", created, "kimi-agent", None))
            # Activity in Lane 1 [0, 100]
            t1 = int((BASE + timedelta(seconds=10)).timestamp() * 1000)
            t2 = int((BASE + timedelta(seconds=50)).timestamp() * 1000)
            db.execute("INSERT INTO message VALUES (?, ?, ?, ?)", ("oc1", t1, t1, json.dumps({"role": "user"})))
            db.execute("INSERT INTO message VALUES (?, ?, ?, ?)", ("oc1", t2, t2, json.dumps({
                "role": "assistant", "cost": 0.05, "tokens": {"input": 100, "output": 20}})))
            # Activity in Lane 2 [200, 300]
            t3 = int((BASE + timedelta(seconds=210)).timestamp() * 1000)
            t4 = int((BASE + timedelta(seconds=270)).timestamp() * 1000)
            db.execute("INSERT INTO message VALUES (?, ?, ?, ?)", ("oc1", t3, t3, json.dumps({"role": "user"})))
            db.execute("INSERT INTO message VALUES (?, ?, ?, ?)", ("oc1", t4, t4, json.dumps({
                "role": "assistant", "cost": 0.07, "tokens": {"input": 200, "output": 40}})))
        report = self.run_field()
        self.assertEqual(2, len(report["runs"]))
        r1, r2 = report["runs"]
        self.assertEqual("ambiguous", r1["transcript"])
        self.assertEqual("ambiguous", r2["transcript"])
        self.assertIsNone(r1["active_s"])
        self.assertIsNone(r2["active_s"])
        self.assertIsNone(r1["tokens"])
        self.assertIsNone(r2["tokens"])
        self.assertIsNone(r1["cost_usd"])
        self.assertIsNone(r2["cost_usd"])
        ambiguous = [u for u in report["unmatched"] if u.get("reason") == "transcript_ambiguous"]
        self.assertEqual(2, len(ambiguous))

    def test_overlapping_lanes_on_same_path_mark_runs_ambiguous(self):
        events = []
        events.extend(self.lane(run="r1", cli="codex", model="gpt-6-sol", start=0, grade=100))
        events.extend(self.lane(run="r2", cli="codex", model="gpt-6-sol", start=50, grade=150))
        write_lines(self.log, events)
        write_lines(self.codex / "session.jsonl", [
            {"type": "session_meta", "timestamp": stamp(10),
             "payload": {"id": "s1", "cwd": "/w/l1", "timestamp": stamp(10)}},
            {"type": "response_item", "timestamp": stamp(20),
             "payload": {"type": "message", "role": "user",
                         "internal_chat_message_metadata_passthrough": {"content_item_kinds": ["user.text"]}}},
            {"type": "response_item", "timestamp": stamp(60),
             "payload": {"type": "message", "role": "assistant"}}])
        report = self.run_field()
        self.assertEqual(2, len(report["runs"]))
        for run in report["runs"]:
            self.assertEqual("ambiguous", run["transcript"])
            self.assertIsNone(run["tokens"])
            self.assertIsNone(run["active_s"])
        reasons = [u["reason"] for u in report["unmatched"] if u.get("reason") == "transcript_ambiguous"]
        self.assertEqual(2, len(reasons))

    def test_ended_ungraded_lane_cannot_claim_or_change_valid_lane(self):
        cases = (("same_second", 0, 0, 0, "2025-12-31"),
                 ("adjacent_second", 0, 1, 1, "2025-12-31"),
                 ("before_since", 86399, 86400, 86400, "2026-01-02"))
        for name, aborted_start, aborted_end, valid_start, since in cases:
            with self.subTest(name=name):
                valid = self.lane(run="valid", start=valid_start, grade=valid_start + 100)
                write_lines(self.codex / "session.jsonl", [
                    {"type": "session_meta", "timestamp": stamp(valid_start),
                     "payload": {"id": "session", "cwd": "/w/l1", "timestamp": stamp(valid_start)}},
                    {"type": "response_item", "timestamp": stamp(valid_start),
                     "payload": {"type": "message", "role": "user",
                                 "internal_chat_message_metadata_passthrough": {"content_item_kinds": ["user.text"]}}},
                    {"type": "response_item", "timestamp": stamp(valid_start + 20),
                     "payload": {"type": "message", "role": "assistant"}},
                    {"type": "event_msg", "timestamp": stamp(valid_start + 20),
                     "payload": {"type": "token_count", "info": {"total_token_usage": {
                         "input_tokens": 1000, "output_tokens": 100}}}}])
                write_lines(self.log, valid)
                expected = self.run_field(since=since)
                self.assertEqual(20, expected["runs"][0]["active_s"])
                self.assertEqual(1100, expected["runs"][0]["tokens"]["total"])
                aborted = self.lane(run="aborted", start=aborted_start)[0]
                write_lines(self.log, valid + [aborted, {"v": 1, "event": "end", "run": "aborted",
                                                         "at": stamp(aborted_end)}])
                self.assertEqual(expected, self.run_field(since=since))

    def test_ungraded_live_lane_remains_in_field_rows(self):
        write_lines(self.log, self.lane(run="live", cli="agy", model="gemini-3.8-flash-high")[0:1])
        report = self.run_field()
        self.assertEqual(["live"], [run["run"] for run in report["runs"]])
        self.assertIsNone(report["runs"][0]["outcome"])

    def test_session_spanning_prior_lane_before_since_marks_current_lane_ambiguous(self):
        events = []
        events.extend(self.lane(run="r_prior", cli="codex", model="gpt-6-sol", start=0, grade=100))
        events.extend(self.lane(run="r_current", cli="codex", model="gpt-6-sol", start=86400, grade=86500))
        write_lines(self.log, events)
        write_lines(self.codex / "spanning.jsonl", [
            {"type": "session_meta", "timestamp": stamp(-2),
             "payload": {"id": "spanning", "cwd": "/w/l1", "timestamp": stamp(-2)}},
            {"type": "response_item", "timestamp": stamp(10),
             "payload": {"type": "message", "role": "user",
                         "internal_chat_message_metadata_passthrough": {"content_item_kinds": ["user.text"]}}},
            {"type": "response_item", "timestamp": stamp(50),
             "payload": {"type": "message", "role": "assistant"}},
            {"type": "event_msg", "timestamp": stamp(50),
             "payload": {"type": "token_count", "info": {"total_token_usage": {
                 "input_tokens": 1000, "output_tokens": 100}}}},
            {"type": "response_item", "timestamp": stamp(86410),
             "payload": {"type": "message", "role": "user",
                         "internal_chat_message_metadata_passthrough": {"content_item_kinds": ["user.text"]}}},
            {"type": "response_item", "timestamp": stamp(86450),
             "payload": {"type": "message", "role": "assistant"}},
            {"type": "event_msg", "timestamp": stamp(86450),
             "payload": {"type": "token_count", "info": {"total_token_usage": {
                 "input_tokens": 2000, "output_tokens": 200}}}}])
        report = self.run_field(since="2026-01-02")
        self.assertEqual(1, len(report["runs"]))
        run = report["runs"][0]
        self.assertEqual("r_current", run["run"])
        self.assertEqual("ambiguous", run["transcript"])
        self.assertIsNone(run["active_s"])
        self.assertIsNone(run["tokens"])
        self.assertIsNone(run["cost_usd"])
        ambiguous = [u for u in report["unmatched"] if u.get("reason") == "transcript_ambiguous"]
        self.assertEqual([{"run": "r_current", "reason": "transcript_ambiguous"}], ambiguous)


if __name__ == "__main__":
    unittest.main()
