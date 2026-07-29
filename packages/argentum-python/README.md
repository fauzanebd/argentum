# `argentum`

The Argentum API from Python. One dependency (`httpx`), sync and async.

```bash
pip install argentum
export ARGENTUM_API_KEY=arg_…
export ARGENTUM_BASE_URL=https://argentum.example.com   # defaults to http://localhost:8080
```

```python
from argentum import Argentum

client = Argentum()

# A spec in, a PDF out. No LLM, sub-second.
pdf = client.reports.render({
    "spec_version": 2,
    "format": "pdf",
    "title": "Q3 Revenue",
    "content": {
        "sections": [
            {"type": "heading", "text": "Revenue by month"},
            {
                "type": "table",
                "columns": [{"label": "Month"}, {"label": "Revenue", "fmt": "currency"}],
                "rows": [["July", 128400], ["August", 141900]],
            },
        ]
    },
})
open("q3.pdf", "wb").write(pdf)
```

A prompt instead of a spec, when you want the agent to do the analysis:

```python
job = client.reports.create("Monthly revenue for 2024 with a bar chart.", user_ref="u_42")
open("2024.pdf", "wb").write(job.download())   # waits for the turn, then downloads
```

A question, streamed:

```python
for ev in client.chat.stream("What were sales last month?", user_ref="u_42"):
    if ev.event == "delta":
        print(ev.data["content"], end="", flush=True)
    elif ev.event == "final":
        print("\n$", ev.data.get("usage", {}).get("cost_usd"))
```

Async is the same client with `await` in front of it:

```python
from argentum.aio import AsyncArgentum

async with AsyncArgentum() as client:
    pdf = await client.reports.render(spec)
    async for ev in client.chat.stream("Revenue last month?", user_ref="u_42"):
        ...
```

## What the client does for you

- **Retries** 429 and 5xx with backoff, honouring `Retry-After`. It deliberately
  does **not** retry a 504 from `chat.send()`: that means the wait ran out while
  the turn kept running, so it raises `WorkInProgressError` carrying
  `thread_id`. Attach with `client.chat.attach(thread_id)` rather than asking
  again — asking again pays for the answer twice.
- **`Idempotency-Key`** on every write, generated once per logical call and
  reused across that call's retries. Pass your own to make a retry after a lost
  response replay rather than re-run.
- **Typed errors** mirroring the API's envelope: `.type`, `.code`, `.param`,
  `.request_id`.
- **Cursor pagination**, followed for you: `documents.iter()`,
  `chat.threads.iter()`, `chat.threads.iter_messages(id)`.

## Types

`argentum/types.py` is generated from `apps/backend/openapi/v1.yaml` by
`pnpm --filter @argentum/openapi-tools build`, and CI fails if the committed
copy is stale. Do not edit it.

Full walkthrough: [`docs/api/quickstart.md`](../../docs/api/quickstart.md).
