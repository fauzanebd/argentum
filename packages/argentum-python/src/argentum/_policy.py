"""The parts of a request that must be identical in the sync and async clients.

Two transports are unavoidable — ``httpx.Client`` and ``httpx.AsyncClient`` have
the same API but not the same ``await`` — so the *policy* is here, as plain
functions, and the loops are the only thing written twice. Retry behaviour that
differed between the two clients would be the kind of bug nobody finds until a
production incident happens to be on the async path.
"""

from __future__ import annotations

import os
import random
import uuid
from email.utils import parsedate_to_datetime
from datetime import datetime, timezone
from typing import Any, Dict, Mapping, Optional, Tuple

DEFAULT_BASE_URL = "http://localhost:8080"


def resolve_credentials(api_key: Optional[str], base_url: Optional[str]) -> Tuple[str, str]:
    """Reads the key and origin from the arguments, then the environment."""
    key = api_key or os.environ.get("ARGENTUM_API_KEY", "")
    if not key:
        from .errors import AuthenticationError

        raise AuthenticationError(
            "No API key. Pass Argentum(api_key=...) or set ARGENTUM_API_KEY. "
            "Mint one in the dashboard under Settings -> API Keys.",
            type="authentication",
            code="missing_api_key",
        )
    origin = (base_url or os.environ.get("ARGENTUM_BASE_URL") or DEFAULT_BASE_URL).rstrip("/")
    return key, origin


def build_headers(
    api_key: str,
    *,
    accept: Optional[str],
    json_body: bool,
    method: str,
    idempotency_key: Optional[str],
    extra: Optional[Mapping[str, str]] = None,
) -> Dict[str, str]:
    """Assembles one request's headers.

    The idempotency key is minted **once per logical request**, by the caller,
    before any retry loop starts. A key generated per attempt would make every
    retry a new logical request, which is precisely the duplicate billing the
    header exists to prevent.
    """
    headers = {"Authorization": f"Bearer {api_key}"}
    if accept:
        headers["Accept"] = accept
    if json_body:
        headers["Content-Type"] = "application/json"
    if method not in ("GET", "DELETE"):
        headers["Idempotency-Key"] = idempotency_key or new_idempotency_key()
    if extra:
        headers.update(extra)
    return headers


def new_idempotency_key() -> str:
    return str(uuid.uuid4())


def is_retryable(status: int) -> bool:
    """Which failures are worth sending again.

    429 and 5xx, with two exclusions that matter more than the rule:

    * **504 is not retried.** On ``POST /v1/chat`` it does not mean the turn
      failed; it means the *wait* ran out while the turn keeps running and keeps
      being billed. Retrying under the same key gets a ``409
      request_in_flight``, and under a new one it would start a second turn. The
      caller gets a :class:`~argentum.errors.WorkInProgressError` carrying the
      thread id instead — attach to it.
    * **501 is not retried.** Nothing about sending it again implements the
      route.
    """
    if status == 429:
        return True
    if status in (501, 504):
        return False
    return status >= 500


def backoff_seconds(attempt: int, retry_after: Optional[float]) -> float:
    """Exponential backoff with full jitter, unless the server said when.

    ``Retry-After`` wins because the rate limiter computes it from the bucket's
    own state — it knows when a token actually appears. The jitter matters for
    the same reason the API floors ``Retry-After`` at one second: every refused
    client waking at the same instant is how a rate limit becomes a synchronised
    thundering herd.
    """
    if retry_after is not None:
        return max(0.0, retry_after)
    ceiling = min(8.0, 0.25 * (2**attempt))
    return round(ceiling * (0.5 + random.random() / 2), 3)


def parse_retry_after(value: Optional[str]) -> Optional[float]:
    if not value:
        return None
    try:
        return max(0.0, float(value))
    except ValueError:
        pass
    try:
        # The HTTP-date form. Rare from this API, valid per the RFC, and cheap
        # to accept rather than silently ignore.
        when = parsedate_to_datetime(value)
    except (TypeError, ValueError):
        return None
    if when is None:
        return None
    if when.tzinfo is None:
        when = when.replace(tzinfo=timezone.utc)
    return max(0.0, (when - datetime.now(timezone.utc)).total_seconds())


def clean_params(params: Mapping[str, Any]) -> Dict[str, Any]:
    """Drops the unset query parameters rather than sending them empty."""
    return {k: v for k, v in params.items() if v not in (None, "")}
