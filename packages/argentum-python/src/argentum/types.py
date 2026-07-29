"""Wire types for the Argentum API.

Generated from apps/backend/openapi/v1.yaml — the same document the server
serves at GET /v1/openapi.json and CI diffs against the gin route tree.

Do not edit. Run `pnpm --filter @argentum/openapi-tools build` and commit.
Contract version: 2026-07-28
"""

from __future__ import annotations

from typing import Any, Dict, List, Literal, TypedDict, Union

API_VERSION = "2026-07-28"

class ChatEventDelta(TypedDict, total=False):
    """A fragment of the answer as it is written. Never persisted; carries no
    `id:`.

    Always present: content.
    """
    content: str


class ChatEventError(TypedDict, total=False):
    """The turn failed. Terminal.

    Always present: message.
    """
    message: str


class ChatEventStarted(TypedDict, total=False):
    """The worker picked the turn up.

    Always present: thread_id, at.
    """
    thread_id: str
    run_id: str
    at: str


class ChatEventThinking(TypedDict, total=False):
    """A one-line statement of what the agent is doing.

    Always present: step.
    """
    step: str


class ChatEventTool(TypedDict, total=False):
    """The tool's **name only** — never its arguments or its result. Those
    carry the SQL the agent ran, and the place for that is the audit log,
    where it is redacted on the way in and reachable only by an admin.
    """
    tool: str


class ChatRequest(TypedDict, total=False):
    """Send `user_ref` **or** `thread_id`. `user_ref` keys the thread when
    there is no `thread_id`, and is what makes the spend attributable.

    Always present: message.
    """
    # The question you want answered.
    message: str
    # Continue a conversation you are already tracking.
    thread_id: str
    # Your own identifier for the person asking.
    user_ref: str


class ConflictError(TypedDict, total=False):
    """A 409. `in_flight` is present only for `request_in_flight`, and its
    contents are whatever ids identify the work — `{thread_id, run_id,
    started_at}` for a chat turn, `{report_id, status}` for a report.

    Always present: error.
    """
    error: ErrorDetail
    # The ids of the request that is still running.
    in_flight: Dict[str, Any]


class CreateReportRequest(TypedDict, total=False):
    """Send `user_ref` **or** `thread_id`: the first names who the report is
    for, the second continues a conversation that already knows.

    Always present: prompt.
    """
    # What the report should contain, in words.
    prompt: str
    # Defaults to `pdf`.
    format: DocumentFormat
    # Your own identifier for the person this is on behalf of.
    user_ref: str
    # Continue an existing `api` conversation instead of starting one.
    thread_id: str
    # Receives the signed `report.completed` body.
    callback_url: str
    # `id` or `en`.
    locale: str
    # An ISO 4217 code.
    currency: str


class Credits(TypedDict, total=False):
    """`enforced: false` means nothing was consulted. It is not a zero balance
    — reporting `$0.00` there would read as "you are out of credit", which
    is the opposite of what an unenforced deployment is saying. A tenant on
    their own LLM key reports `byo_llm: true` and no balance.

    Always present: enforced.
    """
    enforced: bool
    byo_llm: bool
    # The verdict the budget check reached.
    status: str
    # Dollars, not the micro-USD the system counts in — this number is read by a person.
    balance_usd: float
    grant_usd: float
    remaining_pct: float


class Document(TypedDict, total=False):
    """One generated file.

    Always present: id, object, filename, format, size_bytes, source, created_at.
    """
    id: str
    object: Literal["document"]
    filename: str
    format: DocumentFormat
    size_bytes: int
    # Which door produced it.
    source: Literal["agent", "api"]
    # Absent for a document the render door produced — it has no conversation behind it.
    thread_id: str
    created_at: str
    # Presigned and short-lived, re-issued on every read rather than stored.
    download_url: str
    expires_at: str


class DocumentPage(TypedDict, total=False):
    """Always present: data, has_more.
    """
    data: List[Document]
    # Not `len(data) == limit`.
    has_more: bool
    next_cursor: str


class Error(TypedDict, total=False):
    """Every failure under `/v1` has this shape. A bare `{"error": "…"}` string
    anywhere here is a defect.

    Always present: error.
    """
    error: ErrorDetail


class ErrorDetail(TypedDict, total=False):
    """Switch on `type`. `code` is the specific reason within that class, and
    only `message` is meant for a human to read.

    Always present: type, code, message.
    """
    # The coarse class.
    type: Literal["invalid_request", "authentication", "permission", "not_found", "rate_limit", "budget_exhausted", "server"]
    # The specific reason — `insufficient_scope`, `spec_too_large`, `thread_not_found`, and so on.
    code: str
    # A sentence for a person.
    message: str
    # The field or header that was wrong, when there is one.
    param: str
    # The same value as the `X-Request-Id` header.
    request_id: str


class Me(TypedDict, total=False):
    """`credits` is absent when this deployment cannot read a balance, and
    `webhooks` only appears for a key carrying `write:reports` — a read-only
    key has no callback to verify and no reason to hold the secret.

    Always present: api_version, company, key, rate_limit.
    """
    # The contract version, as a date.
    api_version: str
    company: Dict[str, Any]
    key: Dict[str, Any]
    rate_limit: Dict[str, Any]
    credits: Credits
    webhooks: WebhookSettings


class Message(TypedDict, total=False):
    """One turn in a transcript. Token counts and latency are deliberately not
    here: they are per-message zeros for every streamed turn, and a field
    that is honestly zero most of the time is a field integrators report as
    a bug.

    Always present: object, role, content.
    """
    id: str
    object: Literal["message"]
    role: Literal["user", "assistant", "system"]
    content: str
    created_at: str


class MessagePage(TypedDict, total=False):
    """Always present: data, has_more.
    """
    data: List[Message]
    has_more: bool
    next_cursor: str


class PendingTurn(TypedDict, total=False):
    """The 504 body. The field names match the `409 request_in_flight` on
    purpose: both mean "the work is still running and here is how to find
    it", and a caller should be able to parse them with one branch.

    Always present: error, in_flight.
    """
    error: ErrorDetail
    in_flight: Dict[str, Any]


class RenderedDocument(TypedDict, total=False):
    """What the render door returns when `Accept` asks for JSON.

    Always present: object, document.
    """
    object: Literal["document"]
    document: Document
    request_id: str


class Report(TypedDict, total=False):
    """One shape for both doors and for the poll route, so a caller writes the
    collection path once whether the job came from a prompt or from a spec
    that ran long.

    Always present: id, object, status, kind, format, created_at.
    """
    id: str
    object: Literal["report"]
    status: Literal["queued", "running", "completed", "failed"]
    # `agentic` for a prompt; `render` for a spec that outran the synchronous window.
    kind: Literal["agentic", "render"]
    format: DocumentFormat
    # The conversation behind an agentic report — continue it through `POST /v1/chat`.
    thread_id: str
    document: Document
    # Why a `failed` report failed.
    error: str
    created_at: str
    request_id: str


class ReportEventProgress(TypedDict, total=False):
    """What is happening, not what the agent is saying.

    Always present: type, at.
    """
    type: Literal["started", "tool_call", "tool_result", "thinking"]
    at: str
    tool: str
    step: str


class ReportSpec(TypedDict, total=False):
    """One file, one format, one content tree.

    `content` carries one of three shapes: `sections` for a PDF or a deck,
    `table` for a CSV, `sheets` or `table` for a workbook.

    Always present: format, content.
    """
    # `2` for the enterprise layout — cover page, running header, footer.
    spec_version: Literal[1, 2]
    format: DocumentFormat
    # Without a directory.
    filename: str
    title: str
    # `id` or `en`.
    locale: str
    # An ISO 4217 code.
    currency: str
    # RFC3339.
    generated_at: str
    meta: ReportSpecMeta
    content: ReportSpecContent


class ReportSpecAxis(TypedDict, total=False):
    """`min` matters more than it looks: a bar chart whose axis starts at 94%
    of the smallest bar shows a 3% movement as a doubling, which is the most
    common way a correct number is used to say something false.
    """
    label: str
    min: float
    max: float


class ReportSpecChart(TypedDict, total=False):
    """There is no `stacked` flag: stacking is a chart type. A flag would make
    `{type: "pie", stacked: true}` expressible, and every such combination
    is a validation rule someone has to write, test and explain.
    """
    type: Literal["line", "bar", "grouped_bar", "stacked_bar", "pie", "donut", "sparkline"]
    title: str
    labels: List[str]
    series: List[ReportSpecSeries]
    fmt: str
    y_axis: ReportSpecAxis
    # Zero takes the renderer's default, which is almost always right: the width is the measure and the…
    height_mm: float


class ReportSpecContent(TypedDict, total=False):
    sections: List[ReportSpecSection]
    table: ReportSpecTable
    sheets: List[ReportSpecSheet]


class ReportSpecItem(TypedDict, total=False):
    """A row of a `key_value` block or a card in a `kpi_row`. Both vocabularies
    are accepted — `{k, v}` and `{label, value}` — because rendering nothing
    when a caller picked the wrong pair of field names is not a defensible
    failure.
    """
    k: str
    v: str
    label: str
    value: ReportSpecCell
    # Percent units: `12.5` is +12.5%, not +1250%.
    delta_pct: float
    # Empty reads the direction off the sign of `delta_pct`.
    direction: Literal["up", "down", "increase", "decrease", "rising", "falling", "naik", "turun"]
    # Whether an up arrow is good news.
    higher_is_better: bool
    fmt: str


class ReportSpecMeta(TypedDict, total=False):
    """What lands in the PDF's info dictionary. A document that leaves the
    building with an empty Title in its properties is one a records system
    cannot file.
    """
    author: str
    subject: str
    keywords: str


class ReportSpecSection(TypedDict, total=False):
    """One block in the document flow. Every payload field for every section
    type is on this one object rather than in a union — a model that puts
    `text` on a callout instead of inside it still renders something.

    Always present: type.
    """
    type: Literal["cover", "heading", "paragraph", "kpi_row", "table", "callout", "key_value", "chart", "footnote", "page_break", "spacer"]
    subtitle: str
    period: str
    prepared_for: str
    prepared_by: str
    confidentiality: str
    text: str
    # Heading level, 1–3.
    level: int
    title: str
    # A callout's colour.
    tone: Literal["info", "warn", "good"]
    items: List[ReportSpecItem]
    columns: List[ReportSpecColumn]
    rows: List[List[ReportSpecCell]]
    total_row: List[ReportSpecCell]
    caption: str
    # A v1 spacer's height in millimetres.
    size: float
    chart: ReportSpecChart


class ReportSpecSeries(TypedDict, total=False):
    """One plotted population. For a pie or a donut only the first series is
    drawn, because a pie of two series is two pies.
    """
    name: str
    values: List[float]


class ReportSpecSheet(TypedDict, total=False):
    """One workbook tab.

    Always present: name, columns, rows.
    """
    name: str
    columns: List[ReportSpecColumn]
    rows: List[List[ReportSpecCell]]


class ReportSpecTable(TypedDict, total=False):
    """Always present: columns, rows.
    """
    columns: List[ReportSpecColumn]
    rows: List[List[ReportSpecCell]]
    total_row: List[ReportSpecCell]
    caption: str


class Thread(TypedDict, total=False):
    """A conversation. Deliberately not the internal thread record, which
    carries a phone number, a Discord user id and two Lark keys — none of
    which an `api` thread has, and all of which would become part of a
    published contract the moment one was serialized.

    Always present: id, object, last_message_at, created_at.
    """
    id: str
    object: Literal["thread"]
    user_ref: str
    title: str
    # The rolling topic summary the thread resolver keeps.
    summary: str
    last_message_at: str
    created_at: str


class ThreadPage(TypedDict, total=False):
    """Always present: data, has_more.
    """
    data: List[Thread]
    has_more: bool
    next_cursor: str


class Turn(TypedDict, total=False):
    """One question and its answer. The same shape arrives as the `final` SSE
    frame, so a caller who starts on the synchronous door and moves to the
    stream does not rewrite their parser.

    Always present: object, thread_id, message.
    """
    object: Literal["turn"]
    thread_id: str
    # Identifies this turn within the thread.
    run_id: str
    message: Message
    usage: Usage
    request_id: str


class Usage(TypedDict, total=False):
    """What this turn cost. Best-effort and omitted entirely rather than
    reported as zeros: an all-zero window happens when attaching to a turn
    that had already finished, and publishing `tokens_in: 0` there would
    state something false about a turn that cost real money.

    Always present: tokens_in, tokens_out, cost_usd.
    """
    tokens_in: int
    tokens_out: int
    # Dollars, for the reason `credits` gives.
    cost_usd: float


class WebhookSettings(TypedDict, total=False):
    """The signing secret for `callback_url` deliveries, minted on first read.

    Always present: signing_secret, signature_header, how.
    """
    signing_secret: str
    signature_header: str
    # How to verify, in one sentence, so it travels with the secret.
    how: str


# Type aliases. These are evaluated at import time, so they follow the
# classes they name.

ChatEventFinal = Turn

ChatEventMessage = Message

ChatEvent = Union[ChatEventStarted, ChatEventDelta, ChatEventThinking, ChatEventTool, ChatEventMessage, ChatEventError, ChatEventFinal]

DocumentFormat = Literal["pdf", "pptx", "xlsx", "csv"]

ReportEventReport = Report

ReportEvent = Union[ReportEventProgress, ReportEventReport, ChatEventError]

ReportSpecCell = Union[Union[str, float, bool, None], Dict[str, Any]]

ReportSpecColumn = Union[str, Dict[str, Any]]

Scope = Literal["read:metrics", "read:threads", "read:usage", "read:audit", "read:documents", "write:chat", "write:actions", "write:reports"]

__all__ = [
    "API_VERSION",
    "ChatEvent",
    "ChatEventDelta",
    "ChatEventError",
    "ChatEventFinal",
    "ChatEventMessage",
    "ChatEventStarted",
    "ChatEventThinking",
    "ChatEventTool",
    "ChatRequest",
    "ConflictError",
    "CreateReportRequest",
    "Credits",
    "Document",
    "DocumentFormat",
    "DocumentPage",
    "Error",
    "ErrorDetail",
    "Me",
    "Message",
    "MessagePage",
    "PendingTurn",
    "RenderedDocument",
    "Report",
    "ReportEvent",
    "ReportEventProgress",
    "ReportEventReport",
    "ReportSpec",
    "ReportSpecAxis",
    "ReportSpecCell",
    "ReportSpecChart",
    "ReportSpecColumn",
    "ReportSpecContent",
    "ReportSpecItem",
    "ReportSpecMeta",
    "ReportSpecSection",
    "ReportSpecSeries",
    "ReportSpecSheet",
    "ReportSpecTable",
    "Scope",
    "Thread",
    "ThreadPage",
    "Turn",
    "Usage",
    "WebhookSettings",
]
