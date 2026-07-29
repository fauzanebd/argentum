# `@argentum/sdk`

The Argentum API from Node. No runtime dependencies.

```bash
npm install @argentum/sdk
export ARGENTUM_API_KEY=arg_…
export ARGENTUM_BASE_URL=https://argentum.example.com   # defaults to http://localhost:8080
```

```ts
import { writeFile } from 'node:fs/promises';
import { Argentum } from '@argentum/sdk';

const client = new Argentum();

// A spec in, a PDF out. No LLM, sub-second.
const pdf = await client.reports.render({
  spec_version: 2,
  format: 'pdf',
  title: 'Q3 Revenue',
  content: {
    sections: [
      { type: 'heading', text: 'Revenue by month' },
      {
        type: 'table',
        columns: [{ label: 'Month' }, { label: 'Revenue', fmt: 'currency' }],
        rows: [['July', 128_400], ['August', 141_900]],
      },
    ],
  },
});
await writeFile('q3.pdf', pdf);
```

A prompt instead of a spec, when you want the agent to do the analysis:

```ts
const job = await client.reports.create({ prompt: 'Monthly revenue for 2024 with a bar chart.', user_ref: 'u_42' });
await writeFile('2024.pdf', await job.download());
```

A question, streamed:

```ts
for await (const ev of client.chat.stream({ message: 'What were sales last month?', user_ref: 'u_42' })) {
  if (ev.event === 'delta') process.stdout.write(ev.data.content);
  if (ev.event === 'final') console.log('\n$', ev.data.usage?.cost_usd);
}
```

## What the client does for you

- **Retries** 429 and 5xx with backoff, honouring `Retry-After`. It deliberately
  does **not** retry a 504 from `chat.send()`: that means the wait ran out while
  the turn kept running, so it throws `WorkInProgressError` carrying the thread
  id. Attach with `client.chat.attach(threadId)` rather than asking again —
  asking again pays for the answer twice.
- **`Idempotency-Key`** on every write, generated once per logical call and
  reused across that call's retries. Pass your own to make a retry after a lost
  response replay rather than re-run.
- **Typed errors** mirroring the API's envelope: `type`, `code`, `param`,
  `requestId`. `catch (e) { if (e instanceof PermissionError) … }`.
- **Cursor pagination**, followed for you: `documents.listAll()`,
  `chat.threads.listAll()`, `chat.threads.messagesAll(id)`.

## Types

`src/types.generated.ts` is generated from `apps/backend/openapi/v1.yaml` by
`pnpm generate`, and CI fails if the committed copy is stale. Do not edit it.
The ergonomics around it are hand-written; the wire shapes are not.

Full walkthrough: [`docs/api/quickstart.md`](../../docs/api/quickstart.md).
