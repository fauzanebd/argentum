"""The asynchronous client.

Every method here is the one in :mod:`argentum.client` with ``await`` in front
of it, and that is on purpose: an integrator who wrote against the sync client
and later moved a service to asyncio should be changing keywords, not reading
documentation again.

The *policy* — which failures retry, how long the backoff is, how an envelope
becomes an exception, how SSE frames are assembled — lives in ``_policy`` and
``_sse`` and is shared with the sync client. Only the loops are written twice,
because ``httpx.Client`` and ``httpx.AsyncClient`` differ in exactly that.
"""

from __future__ import annotations

import asyncio
import time
from typing import Any, AsyncIterator, Dict, Mapping, Optional

import httpx

from . import types as t
from ._policy import (
    backoff_seconds,
    build_headers,
    clean_params,
    is_retryable,
    new_idempotency_key,
    resolve_credentials,
)
from ._sse import Event, _FrameAssembler
from .client import _chat_body, _describe_missing_document, _error_of
from .errors import ArgentumError, TransportError


class AsyncArgentum:
    """The Argentum client, for asyncio.

    ::

        async with AsyncArgentum() as client:
            pdf = await client.reports.render(spec)
    """

    def __init__(
        self,
        api_key: Optional[str] = None,
        base_url: Optional[str] = None,
        *,
        timeout: float = 60.0,
        max_retries: int = 2,
        http_client: Optional[httpx.AsyncClient] = None,
    ) -> None:
        self._api_key, self.base_url = resolve_credentials(api_key, base_url)
        self._timeout = timeout
        self._max_retries = max_retries
        self._owns_http = http_client is None
        self._http = http_client or httpx.AsyncClient(timeout=timeout)

        self.reports = AsyncReports(self)
        self.documents = AsyncDocuments(self)
        self.chat = AsyncChat(self)

    async def aclose(self) -> None:
        if self._owns_http:
            await self._http.aclose()

    async def __aenter__(self) -> "AsyncArgentum":
        return self

    async def __aexit__(self, *exc: Any) -> None:
        await self.aclose()

    async def me(self) -> t.Me:
        """Who this key is, what it can do, and what the tenant has left to spend."""
        return await self.request_json("GET", "/v1/me")

    # -- transport ---------------------------------------------------------

    async def request(
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
                response = await self._http.request(
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
                await asyncio.sleep(backoff_seconds(attempt, None))
                continue

            if response.is_success:
                return response

            last = _error_of(response)
            if attempt == self._max_retries or not is_retryable(response.status_code):
                raise last
            await asyncio.sleep(backoff_seconds(attempt, last.retry_after))

        raise last or TransportError("request failed", type="transport", code="transport_error")

    async def request_json(self, method: str, path: str, **kwargs: Any) -> Any:
        kwargs.setdefault("accept", "application/json")
        response = await self.request(method, path, **kwargs)
        if response.status_code == 204 or not response.content:
            return None
        return response.json()

    async def request_bytes(self, method: str, path: str, **kwargs: Any) -> bytes:
        response = await self.request(method, path, **kwargs)
        return response.content

    async def events(
        self,
        method: str,
        path: str,
        *,
        json: Any = None,
        params: Optional[Mapping[str, Any]] = None,
        headers: Optional[Mapping[str, str]] = None,
        idempotency_key: Optional[str] = None,
    ) -> AsyncIterator[Event]:
        """Opens a stream and yields its frames until a terminal one.

        Retries apply to the opening of the stream and nothing else — see the
        sync client for why.
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
            async with self._http.stream(
                method,
                self.base_url + path,
                params=clean_params(params or {}),
                json=json,
                headers=request_headers,
                timeout=httpx.Timeout(self._timeout, read=None),
            ) as response:
                if not response.is_success:
                    await response.aread()
                    error = _error_of(response)
                    if attempt == self._max_retries or not is_retryable(response.status_code):
                        raise error
                    await asyncio.sleep(backoff_seconds(attempt, error.retry_after))
                    continue

                assembler = _FrameAssembler()
                async for line in response.aiter_lines():
                    event = assembler.feed(line)
                    if event is None:
                        continue
                    yield event
                    if event.terminal:
                        return
                return


class AsyncReports:
    """The two report doors, asynchronously."""

    def __init__(self, client: AsyncArgentum) -> None:
        self._client = client

    async def render(self, spec: t.ReportSpec, *, idempotency_key: Optional[str] = None) -> bytes:
        """A spec in, the file's bytes out. Handles the replay and 202 cases too."""
        response = await self._client.request(
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
            return await self._client.documents.download(body["document"]["id"])
        if body.get("object") == "report":
            return await AsyncReportJob(self._client, body).download()
        raise ArgentumError(
            f"The render door answered JSON this client does not recognise: {str(body)[:200]}",
            code="unexpected_response",
            status=response.status_code,
        )

    async def render_document(self, spec: t.ReportSpec, *, idempotency_key: Optional[str] = None) -> t.Document:
        body = await self._client.request_json(
            "POST", "/v1/reports/render", json=spec, idempotency_key=idempotency_key
        )
        return body["document"]

    async def create(
        self,
        prompt: str,
        *,
        user_ref: Optional[str] = None,
        thread_id: Optional[str] = None,
        format: str = "pdf",
        callback_url: Optional[str] = None,
        locale: Optional[str] = None,
        currency: Optional[str] = None,
        idempotency_key: Optional[str] = None,
    ) -> "AsyncReportJob":
        body: Dict[str, Any] = {"prompt": prompt, "format": format}
        for key, value in (
            ("user_ref", user_ref),
            ("thread_id", thread_id),
            ("callback_url", callback_url),
            ("locale", locale),
            ("currency", currency),
        ):
            if value:
                body[key] = value
        report = await self._client.request_json("POST", "/v1/reports", json=body, idempotency_key=idempotency_key)
        return AsyncReportJob(self._client, report)

    async def get(self, report_id: str) -> t.Report:
        return await self._client.request_json("GET", f"/v1/reports/{report_id}")

    async def job(self, report_id: str) -> "AsyncReportJob":
        return AsyncReportJob(self._client, await self.get(report_id))

    def stream(self, report_id: str) -> AsyncIterator[Event]:
        return self._client.events("GET", f"/v1/reports/{report_id}/events")


class AsyncReportJob:
    """A report that is being worked on."""

    def __init__(self, client: AsyncArgentum, report: t.Report) -> None:
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

    async def refresh(self) -> t.Report:
        self.report = await self._client.reports.get(self.id)
        return self.report

    async def wait(self, *, poll_seconds: float = 2.0, timeout: float = 600.0) -> t.Report:
        deadline = time.monotonic() + timeout
        while not self.done:
            if time.monotonic() > deadline:
                raise ArgentumError(
                    f"Report {self.id} was still {self.status} after the client's timeout. "
                    f"It is still running — poll it with client.reports.get({self.id!r}).",
                    code="client_timeout",
                )
            await asyncio.sleep(poll_seconds)
            await self.refresh()
        return self.report

    async def stream(self) -> AsyncIterator[Event]:
        """Progress events, storing the terminal frame — see the sync client."""
        async for event in self._client.reports.stream(self.id):
            if event.event == "report":
                self.report = event.data
            yield event

    async def download(self, **wait_kwargs: Any) -> bytes:
        if not self.done:
            await self.wait(**wait_kwargs)
        document = self.report.get("document")
        if not document:
            raise ArgentumError(
                _describe_missing_document(self.report),
                code="report_failed" if self.status == "failed" else "report_no_document",
            )
        return await self._client.documents.download(document["id"])


class AsyncDocuments:
    """The files either report door produced."""

    def __init__(self, client: AsyncArgentum) -> None:
        self._client = client

    async def list(
        self,
        *,
        limit: Optional[int] = None,
        cursor: Optional[str] = None,
        format: Optional[str] = None,
        created_after: Optional[str] = None,
        created_before: Optional[str] = None,
    ) -> t.DocumentPage:
        return await self._client.request_json(
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

    async def iter(self, **kwargs: Any) -> AsyncIterator[t.Document]:
        cursor = kwargs.pop("cursor", None)
        while True:
            page = await self.list(cursor=cursor, **kwargs)
            for document in page.get("data", []):
                yield document
            cursor = page.get("next_cursor")
            if not page.get("has_more") or not cursor:
                return

    async def get(self, document_id: str) -> t.Document:
        return await self._client.request_json("GET", f"/v1/documents/{document_id}")

    async def download(self, document_id: str) -> bytes:
        return await self._client.request_bytes(
            "GET", f"/v1/documents/{document_id}/content", accept="application/octet-stream"
        )


class AsyncChat:
    """A question in, an answer out."""

    def __init__(self, client: AsyncArgentum) -> None:
        self._client = client
        self.threads = AsyncThreads(client)

    async def send(
        self,
        message: str,
        *,
        user_ref: Optional[str] = None,
        thread_id: Optional[str] = None,
        idempotency_key: Optional[str] = None,
    ) -> t.Turn:
        """Ask, and wait for the answer.

        A turn that outruns the server's synchronous window raises
        :class:`~argentum.errors.WorkInProgressError` carrying ``thread_id``.
        Attach to it; asking again pays twice.
        """
        return await self._client.request_json(
            "POST",
            "/v1/chat",
            json=_chat_body(message, user_ref, thread_id),
            idempotency_key=idempotency_key,
        )

    def stream(
        self,
        message: str,
        *,
        user_ref: Optional[str] = None,
        thread_id: Optional[str] = None,
        last_event_id: Optional[str] = None,
        idempotency_key: Optional[str] = None,
    ) -> AsyncIterator[Event]:
        """Ask, and read the answer as it is written.

        ::

            async for ev in client.chat.stream("Revenue last month?", user_ref="u_42"):
                if ev.event == "delta":
                    print(ev.data["content"], end="", flush=True)
        """
        return self._client.events(
            "POST",
            "/v1/chat",
            json=_chat_body(message, user_ref, thread_id),
            headers={"Last-Event-ID": last_event_id} if last_event_id else None,
            idempotency_key=idempotency_key,
        )

    def attach(self, thread_id: str, *, last_event_id: Optional[str] = None) -> AsyncIterator[Event]:
        """Attach to a thread's newest turn — the resume door."""
        return self._client.events(
            "GET",
            f"/v1/threads/{thread_id}/events",
            headers={"Last-Event-ID": last_event_id} if last_event_id else None,
        )


class AsyncThreads:
    """The conversations this integration started."""

    def __init__(self, client: AsyncArgentum) -> None:
        self._client = client

    async def list(
        self, *, limit: Optional[int] = None, cursor: Optional[str] = None, user_ref: Optional[str] = None
    ) -> t.ThreadPage:
        return await self._client.request_json(
            "GET", "/v1/threads", params={"limit": limit, "cursor": cursor, "user_ref": user_ref}
        )

    async def iter(self, **kwargs: Any) -> AsyncIterator[t.Thread]:
        cursor = kwargs.pop("cursor", None)
        while True:
            page = await self.list(cursor=cursor, **kwargs)
            for thread in page.get("data", []):
                yield thread
            cursor = page.get("next_cursor")
            if not page.get("has_more") or not cursor:
                return

    async def get(self, thread_id: str) -> t.Thread:
        return await self._client.request_json("GET", f"/v1/threads/{thread_id}")

    async def messages(
        self, thread_id: str, *, limit: Optional[int] = None, cursor: Optional[str] = None
    ) -> t.MessagePage:
        return await self._client.request_json(
            "GET", f"/v1/threads/{thread_id}/messages", params={"limit": limit, "cursor": cursor}
        )

    async def iter_messages(self, thread_id: str, **kwargs: Any) -> AsyncIterator[t.Message]:
        cursor = kwargs.pop("cursor", None)
        while True:
            page = await self.messages(thread_id, cursor=cursor, **kwargs)
            for message in page.get("data", []):
                yield message
            cursor = page.get("next_cursor")
            if not page.get("has_more") or not cursor:
                return

    async def delete(self, thread_id: str) -> None:
        await self._client.request("DELETE", f"/v1/threads/{thread_id}")
