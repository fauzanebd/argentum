"""The synchronous client."""

from __future__ import annotations

import time
from datetime import datetime, timezone
from typing import Any, Dict, Iterator, List, Mapping, Optional, Union

import httpx

from . import types as t
from ._policy import (
    backoff_seconds,
    build_headers,
    clean_params,
    is_retryable,
    new_idempotency_key,
    parse_retry_after,
    resolve_credentials,
)
from ._sse import Event, _FrameAssembler
from .errors import ArgentumError, TransportError, error_from_body


class Argentum:
    """The Argentum client.

    ::

        from argentum import Argentum

        client = Argentum()                 # reads ARGENTUM_API_KEY / ARGENTUM_BASE_URL
        pdf = client.reports.render(spec)   # bytes

    The three things it does that a ``requests`` wrapper does not: it retries
    429s and 5xx with backoff on the server's own ``Retry-After``, it puts an
    ``Idempotency-Key`` on every write and **reuses it across retries**, and it
    raises errors carrying the API's envelope — type, code, param, request id.
    """

    def __init__(
        self,
        api_key: Optional[str] = None,
        base_url: Optional[str] = None,
        *,
        timeout: float = 60.0,
        max_retries: int = 2,
        http_client: Optional[httpx.Client] = None,
    ) -> None:
        self._api_key, self.base_url = resolve_credentials(api_key, base_url)
        self._timeout = timeout
        self._max_retries = max_retries
        self._owns_http = http_client is None
        # `timeout` bounds one attempt, never a turn: an agentic report takes
        # minutes and the poller waits on its own clock. Streams pass their own
        # read timeout of None, because a stream that has been quiet for a
        # minute is a stream sending heartbeats every fifteen seconds.
        self._http = http_client or httpx.Client(timeout=timeout)

        self.reports = Reports(self)
        self.documents = Documents(self)
        self.chat = Chat(self)

    # -- lifecycle ---------------------------------------------------------

    def close(self) -> None:
        if self._owns_http:
            self._http.close()

    def __enter__(self) -> "Argentum":
        return self

    def __exit__(self, *exc: Any) -> None:
        self.close()

    # -- the one call to make first ---------------------------------------

    def me(self) -> t.Me:
        """Who this key is, what it can do, and what the tenant has left to spend.

        It needs no scope, so it answers even for a key that can do nothing
        else, and its output is the one paste that makes a support question
        answerable.
        """
        return self.request_json("GET", "/v1/me")

    def agents(self) -> List[t.Agent]:
        """The agents this workspace has, and which one answers by default.

        A workspace can keep several — Finance, Ops, Support — each with its own
        persona, tools and databases. Pass an ``id`` from here as ``agent_id``
        to :meth:`Chat.send`, :meth:`Chat.stream` or :meth:`Reports.create`;
        omit it and the one with ``is_default`` answers.

        ::

            finance = next(a for a in client.agents() if a["name"] == "Finance")
            client.chat.send("Revenue last month?", user_ref="u_42", agent_id=finance["id"])

        Needs no scope, like :meth:`me`. Returns the list rather than the API's
        page envelope: the whole roster arrives at once and ``has_more`` is
        always false, so handing back a page would invite a loop that can never
        run twice.
        """
        page: t.AgentPage = self.request_json("GET", "/v1/agents")
        return page.get("data", [])

    def usage(
        self,
        *,
        since: Optional[Union[str, datetime]] = None,
        until: Optional[Union[str, datetime]] = None,
    ) -> t.UsageReport:
        """What this workspace spent over a window, and what is left.

        Not ``me()`` with more fields: ``me()`` answers "can I call at all",
        with no period attached to the number. This takes the period you bill
        your own users for — both bounds default to the current UTC calendar
        month — and breaks the spend down by model.

        Needs the ``read:usage`` scope.

        The parameters are ``since`` / ``until`` rather than ``from`` / ``to``
        because ``from`` is a Python keyword and cannot be a parameter name.
        They are sent as the API's own ``from`` and ``to``.
        """
        return self.request_json(
            "GET", "/v1/usage", params={"from": _rfc3339(since), "to": _rfc3339(until)}
        )

    # -- transport ---------------------------------------------------------

    def request(
        self,
        method: str,
        path: str,
        *,
        params: Optional[Mapping[str, Any]] = None,
        json: Any = None,
        accept: Optional[str] = None,
        headers: Optional[Mapping[str, str]] = None,
        idempotency_key: Optional[str] = None,
    ) -> httpx.Response:
        """One request, retried, returning the raw response."""
        key = idempotency_key or new_idempotency_key()
        request_headers = build_headers(
            self._api_key,
            accept=accept,
            json_body=json is not None,
            method=method,
            idempotency_key=key,
            extra=headers,
        )

        last: Optional[ArgentumError] = None
        for attempt in range(self._max_retries + 1):
            try:
                response = self._http.request(
                    method,
                    self.base_url + path,
                    params=clean_params(params or {}),
                    json=json,
                    headers=request_headers,
                )
            except httpx.HTTPError as exc:
                last = TransportError(
                    f"{method} {path} did not reach Argentum: {exc}",
                    type="transport",
                    code="transport_error",
                )
                if attempt == self._max_retries:
                    raise last from exc
                time.sleep(backoff_seconds(attempt, None))
                continue

            if response.is_success:
                return response

            last = _error_of(response)
            if attempt == self._max_retries or not is_retryable(response.status_code):
                raise last
            time.sleep(backoff_seconds(attempt, last.retry_after))

        raise last or TransportError("request failed", type="transport", code="transport_error")

    def request_json(self, method: str, path: str, **kwargs: Any) -> Any:
        kwargs.setdefault("accept", "application/json")
        response = self.request(method, path, **kwargs)
        if response.status_code == 204 or not response.content:
            return None
        return response.json()

    def request_bytes(self, method: str, path: str, **kwargs: Any) -> bytes:
        return self.request(method, path, **kwargs).content

    def events(
        self,
        method: str,
        path: str,
        *,
        json: Any = None,
        params: Optional[Mapping[str, Any]] = None,
        headers: Optional[Mapping[str, str]] = None,
        idempotency_key: Optional[str] = None,
    ) -> Iterator[Event]:
        """Opens a stream and yields its frames until a terminal one.

        Retries apply to the **opening** of the stream and nothing else: once a
        frame has been handed to the caller, resending the request would replay
        deltas they have already seen. A stream that breaks mid-turn is what
        ``chat.attach(thread_id, last_event_id=...)`` is for.
        """
        key = idempotency_key or new_idempotency_key()
        request_headers = build_headers(
            self._api_key,
            accept="text/event-stream",
            json_body=json is not None,
            method=method,
            idempotency_key=key,
            extra=headers,
        )

        for attempt in range(self._max_retries + 1):
            with self._http.stream(
                method,
                self.base_url + path,
                params=clean_params(params or {}),
                json=json,
                headers=request_headers,
                timeout=httpx.Timeout(self._timeout, read=None),
            ) as response:
                if not response.is_success:
                    response.read()
                    error = _error_of(response)
                    if attempt == self._max_retries or not is_retryable(response.status_code):
                        raise error
                    time.sleep(backoff_seconds(attempt, error.retry_after))
                    continue

                assembler = _FrameAssembler()
                for line in response.iter_lines():
                    event = assembler.feed(line)
                    if event is None:
                        continue
                    yield event
                    if event.terminal:
                        return
                return


def _error_of(response: httpx.Response) -> ArgentumError:
    """Reads a failure body without letting the read become the failure."""
    try:
        body = response.json()
    except ValueError:
        body = None
    return error_from_body(
        response.status_code,
        body,
        response.headers.get("X-Request-Id"),
        parse_retry_after(response.headers.get("Retry-After")),
    )


class Reports:
    """The two report doors, and the collection paths behind them."""

    def __init__(self, client: Argentum) -> None:
        self._client = client

    def render(self, spec: t.ReportSpec, *, idempotency_key: Optional[str] = None) -> bytes:
        """A spec in, the file's bytes out. Deterministic, no LLM, sub-second.

        Three responses can come back and this returns bytes for all three,
        which is the point of it being here rather than in your code:

        * the bytes, which is the ordinary case;
        * the document object, which is what a **replay** of this call returns —
          the bytes are not stored anywhere to be replayed, so the API hands
          back the object with a fresh URL instead, and we fetch it;
        * a ``202`` report, when the spec outran the server's synchronous
          window. We wait for the job and download what it produced.
        """
        response = self._client.request(
            "POST",
            "/v1/reports/render",
            json=spec,
            accept="application/octet-stream",
            idempotency_key=idempotency_key,
        )
        if "application/json" not in response.headers.get("Content-Type", ""):
            return response.content

        body = response.json()
        if body.get("object") == "document":
            return self._client.documents.download(body["document"]["id"])
        if body.get("object") == "report":
            return ReportJob(self._client, body).download()
        raise ArgentumError(
            f"The render door answered JSON this client does not recognise: {str(body)[:200]}",
            code="unexpected_response",
            status=response.status_code,
        )

    def render_document(self, spec: t.ReportSpec, *, idempotency_key: Optional[str] = None) -> t.Document:
        """The same call, returning metadata and a presigned URL instead of bytes."""
        body = self._client.request_json(
            "POST", "/v1/reports/render", json=spec, idempotency_key=idempotency_key
        )
        return body["document"]

    def create(
        self,
        prompt: str,
        *,
        user_ref: Optional[str] = None,
        thread_id: Optional[str] = None,
        agent_id: Optional[str] = None,
        format: str = "pdf",
        callback_url: Optional[str] = None,
        locale: Optional[str] = None,
        currency: Optional[str] = None,
        idempotency_key: Optional[str] = None,
    ) -> "ReportJob":
        """A prompt in, a real agent turn behind it. Returns a job you can wait on."""
        body: Dict[str, Any] = {"prompt": prompt, "format": format}
        for key, value in (
            ("user_ref", user_ref),
            ("thread_id", thread_id),
            ("agent_id", agent_id),
            ("callback_url", callback_url),
            ("locale", locale),
            ("currency", currency),
        ):
            if value:
                body[key] = value
        report = self._client.request_json("POST", "/v1/reports", json=body, idempotency_key=idempotency_key)
        return ReportJob(self._client, report)

    def get(self, report_id: str) -> t.Report:
        return self._client.request_json("GET", f"/v1/reports/{report_id}")

    def job(self, report_id: str) -> "ReportJob":
        """Pick a job you already have the id of back up."""
        return ReportJob(self._client, self.get(report_id))

    def stream(self, report_id: str) -> Iterator[Event]:
        """Progress events for a running job. Ends on the terminal ``report`` frame."""
        return self._client.events("GET", f"/v1/reports/{report_id}/events")


class ReportJob:
    """A report that is being worked on.

    It holds the last state it saw rather than re-fetching on every attribute
    read: a poller that costs a request to ask what it already knows is a poller
    that rate-limits its own caller.
    """

    def __init__(self, client: Argentum, report: t.Report) -> None:
        self._client = client
        self.report = report

    @property
    def id(self) -> str:
        return self.report["id"]

    @property
    def status(self) -> str:
        return self.report["status"]

    @property
    def done(self) -> bool:
        return self.status in ("completed", "failed")

    def refresh(self) -> t.Report:
        self.report = self._client.reports.get(self.id)
        return self.report

    def wait(self, *, poll_seconds: float = 2.0, timeout: float = 600.0) -> t.Report:
        """Poll until the job is terminal.

        Polling rather than streaming, deliberately: the stream is the better
        experience when you want to *show* progress and the worse one when all
        you want is the file — a dropped connection mid-turn would have to be
        reconnected and reconciled, where a poll that fails is just a poll you
        do again. :meth:`stream` is right there when you want the other
        behaviour.
        """
        deadline = time.monotonic() + timeout
        while not self.done:
            if time.monotonic() > deadline:
                raise ArgentumError(
                    f"Report {self.id} was still {self.status} after the client's timeout. "
                    f"It is still running — poll it with client.reports.get({self.id!r}).",
                    code="client_timeout",
                )
            time.sleep(poll_seconds)
            self.refresh()
        return self.report

    def stream(self) -> Iterator[Event]:
        """Progress events for this job.

        The terminal ``report`` frame carries the whole object, so it is stored
        on the way past. Without that, a caller who streamed to completion and
        then called :meth:`download` would find the job still holding the
        ``queued`` snapshot it was constructed with, and poll for a state it had
        already been told.
        """
        for event in self._client.reports.stream(self.id):
            if event.event == "report":
                self.report = event.data
            yield event

    def download(self, **wait_kwargs: Any) -> bytes:
        """Wait for the job and hand back the file's bytes."""
        if not self.done:
            self.wait(**wait_kwargs)
        document = self.report.get("document")
        if not document:
            raise ArgentumError(
                _describe_missing_document(self.report),
                code="report_failed" if self.status == "failed" else "report_no_document",
            )
        return self._client.documents.download(document["id"])


def _describe_missing_document(report: t.Report) -> str:
    """Explains a report with no document to download.

    A **completed** report without one is not an error on our side and not a
    transient state: the agent was asked for a report and answered in prose. The
    API says so by completing the job with no ``document``, and the message has
    to say the same thing — the first version of this said "has produced no
    document yet", which reads as "wait longer" for something that will never
    arrive. The thread is the useful thing to hand back, because the answer is
    in it.
    """
    status = report.get("status")
    if status == "failed":
        return f"The report failed: {report.get('error') or 'no reason given'}"
    if status == "completed":
        thread = report.get("thread_id")
        where = (
            f"Read what it said with client.chat.threads.iter_messages({thread!r}), "
            "or ask again with a more specific prompt."
            if thread
            else "Ask again with a more specific prompt."
        )
        return (
            f"Report {report.get('id')} completed without generating a document — "
            f"the agent answered in prose instead. {where}"
        )
    return f"Report {report.get('id')} is {status} and has produced no document yet."


class Documents:
    """The files either report door produced. Read-only: there is no upload."""

    def __init__(self, client: Argentum) -> None:
        self._client = client

    def list(
        self,
        *,
        limit: Optional[int] = None,
        cursor: Optional[str] = None,
        format: Optional[str] = None,
        created_after: Optional[str] = None,
        created_before: Optional[str] = None,
    ) -> t.DocumentPage:
        return self._client.request_json(
            "GET",
            "/v1/documents",
            params={
                "limit": limit,
                "cursor": cursor,
                "format": format,
                "created_after": created_after,
                "created_before": created_before,
            },
        )

    def iter(self, **kwargs: Any) -> Iterator[t.Document]:
        """Every document, following the cursor for you.

        It exists because the failure it prevents is silent: a caller who reads
        ``data`` and forgets ``has_more`` sees one page and believes it is the
        whole list.
        """
        cursor = kwargs.pop("cursor", None)
        while True:
            page = self.list(cursor=cursor, **kwargs)
            for document in page.get("data", []):
                yield document
            cursor = page.get("next_cursor")
            if not page.get("has_more") or not cursor:
                return

    def get(self, document_id: str) -> t.Document:
        """One document, with a ``download_url`` presigned at the moment you asked."""
        return self._client.request_json("GET", f"/v1/documents/{document_id}")

    def download(self, document_id: str) -> bytes:
        """The file's bytes, streamed from the API rather than from a redirect."""
        return self._client.request_bytes(
            "GET", f"/v1/documents/{document_id}/content", accept="application/octet-stream"
        )


class Chat:
    """A question in, an answer out."""

    def __init__(self, client: Argentum) -> None:
        self._client = client
        self.threads = Threads(client)

    def send(
        self,
        message: str,
        *,
        user_ref: Optional[str] = None,
        thread_id: Optional[str] = None,
        agent_id: Optional[str] = None,
        idempotency_key: Optional[str] = None,
    ) -> t.Turn:
        """Ask, and wait for the answer on the connection.

        A turn that outruns the server's synchronous window raises
        :class:`~argentum.errors.WorkInProgressError` — **not** a failure. The
        turn is still running and still being billed; the error carries
        ``thread_id``, and :meth:`attach` is where you go with it. Asking again
        would pay for the answer twice.
        """
        return self._client.request_json(
            "POST",
            "/v1/chat",
            json=_chat_body(message, user_ref, thread_id, agent_id),
            idempotency_key=idempotency_key,
        )

    def stream(
        self,
        message: str,
        *,
        user_ref: Optional[str] = None,
        thread_id: Optional[str] = None,
        agent_id: Optional[str] = None,
        last_event_id: Optional[str] = None,
        idempotency_key: Optional[str] = None,
    ) -> Iterator[Event]:
        """Ask, and read the answer as it is written.

        ::

            for ev in client.chat.stream("Revenue last month?", user_ref="u_42"):
                if ev.event == "delta":
                    print(ev.data["content"], end="", flush=True)
        """
        return self._client.events(
            "POST",
            "/v1/chat",
            json=_chat_body(message, user_ref, thread_id, agent_id),
            headers={"Last-Event-ID": last_event_id} if last_event_id else None,
            idempotency_key=idempotency_key,
        )

    def attach(self, thread_id: str, *, last_event_id: Optional[str] = None) -> Iterator[Event]:
        """Attach to a thread's newest turn — the resume door.

        This is where a :class:`~argentum.errors.WorkInProgressError` sends you,
        and where a stream that lost its connection reconnects with
        ``last_event_id``. If the turn has already answered you get the answer
        and the stream closes.
        """
        return self._client.events(
            "GET",
            f"/v1/threads/{thread_id}/events",
            headers={"Last-Event-ID": last_event_id} if last_event_id else None,
        )


class Threads:
    """The conversations this integration started."""

    def __init__(self, client: Argentum) -> None:
        self._client = client

    def list(
        self, *, limit: Optional[int] = None, cursor: Optional[str] = None, user_ref: Optional[str] = None
    ) -> t.ThreadPage:
        return self._client.request_json(
            "GET", "/v1/threads", params={"limit": limit, "cursor": cursor, "user_ref": user_ref}
        )

    def iter(self, **kwargs: Any) -> Iterator[t.Thread]:
        cursor = kwargs.pop("cursor", None)
        while True:
            page = self.list(cursor=cursor, **kwargs)
            for thread in page.get("data", []):
                yield thread
            cursor = page.get("next_cursor")
            if not page.get("has_more") or not cursor:
                return

    def get(self, thread_id: str) -> t.Thread:
        return self._client.request_json("GET", f"/v1/threads/{thread_id}")

    def messages(
        self, thread_id: str, *, limit: Optional[int] = None, cursor: Optional[str] = None
    ) -> t.MessagePage:
        """One page of a transcript, oldest first."""
        return self._client.request_json(
            "GET", f"/v1/threads/{thread_id}/messages", params={"limit": limit, "cursor": cursor}
        )

    def iter_messages(self, thread_id: str, **kwargs: Any) -> Iterator[t.Message]:
        cursor = kwargs.pop("cursor", None)
        while True:
            page = self.messages(thread_id, cursor=cursor, **kwargs)
            for message in page.get("data", []):
                yield message
            cursor = page.get("next_cursor")
            if not page.get("has_more") or not cursor:
                return

    def delete(self, thread_id: str) -> None:
        """Delete a conversation. Needs ``write:chat`` — destroying one is not a read."""
        self._client.request("DELETE", f"/v1/threads/{thread_id}")


def _chat_body(
    message: str,
    user_ref: Optional[str],
    thread_id: Optional[str],
    agent_id: Optional[str] = None,
) -> Dict[str, Union[str, None]]:
    body: Dict[str, Any] = {"message": message}
    if user_ref:
        body["user_ref"] = user_ref
    if thread_id:
        body["thread_id"] = thread_id
    if agent_id:
        body["agent_id"] = agent_id
    return body


def _rfc3339(value: "Optional[Union[str, datetime]]") -> Optional[str]:
    """Renders a window bound. RFC3339 is the only form the API accepts, and a
    naive datetime is treated as UTC — the API's own default window is UTC, so
    guessing the local zone would silently shift the period."""
    if value is None:
        return None
    if isinstance(value, datetime):
        if value.tzinfo is None:
            value = value.replace(tzinfo=timezone.utc)
        return value.astimezone(timezone.utc).isoformat().replace("+00:00", "Z")
    return value
