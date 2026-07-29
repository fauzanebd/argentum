# Argentum API — ten minutes from an empty directory to a PDF

You need one thing to start: an API key. Everything below runs against a live
deployment and nothing below needs us.

> Every code block on this page is a file in [`examples/`](examples/), and CI
> runs those files against a real tenant — the deterministic ones on every push,
> the ones that spend LLM tokens nightly. A block that has drifted from the file
> it quotes is a red build. So if something here does not work, that is a bug in
> Argentum and not a typo in the docs.

## 0. A key

Dashboard → **Settings → API Keys → Create**. Pick the scopes you need; they are
fixed for the life of the key and cannot be widened later.

| Scope | Lets a key |
| ----- | ---------- |
| `write:reports` | Render a document, or ask an agent for one |
| `read:documents` | List generated documents, download them, poll a report |
| `write:chat` | Ask a question — the scope that spends tokens — and delete threads |
| `read:threads` | Read conversations and transcripts |

The secret is shown **once**. Put it somewhere your process can read:

```bash
export ARGENTUM_BASE_URL=https://argentum.example.com   # or http://localhost:8080
export ARGENTUM_API_KEY=arg_…
```

Then check it, before writing any code:

<!-- example: examples/curl/me.sh -->
```bash
curl -sS "$ARGENTUM_BASE_URL/v1/me" \
  -H "Authorization: Bearer $ARGENTUM_API_KEY"
```

```json
{
  "api_version": "2026-07-28",
  "company": { "id": "…", "name": "Acme" },
  "key": { "id": "…", "name": "quickstart", "scopes": ["read:documents", "write:reports"] },
  "rate_limit": { "requests_per_minute": 120 },
  "credits": { "enforced": true, "byo_llm": false, "status": "ok", "balance_usd": 24.99, "remaining_pct": 99.9 }
}
```

`GET /v1/me` needs no scope, so it answers even for a key that can do nothing
else. It is also the paste that makes a support question answerable — it says
which contract version you are on, what your key may do, and what the tenant has
left to spend.

The full contract is at `GET /v1/openapi.json`, and that one needs no key at
all. Point a generator at it.

## 1. A PDF, from a spec (curl)

The render door takes a document description and returns a file. No LLM, no
conversation, sub-second — this is the one to reach for when your application
already knows what the report should say.

Save this as `spec.json`:

<!-- example: examples/spec.json -->
```json
{
  "spec_version": 2,
  "format": "pdf",
  "filename": "revenue.pdf",
  "title": "Revenue by month",
  "locale": "en",
  "currency": "USD",
  "content": {
    "sections": [
      {
        "type": "cover",
        "subtitle": "Prepared from the quickstart",
        "period": "Q3",
        "confidentiality": "Internal"
      },
      { "type": "heading", "text": "Revenue by month", "level": 1 },
      {
        "type": "kpi_row",
        "items": [
          { "label": "Total", "value": { "v": 270300, "fmt": "currency" }, "delta_pct": 10.5 },
          { "label": "Best month", "value": { "v": "August" } }
        ]
      },
      {
        "type": "table",
        "columns": [{ "label": "Month" }, { "label": "Revenue", "fmt": "currency" }],
        "rows": [
          ["July", 128400],
          ["August", 141900]
        ],
        "total_row": [{ "v": "Total" }, { "v": 270300, "fmt": "currency" }]
      },
      {
        "type": "chart",
        "caption": "Revenue by month",
        "chart": {
          "type": "bar",
          "labels": ["July", "August"],
          "series": [{ "name": "Revenue", "values": [128400, 141900] }],
          "fmt": "currency"
        }
      }
    ]
  }
}
```

and send it:

<!-- example: examples/curl/render.sh -->
```bash
curl -sS -X POST "$ARGENTUM_BASE_URL/v1/reports/render" \
  -H "Authorization: Bearer $ARGENTUM_API_KEY" \
  -H "Content-Type: application/json" \
  -H "Accept: application/pdf" \
  -H "Idempotency-Key: $(uuidgen)" \
  --data-binary @spec.json \
  -D headers.txt \
  -o revenue.pdf
```

`open revenue.pdf`. It carries your tenant's logo and colours, because branding
is applied server-side rather than being something every caller has to send.

Three things in that command are the contract rather than decoration:

- **`Accept: application/pdf`** asks for the bytes. `Accept: application/json`
  returns the document object with a short-lived presigned URL instead — use
  that when you want to hand the link to a browser.
- **`Idempotency-Key`** is required on every write. Retry with the same key and
  you get the same document back rather than a second one; retry with a
  *different body* under the same key and you get a `409`, which is the API
  telling you your retry loop has a bug.
- **`-D headers.txt`** keeps `X-Request-Id`. Every response has one, including
  failures. It is the string to quote at us.

The documents you have produced are listable, and each one re-presigns on read:

<!-- example: examples/curl/documents.sh -->
```bash
curl -sS "$ARGENTUM_BASE_URL/v1/documents?limit=1" \
  -H "Authorization: Bearer $ARGENTUM_API_KEY"
```

Pass `next_cursor` back as `?cursor=` for the next page. There are no offsets —
rows arrive while you page, and an offset would show you the same row twice or
skip one.

## 2. The same thing in Node

```bash
mkdir revenue && cd revenue
npm init -y
npm install @argentum/sdk
```

Save `spec.json` from step 1 next to it, then `render.mjs`:

<!-- example: examples/node/render.mjs -->
```js
import { readFile, writeFile } from 'node:fs/promises';
import { Argentum } from '@argentum/sdk';

const client = new Argentum(); // reads ARGENTUM_API_KEY and ARGENTUM_BASE_URL

const me = await client.me();
console.log(`key "${me.key.name}" on ${me.company.name}, scopes: ${me.key.scopes.join(', ')}`);

const spec = JSON.parse(await readFile('spec.json', 'utf8'));
const pdf = await client.reports.render(spec);
await writeFile('revenue-node.pdf', pdf);

console.log(`wrote revenue-node.pdf (${pdf.length} bytes)`);
```

```bash
node render.mjs
```

The SDK generates the `Idempotency-Key`, retries a 429 or a 5xx with backoff on
the server's own `Retry-After`, and throws errors carrying the API's envelope.
It has no runtime dependencies.

## 3. The same thing in Python

```bash
mkdir revenue && cd revenue
python3 -m venv venv && . venv/bin/activate
pip install argentum
```

<!-- example: examples/python/render.py -->
```python
import json

from argentum import Argentum

client = Argentum()  # reads ARGENTUM_API_KEY and ARGENTUM_BASE_URL

me = client.me()
print(f"key \"{me['key']['name']}\" on {me['company']['name']}, scopes: {', '.join(me['key']['scopes'])}")

with open("spec.json") as f:
    spec = json.load(f)

pdf = client.reports.render(spec)
with open("revenue-python.pdf", "wb") as f:
    f.write(pdf)

print(f"wrote revenue-python.pdf ({len(pdf)} bytes)")
```

```bash
python render.py
```

`argentum.aio.AsyncArgentum` is the same client with `await` in front of every
method.

**That is the ten minutes.** Everything below is the other door.

## 4. When you do not know what the report should say

`POST /v1/reports` takes a prompt instead of a spec, runs a real agent turn
against the tenant's own data, and produces a document at the end of it. Seconds
to minutes, priced in tokens rather than as a render.

<!-- example: examples/curl/report.sh -->
```bash
curl -sS -X POST "$ARGENTUM_BASE_URL/v1/reports" \
  -H "Authorization: Bearer $ARGENTUM_API_KEY" \
  -H "Content-Type: application/json" \
  -H "Idempotency-Key: $(uuidgen)" \
  -d '{"prompt":"Total revenue by month for 2024, with a bar chart.","format":"pdf","user_ref":"quickstart"}'
```

That returns `202` with a report id. Three ways to collect the result, and you
pick one:

1. **Poll** `GET /v1/reports/{id}` until `status` is `completed`.
2. **Stream** `GET /v1/reports/{id}/events` for progress.
3. **Callback**: send `callback_url` and verify the signature. `GET /v1/me`
   returns the signing secret and how to check it, for a key that can cause one.

In Node the poller is the return value:

<!-- example: examples/node/report.mjs -->
```js
import { writeFile } from 'node:fs/promises';
import { Argentum } from '@argentum/sdk';

const client = new Argentum();

const job = await client.reports.create({
  prompt: 'Total revenue by month for 2024, with a bar chart.',
  format: 'pdf',
  user_ref: 'quickstart',
});
console.log(`report ${job.id} is ${job.status}`);

// Progress while the agent works. Skip this and call job.download() if all you
// want is the file — it polls on its own.
for await (const ev of job.stream()) {
  if (ev.event === 'progress') console.log(`  ${ev.data.type}${ev.data.tool ? ' ' + ev.data.tool : ''}`);
  if (ev.event === 'report') console.log(`  ${ev.data.status}`);
}

const pdf = await job.download();
await writeFile('agentic-node.pdf', pdf);
console.log(`wrote agentic-node.pdf (${pdf.length} bytes)`);
```

and in Python:

<!-- example: examples/python/report.py -->
```python
from argentum import Argentum

client = Argentum()

job = client.reports.create(
    "Total revenue by month for 2024, with a bar chart.",
    format="pdf",
    user_ref="quickstart",
)
print(f"report {job.id} is {job.status}")

# Progress while the agent works. Skip this and call job.download() if all you
# want is the file — it polls on its own.
for ev in job.stream():
    if ev.event == "progress":
        print("  " + ev.data["type"] + (" " + ev.data["tool"] if ev.data.get("tool") else ""))
    if ev.event == "report":
        print("  " + ev.data["status"])

pdf = job.download()
with open("agentic-python.pdf", "wb") as f:
    f.write(pdf)

print(f"wrote agentic-python.pdf ({len(pdf)} bytes)")
```

`user_ref` is your own identifier for the person the report is for. It keys the
conversation and makes the spend attributable, which is what lets a tenant
police their own integration.

## 5. A question, not a document

<!-- example: examples/curl/chat.sh -->
```bash
curl -sSN -X POST "$ARGENTUM_BASE_URL/v1/chat" \
  -H "Authorization: Bearer $ARGENTUM_API_KEY" \
  -H "Content-Type: application/json" \
  -H "Accept: text/event-stream" \
  -H "Idempotency-Key: $(uuidgen)" \
  -d '{"message":"What was total revenue in December 2024?","user_ref":"quickstart"}'
```

```
event: started
data: {"thread_id":"…","run_id":"…","at":"2026-07-28T16:29:41Z"}

event: tool_call
data: {"tool":"run_sql"}

event: delta
data: {"content":"Total revenue in December 2024 was "}

id: MTc4NTI1MTc5MjI0NzY4Mzo3…
event: final
data: {"object":"turn","thread_id":"…","message":{…},"usage":{"tokens_in":5232,"tokens_out":579,"cost_usd":0.0016}}
```

`Accept: application/json` instead blocks until the turn answers and returns the
same `turn` object as one response.

<!-- example: examples/node/chat.mjs -->
```js
import { Argentum } from '@argentum/sdk';

const client = new Argentum();

for await (const ev of client.chat.stream({
  message: 'What was total revenue in December 2024?',
  user_ref: 'quickstart',
})) {
  if (ev.event === 'delta') process.stdout.write(ev.data.content);
  if (ev.event === 'tool_call') process.stderr.write(`\n[${ev.data.tool}]\n`);
  if (ev.event === 'final') {
    console.log(`\n\nthread ${ev.data.thread_id}`);
    if (ev.data.usage) console.log(`cost $${ev.data.usage.cost_usd.toFixed(6)}`);
  }
}
```

<!-- example: examples/python/chat.py -->
```python
import sys

from argentum import Argentum

client = Argentum()

for ev in client.chat.stream("What was total revenue in December 2024?", user_ref="quickstart"):
    if ev.event == "delta":
        print(ev.data["content"], end="", flush=True)
    elif ev.event == "tool_call":
        print(f"\n[{ev.data.get('tool')}]", file=sys.stderr)
    elif ev.event == "final":
        print(f"\n\nthread {ev.data['thread_id']}")
        usage = ev.data.get("usage")
        if usage:
            print(f"cost ${usage['cost_usd']:.6f}")
```

Continue a conversation by passing the `thread_id` back. Read the transcript
with `GET /v1/threads/{id}/messages`.

## The five things worth knowing before you go to production

### A 504 from `POST /v1/chat` does not mean the turn failed

It means the **wait** ran out. The turn is still running and still being billed,
and the response carries `in_flight.thread_id`. Attach to
`GET /v1/threads/{id}/events` instead of asking again — asking again pays for
the same answer twice.

Both SDKs raise a distinct error for it (`WorkInProgressError`) carrying the
thread id, and deliberately do not retry it.

### Retries are safe if you keep the key

An `Idempotency-Key` is per *logical* request, not per attempt. Reuse it across
your own retries and a duplicate arrives as a replay (`Idempotent-Replay: true`)
or, if the original is still running, as a `409 request_in_flight` carrying the
ids of the work you are already waiting on. Both SDKs do this for you.

A request that *fails* forgets its key, so a retry after a 500 is a fresh
attempt rather than a replay of a failure.

### Errors are typed

```json
{ "error": { "type": "permission", "code": "insufficient_scope",
             "message": "This key does not have the `write:reports` scope…",
             "request_id": "req_1945841125adc2c7bb618224f5fa88fd" } }
```

Switch on `type` — the vocabulary is closed: `invalid_request`,
`authentication`, `permission`, `not_found`, `rate_limit`, `budget_exhausted`,
`server`. `code` is the specific reason and new ones are added over time, so
treat an unknown one as its type. `param` names the offending field when there
is one.

### Rate limits arrive before you hit them

`RateLimit-Limit`, `RateLimit-Remaining` and `RateLimit-Reset` are on every
response, not just on a refusal. A 429 also carries `Retry-After`, computed from
your bucket rather than guessed; both SDKs honour it.

### The contract is additive

Fields are added; they are not renamed or removed. Ignore unknown fields and
unknown SSE event names and you will not be broken by a release. If we ever need
to break something, it will be `/v2`.

---

**Reference.** [`GET /v1/openapi.json`](../../apps/backend/openapi/v1.yaml) is
the full spec — every route, every field, every error. A Postman collection
generated from it is in
[`apps/backend/docs/postman/`](../../apps/backend/docs/postman/).
