"""The Argentum API from Python.

::

    from argentum import Argentum

    client = Argentum()                                  # ARGENTUM_API_KEY, ARGENTUM_BASE_URL
    pdf = client.reports.render(spec)                    # bytes
    job = client.reports.create("Revenue for 2024", user_ref="u_42")
    pdf = job.download()                                 # waits, then downloads

    for ev in client.chat.stream("What were sales last month?", user_ref="u_42"):
        if ev.event == "delta":
            print(ev.data["content"], end="", flush=True)

The async client is the same object with ``await`` in front of it — see
:class:`argentum.aio.AsyncArgentum`.

The wire types in :mod:`argentum.types` are generated from
``apps/backend/openapi/v1.yaml``. The ergonomics around them are not.
"""

from ._sse import Event
from .client import Argentum, Chat, Documents, ReportJob, Reports, Threads
from .errors import (
    ArgentumError,
    AuthenticationError,
    BudgetExhaustedError,
    IdempotencyConflictError,
    InvalidRequestError,
    NotFoundError,
    PermissionDenied,
    RateLimitError,
    ServerError,
    TransportError,
    WorkInProgressError,
)
from .types import API_VERSION

__version__ = "0.1.0"

__all__ = [
    "API_VERSION",
    "Argentum",
    "ArgentumError",
    "AuthenticationError",
    "BudgetExhaustedError",
    "Chat",
    "Documents",
    "Event",
    "IdempotencyConflictError",
    "InvalidRequestError",
    "NotFoundError",
    "PermissionDenied",
    "RateLimitError",
    "ReportJob",
    "Reports",
    "ServerError",
    "Threads",
    "TransportError",
    "WorkInProgressError",
    "__version__",
]
