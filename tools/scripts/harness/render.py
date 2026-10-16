import json
from dataclasses import dataclass

from harness.state import Store


@dataclass(frozen=True)
class ButtonSummary:
    label: str
    callback_id: str


@dataclass(frozen=True)
class TaskSummary:
    task_id: str
    title: str
    state: str
    outcome: str
    controls: tuple[ButtonSummary, ...] = ()
    scope: str = ''
    verification: str = ''
    review: str = ''
    risks: str = ''
    operation: str = ''


@dataclass(frozen=True)
class OverviewSummary:
    tasks: tuple[TaskSummary, ...]


@dataclass(frozen=True)
class ActivitySummary:
    entries: tuple[str, ...]


@dataclass(frozen=True)
class AgentChoice:
    agent: str
    model: str
    callback_id: str


@dataclass(frozen=True)
class NewTaskSummary:
    brief: str
    default: AgentChoice
    alternatives: tuple[AgentChoice, ...]


@dataclass(frozen=True)
class OptionSummary:
    label: str
    tradeoff: str
    callback_id: str
    recommended: bool


@dataclass(frozen=True)
class DecisionSummary:
    decision_id: str
    revision: int
    question: str
    options: tuple[OptionSummary, ...]
    custom_callback_id: str


def new_task_summary(store: Store, brief: str) -> NewTaskSummary:
    callbacks = store.issue_agent_options(brief)
    choices = tuple(AgentChoice(pair.agent, pair.model, callback) for pair, callback in zip((store.config.default, *store.config.alternatives), callbacks))
    return NewTaskSummary(brief, choices[0], choices[1:])


def decision_summary(store: Store, decision_id: str) -> DecisionSummary:
    row = store.decision(decision_id)
    payload = json.loads(row['payload'])
    options = tuple(OptionSummary(option['label'], option['tradeoff'], store.issue_callback(row['task_id'], decision_id, option['id']), option['id'] == payload['recommendation']) for option in payload['options'])
    return DecisionSummary(decision_id, row['revision'], payload['question'], options, store.issue_callback(row['task_id'], decision_id, None))


def _plain(text: str, limit: int = 512) -> str:
    # A RichText string is literal text, so worker markup never becomes an entity.
    if not isinstance(text, str):
        raise TypeError('RichText requires plain text')
    return text if len(text) <= limit else text[:limit - 1] + '…'


def _paragraph(text: str) -> dict:
    return {'type': 'paragraph', 'text': _plain(text)}


def _heading(text: str) -> dict:
    return {'type': 'heading', 'text': _plain(text), 'size': 3}


def _buttons(controls: tuple[ButtonSummary, ...]) -> list[dict]:
    buttons = []
    for control in controls:
        if not isinstance(control.callback_id, str) or not 1 <= len(control.callback_id.encode()) <= 32:
            raise ValueError('callback ID must contain 1 to 32 bytes')
        buttons.append({'text': _plain(control.label, 128), 'callback_data': control.callback_id})
    return [{'type': 'buttons', 'buttons': buttons[index:index + 8]} for index in range(0, len(buttons), 8)]


def _card(blocks: list[dict]) -> dict:
    card = {'blocks': blocks, 'skip_entity_detection': True}
    if len(json.dumps(card, ensure_ascii=False).encode()) > 24576:
        raise ValueError('card exceeds 24 KiB')
    return card


def overview(summary: OverviewSummary) -> dict:
    groups = ('active', 'awaiting decision', 'blocked', 'finished')
    for task in summary.tasks:
        if task.state not in groups:
            raise ValueError(f'unknown task state: {task.state}')
    blocks = []
    for group in groups:
        tasks = [task for task in summary.tasks if task.state == group]
        blocks.append(_heading(group.capitalize()))
        cells = [[{'text': title, 'is_header': True, 'align': 'left', 'valign': 'top'} for title in ('Task', 'Title', 'Outcome')]]
        for task in tasks[:4]:
            cells.append([{'text': _plain(text, 96), 'align': 'left', 'valign': 'top'} for text in (task.task_id, task.title, task.outcome)])
        blocks.append({'type': 'table', 'cells': cells})
        if len(tasks) > 4:
            blocks.append(_paragraph(f'{len(tasks) - 4} more tasks…'))
    blocks.append(_paragraph('New Task: Send a brief, then choose the coordinator agent and model.'))
    return _card(blocks)


def status(summary: TaskSummary) -> dict:
    blocks = [_heading(summary.title), _paragraph(f'Task {summary.task_id}'), _paragraph(summary.state), _paragraph(summary.outcome)]
    for label, text in (('Scope', summary.scope), ('Verification', summary.verification), ('Review', summary.review), ('Risks', summary.risks), ('Proposed operation', summary.operation)):
        if text:
            blocks.extend([_heading(label), _paragraph(text)])
    blocks.extend(_buttons(summary.controls))
    return _card(blocks)


def activity(summary: ActivitySummary) -> dict:
    blocks = [_paragraph(entry) for entry in summary.entries[-8:]]
    if len(summary.entries) > 8:
        blocks.insert(0, _paragraph(f'{len(summary.entries) - 8} earlier entries omitted…'))
    return _card([{'type': 'details', 'summary': 'Activity', 'blocks': blocks}])


def new_task(summary: NewTaskSummary) -> dict:
    default = summary.default
    blocks = [_heading('New Task'), _paragraph(summary.brief), _paragraph(f'Default coordinator: {default.agent} / {default.model}')]
    controls = tuple(ButtonSummary(f'{choice.agent} / {choice.model}' + (' (default)' if index == 0 else ''), choice.callback_id) for index, choice in enumerate((default, *summary.alternatives)))
    blocks.extend(_buttons(controls))
    return _card(blocks)


def decision(summary: DecisionSummary) -> dict:
    blocks = [_heading(summary.question), _paragraph(f'Decision {summary.decision_id} · Revision {summary.revision}')]
    controls = []
    for option in summary.options:
        label = _plain(option.label, 48) + (' (recommended)' if option.recommended else '')
        blocks.extend([_paragraph(label), _paragraph(_plain(option.tradeoff, 96))])
        controls.append(ButtonSummary(label, option.callback_id))
    blocks.append(_paragraph('Custom Answer: Reply to this decision with your answer.'))
    controls.append(ButtonSummary('Custom Answer', summary.custom_callback_id))
    blocks.extend(_buttons(tuple(controls)))
    return _card(blocks)
