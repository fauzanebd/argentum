"""A server-sent events reader, shared by both clients.

Written rather than depended on: SSE is a line-oriented format with four field
names, and pulling in a parser for it would double this package's dependency
count.

What it does that a naive ``split`` does not: it drops comments (``:
heartbeat``) silently, so the keepalive costs an integrator no code, and it
keeps the ``id:`` — which is what a caller sends back as ``Last-Event-ID`` to
resume a dropped stream.
"""

from __future__ import annotations

import json
from dataclasses import dataclass, field
from typing import Any, Dict, Iterable, List, Optional


@dataclass
class Event:
    """One SSE frame, with its payload already decoded.

    ``event`` is the discriminator: ``delta``, ``final``, ``progress``,
    ``report``, and so on. Treat an unknown one as ignorable — that is what lets
    the server add frames without breaking you.
    """

    event: str
    data: Dict[str, Any] = field(default_factory=dict)
    #: Present only on frames backed by something persisted — a `message` or a
    #: `final`. A delta has none, because it exists nowhere but the connection
    #: that carried it.
    id: Optional[str] = None

    @property
    def terminal(self) -> bool:
        """True for the frames after which the server closes the stream."""
        return self.event in ("final", "error", "report")


class _FrameAssembler:
    """Turns a sequence of lines into frames. Fed by whichever client is reading."""

    def __init__(self) -> None:
        self._lines: List[str] = []

    def feed(self, line: str) -> Optional[Event]:
        # httpx strips the newline, so a blank string is the frame boundary.
        if line != "":
            self._lines.append(line)
            return None
        raw, self._lines = self._lines, []
        return _parse(raw)


def _parse(lines: Iterable[str]) -> Optional[Event]:
    name = "message"
    ident: Optional[str] = None
    data: List[str] = []

    for line in lines:
        if not line or line.startswith(":"):
            continue  # a comment, i.e. the heartbeat
        field_name, _, value = line.partition(":")
        # One optional space after the colon is part of the format, not part of
        # the value — a JSON payload that lost its first character would fail to
        # parse for a reason nobody would find quickly.
        value = value[1:] if value.startswith(" ") else value
        if field_name == "event":
            name = value
        elif field_name == "data":
            data.append(value)
        elif field_name == "id":
            ident = value

    if not data:
        return None
    payload = "\n".join(data)
    try:
        decoded = json.loads(payload)
    except json.JSONDecodeError:
        decoded = {"raw": payload}
    return Event(event=name, data=decoded if isinstance(decoded, dict) else {"value": decoded}, id=ident)
