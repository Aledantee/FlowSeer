import dataclasses
import json
import tempfile
import unittest
from pathlib import Path

from harness import render, telegram
from harness.config import load
from harness.state import Store
from tests.harness import configuration, question
from tests.harness.test_telegram import fixture


def buttons(card):
    return [button for block in card['blocks'] if block['type'] == 'buttons' for button in block['buttons']]


class RenderTest(unittest.TestCase):
    def store(self):
        directory = tempfile.TemporaryDirectory()
        self.addCleanup(directory.cleanup)
        return Store(load(configuration(Path(directory.name))))

    def test_worker_text_is_plain_richtext(self):
        text = '<b>worker</b> [link](tg://user?id=7) & "quote"'
        card = render.activity(render.ActivitySummary((text,)))
        self.assertEqual(card, {'blocks': [{'type': 'details', 'summary': 'Activity', 'blocks': [{'type': 'paragraph', 'text': text}]}], 'skip_entity_detection': True})
        with self.assertRaisesRegex(TypeError, 'plain text'):
            render.activity(render.ActivitySummary(({'type': 'url', 'url': 'https://example.invalid'},)))

    def test_summary_dataclasses_cannot_carry_diffs(self):
        names = []
        for name, value in vars(render).items():
            if isinstance(value, type) and dataclasses.is_dataclass(value):
                names.append(name)
                self.assertFalse(any('diff' in field.name or 'patch' in field.name for field in dataclasses.fields(value)), name)
        self.assertTrue({'TaskSummary', 'ActivitySummary', 'DecisionSummary', 'NewTaskSummary', 'OverviewSummary'} <= set(names))

    def test_overview_groups_and_uses_table_cells(self):
        summaries = tuple(render.TaskSummary(str(n), 'Storage', state, 'Working') for n, state in enumerate(('active', 'awaiting decision', 'blocked', 'finished')))
        card = render.overview(render.OverviewSummary(summaries))
        self.assertEqual([block['text'] for block in card['blocks'] if block['type'] == 'heading'], ['Active', 'Awaiting decision', 'Blocked', 'Finished'])
        tables = [block for block in card['blocks'] if block['type'] == 'table']
        self.assertEqual(len(tables), 4)
        self.assertEqual(tables[0]['cells'][1], [{'text': '0', 'align': 'left', 'valign': 'top'}, {'text': 'Storage', 'align': 'left', 'valign': 'top'}, {'text': 'Working', 'align': 'left', 'valign': 'top'}])
        self.assertIn('Send a brief', json.dumps(card))

    def test_new_task_default_and_every_alternative_create_stored_pair(self):
        store = self.store()
        summary = render.new_task_summary(store, 'Build storage')
        card = render.new_task(summary)
        self.assertIn('claude / default-model', json.dumps(card))
        self.assertEqual([b['text'] for b in buttons(card)], ['claude / default-model (default)', 'codex / gpt-5.5'])
        for index, pair in enumerate((store.config.default, *store.config.alternatives)):
            button = buttons(render.new_task(render.new_task_summary(store, 'Build storage')))[index]
            self.assertLessEqual(len(button['callback_data'].encode()), 32)
            raw = fixture('callback')
            raw['update_id'] = 200 + index
            raw['callback_query']['message'].pop('message_thread_id')
            raw['callback_query']['data'] = button['callback_data']
            update = telegram.normalize(raw).as_store_update()
            result = Store(store.config).ingest(update)
            self.assertTrue(result['accepted'], result)
            task = store.task(result['task_id'])
            self.assertEqual((task['brief'], task['agent'], task['model']), ('Build storage', pair.agent, pair.model))
            self.assertEqual(store.ingest(update), result)
        prompt = render.new_task(render.NewTaskSummary('Send a brief', summary.default, summary.alternatives))
        self.assertIn('Send a brief', json.dumps(prompt))

    def test_overview_refuses_unknown_state(self):
        summary = render.OverviewSummary((
            render.TaskSummary('first', 'Storage', 'active', 'Working'),
            render.TaskSummary('second', 'Network', 'cancel_requested', 'Waiting'),
        ))
        with self.assertRaisesRegex(ValueError, 'cancel_requested'):
            render.overview(summary)

    def test_new_task_card_consumes_sibling_buttons(self):
        store = self.store()
        summary = render.new_task_summary(store, 'Build storage')
        update = {'update_id': 1, 'kind': 'callback', 'sender_id': 7, 'chat_id': 11, 'thread_id': None, 'callback_id': summary.default.callback_id}
        first = Store(store.config).ingest(update)
        self.assertTrue(first['accepted'])
        sibling = update | {'update_id': 2, 'callback_id': summary.alternatives[0].callback_id}
        self.assertEqual(Store(store.config).ingest(sibling)['reason'], 'callback_used')
        self.assertEqual(Store(store.config).ingest(update), first)
        with store._connect() as connection:
            self.assertEqual(connection.execute('SELECT count(*) FROM tasks').fetchone()[0], 1)
            self.assertEqual(connection.execute('SELECT count(*) FROM events').fetchone()[0], 1)
            self.assertEqual(connection.execute('SELECT count(*) FROM inbound').fetchone()[0], 2)

    def test_decision_controls_bind_to_store_and_custom_reply(self):
        store = self.store()
        task = store.create_task('Storage', thread_id=3)
        decision = store.request_decision(task, question())
        card = render.decision(render.decision_summary(store, decision))
        self.assertEqual([b['text'] for b in buttons(card)], ['SQLite (recommended)', 'Files', 'Custom Answer'])
        self.assertIn('One host', json.dumps(card))
        self.assertIn('Reply to this decision', json.dumps(card))
        self.assertIn(decision, json.dumps(card))
        self.assertIn('Revision 1', json.dumps(card))
        custom = buttons(card)[-1]['callback_data']
        raw = fixture('callback')
        raw['callback_query']['data'] = custom
        result = store.ingest(telegram.normalize(raw).as_store_update())
        self.assertTrue(result['accepted'])
        self.assertIsNone(store.answer(decision))
        store.map_message(task, 12, decision)
        self.assertTrue(store.ingest(telegram.normalize(fixture('reply')).as_store_update())['accepted'])
        self.assertEqual(store.answer(decision)['text'], 'Use files instead')

    def test_status_controls_and_approval_summary(self):
        store = self.store()
        task = store.create_task('Storage', thread_id=3)
        payload = question() | {'options': [{'id': name.lower(), 'label': name, 'tradeoff': 'Coordinator acknowledgement required'} for name in ('Steer', 'Pause', 'Resume', 'Cancel')], 'recommendation': 'steer'}
        decision = store.request_decision(task, payload)
        controls = tuple(render.ButtonSummary(option['label'], store.issue_callback(task, decision, option['id'])) for option in payload['options'])
        card = render.status(render.TaskSummary('task', 'Storage', 'cancel_requested', 'Waiting for acknowledgement', controls, 'tools/scripts/harness', '38 tests passed', 'accept', 'Client check pending', 'commit'))
        self.assertEqual([b['text'] for b in buttons(card)], ['Steer', 'Pause', 'Resume', 'Cancel'])
        content = json.dumps(card)
        for text in ('cancel_requested', 'Waiting for acknowledgement', 'tools/scripts/harness', '38 tests passed', 'accept', 'Client check pending', 'commit'):
            self.assertIn(text, content)
        with self.assertRaisesRegex(ValueError, 'callback'):
            render.status(dataclasses.replace(render.TaskSummary('t', 'T', 'active', 'Work'), controls=(render.ButtonSummary('Cancel', 'x' * 33),)))

    def test_callback_limit_counts_utf8_bytes(self):
        summary = render.TaskSummary('t', 'Storage', 'active', 'Working')
        card = render.status(dataclasses.replace(summary, controls=(render.ButtonSummary('Cancel', '界' * 10),)))
        self.assertEqual(buttons(card)[0]['callback_data'], '界' * 10)
        with self.assertRaisesRegex(ValueError, 'callback'):
            render.status(dataclasses.replace(summary, controls=(render.ButtonSummary('Cancel', '界' * 11),)))

    def test_cards_bound_worker_output_without_cutting_callbacks(self):
        text = '🦊' * 100000
        tasks = tuple(render.TaskSummary(str(n), text, state, text) for state in ('active', 'awaiting decision', 'blocked', 'finished') for n in range(100))
        cards = [render.overview(render.OverviewSummary(tasks)), render.activity(render.ActivitySummary((text,) * 100)), render.status(render.TaskSummary('t', text, 'active', text, scope=text, verification=text, review=text, risks=text, operation=text)), render.decision(render.DecisionSummary('d', 1, text, tuple(render.OptionSummary(text, text, 'c' * 32, n == 0) for n in range(20)), 'z' * 32))]
        for card in cards:
            self.assertLessEqual(len(json.dumps(card, ensure_ascii=False).encode()), 24576)
            self.assertIn('…', json.dumps(card, ensure_ascii=False))
        with self.assertRaisesRegex(ValueError, 'card exceeds'):
            render.new_task(render.NewTaskSummary('Brief', render.AgentChoice('default', 'm', 'c' * 32), tuple(render.AgentChoice('a', 'm', 'c' * 32) for _ in range(1000))))
