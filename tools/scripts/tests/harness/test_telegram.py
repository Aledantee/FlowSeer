import io
import json
import logging
import sqlite3
import tempfile
import traceback
import unittest
from pathlib import Path
from urllib.error import HTTPError, URLError

from harness import telegram
from harness.config import load
from harness.state import Store
from tests.harness import configuration, question


FIXTURES = Path(__file__).parent / 'testdata' / 'telegram'
TOKEN = 'synthetic-credential-sentinel'


def fixture(name):
    return json.loads((FIXTURES / f'{name}.json').read_text())


class Opener:
    def __init__(self, *responses):
        self.responses = list(responses)
        self.calls = []

    def __call__(self, request, *, timeout):
        self.calls.append((request.full_url.rsplit('/', 1)[-1], json.loads(request.data), timeout))
        response = self.responses.pop(0)
        if isinstance(response, Exception):
            raise response
        return io.BytesIO(json.dumps(response).encode())


class TelegramTest(unittest.TestCase):
    def store(self):
        directory = tempfile.TemporaryDirectory()
        self.addCleanup(directory.cleanup)
        return Store(load(configuration(Path(directory.name))))

    def test_normalizes_fixture_identities(self):
        expected = {
            'private': ('message', 11, None, 7, 81, None, None),
            'group': ('message', -21, None, 42, 82, None, None),
            'topic': ('message', 11, 3, 7, 83, None, None),
            'reply': ('message', 11, 3, 7, 84, 12, None),
            'callback': ('callback', 11, 3, 7, 12, None, None),
            'stop': ('stop', 11, 3, None, None, None, 9),
            'other': ('other', None, None, None, None, None, None),
        }
        for name, want in expected.items():
            with self.subTest(name=name):
                raw = fixture(name)
                record = telegram.normalize(raw)
                self.assertEqual((record.kind, record.chat_id, record.thread_id, record.sender_id, record.message_id, record.reply_to_message_id, record.draft_id), want)
                self.assertEqual(record.update_id, raw['update_id'])
                self.assertEqual(record.as_store_update()['raw'], raw)
        callback = telegram.normalize(fixture('callback'))
        self.assertEqual((callback.callback_id, callback.callback_query_id), ('stored-option', 'query-1'))
        self.assertEqual(telegram.normalize(fixture('reply')).text, 'Use files instead')

    def test_normalized_records_are_ingested_without_authorizing(self):
        store = self.store()
        task = store.create_task('Storage', thread_id=3)
        decision = store.request_decision(task, question())
        store.map_message(task, 12, decision)
        store.map_draft(task, 9)
        callback = fixture('callback')
        callback['callback_query']['data'] = store.issue_callback(task, decision, None)
        self.assertTrue(store.ingest(telegram.normalize(callback).as_store_update())['accepted'])
        self.assertTrue(store.ingest(telegram.normalize(fixture('reply')).as_store_update())['accepted'])
        self.assertEqual(store.answer(decision)['text'], 'Use files instead')
        self.assertTrue(store.ingest(telegram.normalize(fixture('stop')).as_store_update())['accepted'])
        self.assertEqual(store.task(task)['status'], 'cancel_requested')
        group = telegram.normalize(fixture('group'))
        self.assertEqual(group.sender_id, 42)
        self.assertEqual(store.ingest(group.as_store_update())['reason'], 'unauthorized')
        self.assertEqual(store.ingest(telegram.normalize(fixture('other')).as_store_update())['reason'], 'unauthorized')
        with sqlite3.connect(store.path) as connection:
            self.assertEqual(connection.execute('SELECT count(*) FROM inbound').fetchone()[0], 5)

    def test_methods_send_documented_fields(self):
        opener = Opener(*[{'ok': True, 'result': {'value': n}} for n in range(7)])
        client = telegram.Client(TOKEN, opener=opener)
        rich = {'blocks': [{'type': 'paragraph', 'text': 'Done'}]}
        self.assertEqual(client.get_me(), {'value': 0})
        client.get_webhook_info()
        client.create_forum_topic(11, 'Storage')
        client.send_rich_message(11, rich, thread_id=3)
        client.edit_message_text(11, 12, rich)
        client.send_rich_message_draft(11, 9, rich, thread_id=3)
        client.answer_callback_query('query-1', 'Recorded')
        self.assertEqual([(method, body) for method, body, _ in opener.calls], [
            ('getMe', {}), ('getWebhookInfo', {}), ('createForumTopic', {'chat_id': 11, 'name': 'Storage'}),
            ('sendRichMessage', {'chat_id': 11, 'message_thread_id': 3, 'rich_message': rich}),
            ('editMessageText', {'chat_id': 11, 'message_id': 12, 'rich_message': rich}),
            ('sendRichMessageDraft', {'chat_id': 11, 'message_thread_id': 3, 'draft_id': 9, 'rich_message': rich, 'can_stop': True}),
            ('answerCallbackQuery', {'callback_query_id': 'query-1', 'text': 'Recorded'}),
        ])

    def test_poll_offset_follows_inbound_commit(self):
        store = self.store()
        opener = Opener(fixture('webhook_empty'), {'ok': True, 'result': [fixture('group'), fixture('other')]}, {'ok': True, 'result': []})
        poller = telegram.Poller(telegram.Client(TOKEN, opener=opener), store)
        self.assertEqual(len(poller.poll()), 2)
        self.assertEqual(poller.offset, 108)
        poller.poll()
        self.assertEqual(opener.calls[1][1], {'timeout': 30, 'allowed_updates': []})
        self.assertEqual(opener.calls[2][1]['offset'], 108)
        with sqlite3.connect(store.path) as connection:
            self.assertEqual(connection.execute('SELECT count(*) FROM inbound').fetchone()[0], 2)

    def test_failed_commit_keeps_offset(self):
        store = self.store()
        opener = Opener(fixture('webhook_empty'), {'ok': True, 'result': [fixture('private')]})
        poller = telegram.Poller(telegram.Client(TOKEN, opener=opener), store)
        def fail(update):
            raise RuntimeError('commit failed')
        store.ingest = fail
        with self.assertRaisesRegex(RuntimeError, 'commit failed'):
            poller.poll()
        self.assertIsNone(poller.offset)

    def test_set_webhook_refused_without_deletion(self):
        opener = Opener(fixture('webhook_set'))
        poller = telegram.Poller(telegram.Client(TOKEN, opener=opener), self.store())
        with self.assertRaisesRegex(telegram.TelegramError, 'getWebhookInfo: webhook is set'):
            poller.poll()
        self.assertEqual([call[0] for call in opener.calls], ['getWebhookInfo'])

    def test_retry_after_in_json_and_http_error(self):
        for response in (fixture('retry_after'), HTTPError('https://example/' + TOKEN, 429, TOKEN, {}, io.BytesIO(json.dumps(fixture('retry_after')).encode()))):
            with self.subTest(response=type(response).__name__):
                sleeps = []
                opener = Opener(response, {'ok': True, 'result': True})
                self.assertTrue(telegram.Client(TOKEN, opener=opener, sleep=sleeps.append).answer_callback_query('q'))
                self.assertEqual(sleeps, [2])
                self.assertEqual(len(opener.calls), 2)

    def test_errors_and_chains_do_not_expose_credential(self):
        errors = [TimeoutError(TOKEN), URLError(TOKEN), HTTPError('https://example/' + TOKEN, 403, TOKEN, {}, io.BytesIO(b'bad')), fixture('api_error'), {'ok': False, 'description': TOKEN}, {'ok': True}, ValueError(TOKEN)]
        records = []
        class Capture(logging.Handler):
            def emit(self, record):
                records.append(record.getMessage())
        handler = Capture()
        logging.getLogger().addHandler(handler)
        self.addCleanup(logging.getLogger().removeHandler, handler)
        for response in errors:
            with self.subTest(response=type(response).__name__):
                client = telegram.Client(TOKEN, opener=Opener(response))
                try:
                    client.get_me()
                except telegram.TelegramError as error:
                    self.assertIn('getMe', str(error))
                    self.assertNotIn(TOKEN, str(error) + repr(error) + ''.join(traceback.format_exception(error)))
                    self.assertIsNone(error.__context__)
                    self.assertIsNone(error.__cause__)
                else:
                    self.fail('expected sanitized TelegramError')
        self.assertNotIn(TOKEN, repr(records))
