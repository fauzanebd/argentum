"""Errors, mirroring the API's envelope.

Every failure under ``/v1`` arrives as ``{"error": {type, code, message, param,
request_id}}``. These classes are that envelope, so ``except`` gives you the
same three things a ``curl`` would have shown you: the class, the specific
reason, and the id to quote at us.
"""

from __future__ import annotations

from typing import Any, Dict, Optional


class ArgentumError(Exception):
    """The base of every failure this SDK raises."""

    def __init__(
        self,
        message: str,
        *,
        type: str = "server",
        code: str = "unknown",
        status: int = 0,
        param: Optional[str] = None,
        request_id: Optional[str] = None,
        retry_after: Optional[float] = None,
    ) -> None:
        super().__init__(message)
        self.message = message
        #: The coarse class. Switch on this.
        self.type = type
        #: The specific reason within that class — ``insufficient_scope``, ``spec_too_large``.
        self.code = code
        #: HTTP status, or 0 when the request never reached a response.
        self.status = status
        #: The field or header that was wrong, when the API named one.
        self.param = param
        #: Quote this in a support conversation; it is the ``X-Request-Id`` header.
        self.request_id = request_id
        #: Seconds the API asked us to wait, from ``Retry-After``.
        self.retry_after = retry_after

    def __str__(self) -> str:
        bits = [self.message]
        if self.code and self.code != "unknown":
            bits.append(f"[{self.type}/{self.code}]")
        if self.request_id:
            bits.append(f"request_id={self.request_id}")
        return " ".join(bits)


class InvalidRequestError(ArgentumError):
    """400 — the request was malformed. ``param`` names the field when there is one."""


class AuthenticationError(ArgentumError):
    """401 — no key, or one that is not usable."""


class PermissionError_(ArgentumError):
    """403 — a valid key without the scope this route needs.

    The trailing underscore keeps Python's own ``PermissionError`` reachable in
    a module that imports both; ``PermissionDenied`` is exported as an alias for
    anyone who would rather not look at it.
    """


PermissionDenied = PermissionError_


class NotFoundError(ArgentumError):
    """404 — no such resource *for this company*."""


class RateLimitError(ArgentumError):
    """429 — too many requests on this key. The client retries these for you."""


class BudgetExhaustedError(ArgentumError):
    """402 — the tenant is out of credit. Never retried: retrying it is a billing loop."""


class ServerError(ArgentumError):
    """5xx — our fault."""


class IdempotencyConflictError(ArgentumError):
    """409 ``idempotency_key_reuse`` — the same key arrived with a different body."""


class WorkInProgressError(ArgentumError):
    """The work is still running, and this response says where to find it.

    Two responses land here: the ``409 request_in_flight`` a retry gets while
    the original is still going, and the ``504`` the synchronous chat door
    answers when a turn outruns the wait. Neither is collectable by asking
    again — that is what would pay for the turn twice.
    """

    def __init__(self, message: str, *, in_flight: Optional[Dict[str, Any]] = None, **kwargs: Any) -> None:
        super().__init__(message, **kwargs)
        self.in_flight = in_flight or {}

    @property
    def thread_id(self) -> Optional[str]:
        """The thread to attach to with ``client.chat.attach(thread_id)``."""
        value = self.in_flight.get("thread_id")
        return value if isinstance(value, str) else None

    @property
    def report_id(self) -> Optional[str]:
        """The report to poll with ``client.reports.get(id)``."""
        value = self.in_flight.get("report_id")
        return value if isinstance(value, str) else None


class TransportError(ArgentumError):
    """The request never reached a response: DNS, connection, TLS, timeout."""


_BY_TYPE = {
    "invalid_request": InvalidRequestError,
    "authentication": AuthenticationError,
    "permission": PermissionError_,
    "not_found": NotFoundError,
    "rate_limit": RateLimitError,
    "budget_exhausted": BudgetExhaustedError,
    "server": ServerError,
}


def error_from_body(
    status: int,
    body: Any,
    request_id: Optional[str] = None,
    retry_after: Optional[float] = None,
) -> ArgentumError:
    """Builds the error for one failed response.

    Two codes are dispatched on before the type is, because their ``type`` is
    not what a caller needs to branch on: ``request_in_flight`` is typed
    ``invalid_request`` and ``turn_in_progress`` is typed ``server``, but both
    mean the same thing — the work exists, go and collect it.
    """
    detail = body.get("error") if isinstance(body, dict) else None
    detail = detail if isinstance(detail, dict) else {}
    type_ = detail.get("type", "server")
    code = detail.get("code", "unknown")
    message = detail.get("message", f"Argentum answered {status} with no error envelope.")
    kwargs = dict(
        type=type_,
        code=code,
        status=status,
        param=detail.get("param"),
        request_id=detail.get("request_id") or request_id,
        retry_after=retry_after,
    )

    if code in ("request_in_flight", "turn_in_progress"):
        in_flight = body.get("in_flight") if isinstance(body, dict) else None
        return WorkInProgressError(message, in_flight=in_flight, **kwargs)
    if code == "idempotency_key_reuse":
        return IdempotencyConflictError(message, **kwargs)
    return _BY_TYPE.get(type_, ServerError)(message, **kwargs)


__all__ = [
    "ArgentumError",
    "InvalidRequestError",
    "AuthenticationError",
    "PermissionError_",
    "PermissionDenied",
    "NotFoundError",
    "RateLimitError",
    "BudgetExhaustedError",
    "ServerError",
    "IdempotencyConflictError",
    "WorkInProgressError",
    "TransportError",
    "error_from_body",
]
