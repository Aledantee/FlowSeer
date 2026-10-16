import json
import time
from dataclasses import asdict, dataclass
from typing import Callable, Literal
from urllib.error import HTTPError
from urllib.request import Request, urlopen

from harness.state import Store


@dataclass(frozen=True)
class Update:
    update_id: int
    kind: Literal['message', 'callback', 'stop', 'other']
    raw: dict
    chat_id: int | None = None
    thread_id: int | None = None
    sender_id: int | None = None
    message_id: int | None = None
    reply_to_message_id: int | None = None
    callback_id: str | None = None
    callback_query_id: str | None = None
    draft_id: int | None = None
    text: str | None = None

    def as_store_update(self) -> dict:
        return asdict(self)


def normalize(raw: dict) -> Update:
    fields = {'update_id': raw['update_id'], 'raw': raw}
    if 'message' in raw:
        message = raw['message']
        kind = 'message'
        fields['sender_id'] = message.get('from', {}).get('id')
        fields['text'] = message.get('text')
        fields['reply_to_message_id'] = message.get('reply_to_message', {}).get('message_id')
    elif 'callback_query' in raw:
        callback = raw['callback_query']
        message = callback.get('message', {})
        kind = 'callback'
        fields['sender_id'] = callback['from']['id']
        fields['callback_id'] = callback.get('data')
        fields['callback_query_id'] = callback['id']
    elif 'stopped_message_generation' in raw:
        message = raw['stopped_message_generation']
        kind = 'stop'
        fields['draft_id'] = message['draft_id']
    else:
        message = {}
        kind = 'other'
    fields['chat_id'] = message.get('chat', {}).get('id')
    fields['thread_id'] = message.get('message_thread_id')
    fields['message_id'] = message.get('message_id')
    return Update(kind=kind, **fields)


class TelegramError(Exception):
    pass


class Client:
    def __init__(self, token: str, *, opener: Callable = urlopen, sleep: Callable = time.sleep):
        self._token = token
        self._opener = opener
        self._sleep = sleep

    def _exchange(self, method: str, payload: dict, timeout: int) -> dict | None:
        # Leaving this frame discards URL-bearing exceptions before a public error is raised.
        try:
            request = Request(
                f'https://api.telegram.org/bot{self._token}/{method}',
                data=json.dumps(payload, ensure_ascii=False).encode(),
                headers={'Content-Type': 'application/json'},
                method='POST',
            )
            with self._opener(request, timeout=timeout) as response:
                result = json.load(response)
            return result if isinstance(result, dict) else None
        except HTTPError as error:
            try:
                result = json.load(error)
                return result if isinstance(result, dict) else None
            except Exception:
                return None
            finally:
                error.close()
        except Exception:
            return None

    def _call(self, method: str, payload: dict, *, timeout: int = 30):
        for attempt in range(2):
            response = self._exchange(method, payload, timeout)
            if response and response.get('ok') is True and 'result' in response:
                return response['result']
            delay = (response or {}).get('parameters', {})
            delay = delay.get('retry_after') if isinstance(delay, dict) else None
            if type(delay) is int and delay >= 0 and attempt == 0:
                self._sleep(delay)
                continue
            raise TelegramError(f'{method}: request failed')

    def get_me(self) -> dict:
        return self._call('getMe', {})

    def get_webhook_info(self) -> dict:
        return self._call('getWebhookInfo', {})

    def get_updates(self, offset: int | None = None, timeout: int = 30) -> list[Update]:
        payload = {'timeout': timeout, 'allowed_updates': []}
        if offset is not None:
            payload['offset'] = offset
        return [normalize(raw) for raw in self._call('getUpdates', payload, timeout=timeout + 10)]

    def create_forum_topic(self, chat_id: int, name: str) -> dict:
        return self._call('createForumTopic', {'chat_id': chat_id, 'name': name})

    def send_rich_message(self, chat_id: int, rich_message: dict, *, thread_id: int | None = None) -> dict:
        payload = {'chat_id': chat_id, 'rich_message': rich_message}
        if thread_id is not None:
            payload['message_thread_id'] = thread_id
        return self._call('sendRichMessage', payload)

    def edit_message_text(self, chat_id: int, message_id: int, rich_message: dict) -> dict:
        return self._call('editMessageText', {'chat_id': chat_id, 'message_id': message_id, 'rich_message': rich_message})

    def send_rich_message_draft(self, chat_id: int, draft_id: int, rich_message: dict, *, thread_id: int | None = None) -> bool:
        payload = {'chat_id': chat_id, 'draft_id': draft_id, 'rich_message': rich_message, 'can_stop': True}
        if thread_id is not None:
            payload['message_thread_id'] = thread_id
        return self._call('sendRichMessageDraft', payload)

    def answer_callback_query(self, callback_query_id: str, text: str | None = None) -> bool:
        payload = {'callback_query_id': callback_query_id}
        if text is not None:
            payload['text'] = text
        return self._call('answerCallbackQuery', payload)


class Poller:
    def __init__(self, client: Client, store: Store):
        self.client = client
        self.store = store
        self.offset: int | None = None
        self._checked_webhook = False

    def poll(self) -> list[dict]:
        if not self._checked_webhook:
            if self.client.get_webhook_info()['url']:
                raise TelegramError('getWebhookInfo: webhook is set')
            self._checked_webhook = True
        results = []
        for update in self.client.get_updates(self.offset):
            results.append(self.store.ingest(update.as_store_update()))
            self.offset = max(self.offset or 0, update.update_id + 1)
        return results
