# OpenRouter provider routing

**Built 2026-09-11** (`T-Q17`). What `internal/llmroute` sends, why the deny-list
exists, and how to re-measure it.

Source: `apps/backend/internal/llmroute/transport.go`,
`apps/backend/internal/guardrails/toolcall_leak.go`.

---

## 1. The turn that stopped

A dashboard user asked, in Indonesian, *"member dengan pembelanjaan paling
banyak bulan ini"* — who spent the most this month. What came back was the
model's reasoning:

> The user is asking in Indonesian... I need to check if there's a defined
> metric for this. Let me call `list_metrics` first... Let me call
> `list_metrics` first, and also `get_schema` with keywords related to
> purchases, transactions, orders,
> members.`functions.list_metrics:0{}functions.get_schema:1{"source_id":"785c6d43-…","keywords":["transaction","purchase",…]}`

Then nothing. 7.65 seconds, no error, no spinner left running, a thumbs-up and a
copy button under a paragraph that stops mid-thought. **No tool ever ran.**

The tail of that message is Kimi K2's *native* tool-call syntax — a name, the
call's index, its JSON arguments — sitting in the assistant's text where only
structured `tool_calls` belong. The model decided correctly and said so in the
only format it knows; the endpoint serving it never translated that decision
into the OpenAI wire format.

Downstream, agent-sdk-go does exactly what it should with an assistant message
carrying no tool calls (`pkg/llm/openai/streaming.go:561`):

```go
if len(assistantResponse.ToolCalls) == 0 {
    // No tool calls, we're done
    gotCompleteResponse = true
    break // Exit the iteration loop
}
```

One iteration, loop over, and `ChatRunner.runStream` returns the accumulated
content — the reasoning — to be persisted and published as the answer.

## 2. It was one endpoint out of twenty-one

`LLM_MODEL=moonshotai/kimi-k2.6` is served by 21 OpenRouter endpoints across 20
providers. Seventeen were probed with the backend's own request shape — the same
two tools, the same system message, `stream: true` — each pinned by
`provider.order` with `allow_fallbacks: false`. Sixteen answered. Fireworks
returned a transport error and was not retried; StreamLake, Novita and Phala
were skipped as already deranked by OpenRouter (`status: -2`), so **four
endpoints in the pool remain unmeasured.**

| Provider | tool-call deltas | `finish_reason` | Verdict |
| --- | --- | --- | --- |
| **Decart** | **0** | **`stop`** | **native syntax in `content`** |
| Inceptron | 12 | `tool_calls` | ok |
| CoreWeave | 25 | `tool_calls` | ok |
| Crusoe | 32 | `tool_calls` | ok |
| DeepInfra | 10 | `tool_calls` | ok |
| Chutes | 32 | `tool_calls` | ok |
| Venice | 10 | `tool_calls` | ok |
| Parasail | 14 | `tool_calls` | ok |
| SiliconFlow | 32 | `tool_calls` | ok |
| GMICloud | 35 | `tool_calls` | ok |
| DigitalOcean | 34 | `tool_calls` | ok |
| Baidu | 18 | `tool_calls` | ok |
| AtlasCloud | 52 | `tool_calls` | ok |
| Cloudflare | 31 | `tool_calls` | ok |
| Moonshot AI | 54 | `tool_calls` | ok |
| BaseTen | 20 | `tool_calls` | ok |
| Fireworks | — | — | transport error, not retried |
| StreamLake, Novita, Phala | — | — | not probed (deranked upstream) |

Four things make this worth a deny-list rather than a shrug:

- **It is not a capability gap the API can be asked about.** Decart advertises
  `"tools"` and `"tool_choice"` in its `supported_parameters`, so OpenRouter's
  own `provider.require_parameters` preference does not filter it out. The
  advertisement is true and the behaviour is still wrong.
- **The blocking fallback does not rescue it.** Identical with `stream: false`,
  so `ChatRunner.Run`'s non-streaming path would have persisted the same
  paragraph.
- **Default routing picks it.** It is the cheapest completion price on the
  model. One of four unpinned probes landed there; the other three went to
  Baidu. That ratio is why this presented as an intermittent product bug rather
  than an outage — most turns were fine.
- **Nothing in the stack noticed.** No error event, no empty reply, no guardrail
  trip. The failure's entire visible surface was a user screenshotting an answer
  that stopped talking.

## 3. What was built

**`internal/llmroute` — routing.** The package that was `internal/llmzdr` now
owns the whole `provider` object in an OpenRouter request body, because that
object had never been about ZDR alone. It sends `provider.ignore: ["decart"]`
on every inference request, merged with — never replacing — any preferences the
caller already set.

It is installed **unconditionally**, in `llmclient.installUsageTap`, which is
the one function every LLM tier and every per-tenant credential override builds
its client through. ZDR stays opt-in because narrowing to ZDR endpoints is an
operator's judgement about their own model. The deny-list is not a judgement:
every slug on it has been measured to end a turn without answering, so opting
out of it is opting into the bug.

The two preferences **fail in opposite directions**, which is the one subtlety
in the file. ZDR fails closed — a body this layer cannot parse is a body it
cannot prove carries the flag, and sending it anyway defeats the point of
switching ZDR on. The deny-list fails open and logs, because it is in front of
every request in the product and a JSON hiccup must not become a total outage.
What gets through costs one turn, and that turn is caught by the second half:

**`guardrails.CheckToolCallLeak` — detection.** First in `ChatRunner`'s
post-turn chain, ahead of the fabrication gate, because a reply carrying a
leaked call is not an answer the agent got wrong — every gate below would be
judging text the agent never meant as prose.

It **replaces the reply rather than trimming the leak.** Stripping the call
would leave the reasoning, and the reasoning is not an answer: publishing it
hands the user a confident paragraph about work that never happened, which is
worse than the blank `CheckEmptyReply` exists to catch, because a blank is
obviously broken and this is not. The replacement says what did run, says the
fault is ours, and asks for the question again — a retry genuinely fixes it,
since routing is decided per request.

The pattern requires `functions.<name>:<digits>` followed by `{`. Without the
brace it is a word and a number, which an answer about a database could
legitimately contain. This guard replaces whole replies, so it is tuned to miss
rather than to over-catch; `<|tool_call_begin|>` is matched too, for a provider
that does not decode the special tokens at all.

Every trip writes a `toolcall_leak` audit row — its own name, not `final_answer`
and not `empty_reply`, so folding it in cannot corrupt the numbers those two
report — and a `toolcall_leak` count on the turn's completion line. The count
is the whole point of the return value: a deny-list is always one provider
behind, and the next one should be findable by a filter rather than by a
screenshot.

**Neither half is asked to be the only one.** The list keeps traffic off the
endpoint we know about; the guard names the endpoint we do not.

## 4. Re-measuring

The deny-list is a record of measurements, not of reputations. A slug leaves it
when somebody re-measures, not when a provider says it is fixed.

```bash
# Every endpoint serving the model, with what each claims to support.
curl -s https://openrouter.ai/api/v1/models/moonshotai/kimi-k2.6/endpoints |
  jq -r '.data.endpoints[] | "\(.provider_name)\t\(.supported_parameters | index("tools") != null)"'
```

To test one, POST `/api/v1/chat/completions` with a tool definition, a prompt
that forces a call, `"stream": true`, and the provider pinned:

```json
"provider": { "order": ["decart"], "allow_fallbacks": false }
```

A healthy endpoint streams `delta.tool_calls` and finishes `tool_calls`. A
broken one streams `delta.content` containing `functions.<name>:<n>{…}` and
finishes `stop`. Check both `stream` modes — they have agreed so far, and the
day they do not is worth knowing.

The deny-list itself was checked the same way on 2026-09-11: six unpinned
requests carrying `"provider": {"ignore": ["decart"]}`, all six routed to
Inceptron, all six returning structured tool calls. What that proves is the
preference, not the plumbing — one live turn through the deployed backend is
still owed, and is the only thing that can show the transport puts the field on
the body openai-go actually builds.

## 5. Not built

**Recovering the leaked call.** The name and the arguments are right there in
the text, and parsing them back into a tool call would turn a dead turn into a
live one. It was left alone deliberately: it means executing a tool call that no
structured field ever authorised, assembled by regex out of model prose, at the
exact moment the evidence says the provider is behaving unpredictably. The
`run_sql` path is behind `sqlguard` and the action path behind an approval, and
neither was designed to be reached from a string scraped out of a reply.

**Logging which provider served the turn.** The single most useful field this
guard could carry, and it cannot: OpenRouter returns `provider` in the response
body, and agent-sdk-go's typed openai-go client drops it before the runner sees
anything. Getting it means reading the response in `internal/llmusage`'s
transport, where the body is already being parsed for token counts, and
threading it onto the turn — worth doing, not worth blocking this on.
