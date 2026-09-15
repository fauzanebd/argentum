# A repository as a source — what an agent can read in a tenant's GitHub, and what it costs

Written 2026-09-15 against `main` @ `9577cab`, from the owner's request:

> *"Research and plan in a roadmap a capability for our agents to use a GitHub
> repository as their data source: (1) read-only for now, later maybe change it
> and create a PR; (2) efficient in cost but effective; (3) tell me if we need
> other tools or technology. My goal is that this agent is like a human or an
> employee that can do many things."*

The plan it feeds is
[`../plan/13-repository-source-roadmap.md`](../plan/13-repository-source-roadmap.md).
The connection as designed is drawn on one page in
[`assets/09-github-connection.html`](assets/09-github-connection.html). It is a
picture of this document, not of anything built.

**Every repository claim below was read at `9577cab` and carries its file and
line.** Every GitHub claim carries the primary-source URL it was read from on
2026-09-15. **Nothing in this document was measured by running this product or
by calling GitHub.** The cost figures in §5 are arithmetic on measured inputs,
not measurements. **§5e's search figures were measured**, on 2026-09-15, on a
scratch Postgres on this machine rather than in production; the appendix has the
method. §9 lists what that leaves owed.

---

## 1. The finding that decides the scope: today's guardrail refuses the questions

A GitHub connector alone would not make the agent read code. The first thing
that would stop it is the topic rule, not a missing tool:

```yaml
# config/guardrails.yaml:119-122 — the require_analytics_topic classifier
FALSE: everything else. Two families are the ones people get wrong:
1. Teaching or debugging code — algorithms, syntax, frameworks, CSS or layout, git, terminals, CS homework …
```

It also has a deterministic block. `block_off_topic_programming_tutorial`
(`guardrails.yaml:134-162`) refuses any message matching `golang`,
`how do i (implement|declare|debug)`, `struct in go`, `time complexity`, and so
on. It does this before any classifier runs. So *"how does our Go checkout
service compute the member discount?"* is refused by a regex.

The guardrail file is one global config (`GUARDRAILS_CONFIG_PATH`,
`internal/config/config.go:717`). No tenant or agent can override it. The
persona reads *"You are Argentum, an expert data analyst helping business owners
understand their metrics"* (`internal/bootstrap/system_prompt.go:287`).

**The backlog's explicit-rejection table covers this.** It rejects *"removing
guardrail topic enforcement to widen the product"* because *"the narrow scope is
what makes the agent trustworthy and cheap. A general assistant competes with
ChatGPT; a trusted business-data agent does not"* ([`../plan/backlog.md`](../plan/backlog.md),
*Explicitly rejected*). The owner's goal is an employee who can do many things,
and that goal meets this row head-on.

**The two can be reconciled without reopening the rejection.** The classifier's
own test is not "is this analytics". It is:

> *"The test is whose data answers it. If answering means reading THIS
> organization's own business data … the answer is TRUE."* (`guardrails.yaml:117`)

A tenant's private repository is that organization's own data, as much as its
warehouse is. *"Teach me linked lists"* stays refused under that test, because
nothing of theirs answers it. *"What does `status = 7` mean in our orders
table?"* is answered by an enum in their code. So the change is a **per-agent
topic scope**, not a removed rule:

- An agent bound to a repository has the test widened by one clause.
- An agent with no repository is byte-identical to today.

The existing per-turn policy is the precedent for this shape. `guardrails.PIIMode`
is a tenant setting passed in on every turn (`internal/guardrails/guardrails.go:116`,
read at `internal/app/chat_runner.go:1499`). The input rules are already rebound
per turn at `internal/bootstrap/stack.go:961`.

This is **decision 1** of the roadmap, and the owner should confirm it. It is
the one place this plan touches something the backlog rejected.

---

## 2. What already exists

| Mechanism | Where | What it gives this feature |
| --- | --- | --- |
| Per-turn tools offered only when a condition holds | `offerRoomTools` (`internal/bootstrap/stack.go:1064`) | Repository tools can reach only turns whose agent is bound to a repository. Every other agent pays **zero** schema tokens |
| A per-agent binding where empty means *none* | `agent_mcp_servers` (`038`), `agentscope.Scope.MCPServerIDs` (`internal/agentscope/scope.go:57`) | The shape for `agent_repositories`. Code is a DSN-class object, so empty must not mean all |
| An encrypted-secret keyring with rotation | `crypto.DSNCipher.Encrypt`/`Decrypt` (`internal/crypto/keyring.go:184,217`) | Nothing new is needed for the webhook secret or any stored credential |
| An RS256-capable JWT library | `github.com/golang-jwt/jwt/v5` (`apps/backend/go.mod:18`) | A GitHub App JWT needs no new dependency |
| Verified inbound webhooks with redelivery dedupe | Slack: raw body → HMAC → `RedisDeduper` `SET NX` (`handlers/slack_webhook.go:57`, `slack/dedupe.go:53`); Meta's `X-Hub-Signature-256` (`handlers/webhook.go:84`) | GitHub signs deliveries in exactly the Meta scheme |
| Background jobs | asynq, e.g. `document:parse` (`internal/queue/tasks.go:57`) | `repository:sync` is one more task name |
| Text in the control plane with a lexical index | `document_chunks` + generated `tsvector` + GIN (`061_document_chunks.up.sql`) | Precedent for tenant text living in the control database, scoped by `company_id` |
| Audit, budget and fencing on every tool | `GuardAll` → `MarkUntrustedReadsAll` → `WithAuditAll` → `FenceResultsAll` (`stack.go:725-789`) | Repository tools inherit all four by being in the list |
| A turn budget | 8 iterations, 12 tool calls, 200k tokens, 150 s (`internal/agentbudget/budget.go:88-92`) | Repository exploration must fit here, and that is a design constraint (§5) |
| Taint that gates actions for untrusted text | `taint.KindDocument` gates, `KindData` fences only (`internal/taint/taint.go:45-54`) | Issue and PR bodies written by strangers need the gating kind (§6) |
| A write path with approval, idempotency and re-checks | `actions.Action`, `ActionService.ProposeAction` / `Approve` (`internal/app/action_service.go:98,287`); `mcp_call` re-checks at execute (`internal/actions/mcp_call.go:160`) | Opening a PR is one more action kind, not a second write path |
| Company erasure | `RetentionService.EraseCompanyData` (`internal/app/retention_service.go:200`) | Mirrored code is wired in from day one |

**What does not exist, and matters:**
- There is no `exec.Command` anywhere in `internal/` or `cmd/`, and no `git` binary.
- There is no code sandbox. `internal/compute` is a decimal evaluator, and `T-W4` is unbuilt (`internal/compute/compute.go:20-23`).
- There is no prompt caching on the production model. Caching is wired only for the Anthropic interface (`stack.go:948`), and production runs `moonshotai/kimi-k2.6` on OpenRouter ([`../coverage/provider-routing.md`](../coverage/provider-routing.md)).
- The worker is one replica with **512 Mi** of memory and an unbounded `/tmp` `emptyDir` (`helm/argentum/values.yaml:109-120`, `templates/deployment-worker.yaml:81`).

---

## 3. What a repository is good for *in this product*, ranked

The ranking is by how directly each use makes the existing product answer
better. The value of a general employee is kept separate.

1. **The code that explains the data.** dbt models, SQL migrations, enum
   definitions, ETL jobs, and the service that writes the `orders` table. Today
   the agent guesses what `status = 7` or `channel = 'B2B-X'` means from column
   names. The repository states it. This improves the core analytics answer
   rather than adding a surface, and it is where [`03-gap-analysis.md`](03-gap-analysis.md)'s
   *metric-definition gap* argument points: a dbt project *is* a tenant's metric
   layer, already written.
2. **Engineering activity as data.** Commits, merged pull requests, releases and
   issues over a window. *"Conversion fell on 10 September. What shipped that
   day?"* joins a metric this product already computes with a deploy it cannot
   see today. This is the cross-source question no BI tool answers.
3. **Runbooks and docs in the repository.** READMEs, `docs/` and ADRs, all prose.
   This overlaps `search_documents`, which is PDF-only today (`internal/app/document_ingest_service.go:184`).
4. **Code questions for engineers.** *"Where do we retry payment webhooks?"*
   This is the employee half of the goal. It is served by the same tools as uses
   1–3 and needs nothing more, but it is the use furthest from what the product
   is sold as.
5. **Changing the repository.** Editing a model, fixing a metric definition, or
   opening a PR. This is the write phase (§7).

The design serves 1–4 with one set of tools. Nothing below is built for 4
alone.

---

## 4. What GitHub offers, and the limits that shape the design

| Fact | Consequence | Source |
| --- | --- | --- |
| A **GitHub App** is GitHub's recommendation for long-lived integrations. The customer picks which repositories it sees; no seat or user is involved | A PAT belongs to a person and leaves with them. An OAuth App sees every repository the user can. Neither fits | [deciding when to build a GitHub App](https://docs.github.com/en/apps/creating-github-apps/about-creating-github-apps/deciding-when-to-build-a-github-app), [PATs](https://docs.github.com/en/authentication/keeping-your-account-and-data-secure/managing-your-personal-access-tokens) |
| An installation token lasts **1 hour** and can be **narrowed at mint time** to `repository_ids` (up to 500) and a `permissions` subset | Tokens are never stored. Each one is minted for exactly the repositories an agent is bound to | [installation access token](https://docs.github.com/en/rest/apps/apps#create-an-installation-access-token-for-an-app) |
| Since 2026-04-27 installation tokens are a stateless `ghs_…` format of about 520 characters | Nothing may assume a 40-character token | [changelog 2026-04-24](https://github.blog/changelog/2026-04-24-notice-about-upcoming-new-format-for-github-app-installation-tokens/) |
| The setup URL's `installation_id` **can be spoofed**. Verify it with a user token and `GET /user/installations` | Without that check, a tenant can bind another company's installation. This is the tenant-isolation test for the connection | [about the setup URL](https://docs.github.com/en/apps/creating-github-apps/registering-a-github-app/about-the-setup-url), [installations](https://docs.github.com/en/rest/apps/installations) |
| Adding a permission to an App needs **every installation owner** to approve it | Adding write to the read App later would prompt every tenant and stall those who don't click. The write phase is a **second App** | [choosing permissions](https://docs.github.com/en/apps/creating-github-apps/registering-a-github-app/choosing-permissions-for-a-github-app) |
| Rate limits: 5,000–12,500 requests/h per installation. Secondary: 100 concurrent, 900 points/min. **A `304` on a conditional request is free** | Fetching per file at turn time hits the secondary limit long before the hourly one. Poll with ETags | [REST rate limits](https://docs.github.com/en/rest/using-the-rest-api/rate-limits-for-the-rest-api), [best practices](https://docs.github.com/en/rest/using-the-rest-api/best-practices-for-using-the-rest-api) |
| REST code search is the **legacy engine**: default branch only, files under 384 KB, **no regex**, **10 requests/min**, 1,000 results. No public API for the new engine was found | It cannot be the agent's search. Ten per minute is shared by every turn of a tenant | [searching code (legacy)](https://docs.github.com/en/search-github/searching-on-github/searching-code), [search API](https://docs.github.com/en/rest/search/search) |
| `GET /repos/{o}/{r}/tarball/{ref}` returns one redirect to a whole-repository archive. The private link lives for 5 minutes | A snapshot is **one** API request, not thousands, and needs no `git` binary | [contents API](https://docs.github.com/en/rest/repos/contents) |
| Contents API: full support up to 1 MB, raw only up to 100 MB, directory listings cut at 1,000 entries. Trees API: 100,000 entries or 7 MB, then `truncated` | Reading a single file at another ref over the live API is fine. Walking a repository over it is not | [contents](https://docs.github.com/en/rest/repos/contents), [trees](https://docs.github.com/en/rest/git/trees) |
| Webhooks are **never retried automatically**. Timeout is 10 s, redelivery is possible for 3 days, nothing is sent over 25 MB, and pushes of more than 3 tags send nothing | A `push` webhook is a hint, never the source of truth. A reconciler compares the stored head SHA against GitHub | [failed deliveries](https://docs.github.com/en/webhooks/using-webhooks/handling-failed-webhook-deliveries), [payloads](https://docs.github.com/en/webhooks/webhook-events-and-payloads) |
| Compare: 250 commits and 300 files on page 1 | "What changed" for a big range reads per-commit, or diffs manifests | [commits](https://docs.github.com/en/rest/commits/commits) |
| `createCommitOnBranch` (GraphQL) writes a multi-file commit, **signed and Verified**, guarded by `expectedHeadOid` | The write phase needs no clone and no signing key | [GraphQL commits](https://docs.github.com/en/graphql/reference/commits), [changelog 2021-09-13](https://github.blog/changelog/2021-09-13-a-simpler-api-for-authoring-commits/) |
| GHES cannot install a github.com App, so an App is registered per instance. GHE.com uses another API host | The API base URL and App registration are per-deployment settings, and v1 is github.com only | [GHES apps](https://docs.github.com/en/apps/sharing-github-apps/making-your-github-app-available-for-github-enterprise-server), [GHE.com](https://docs.github.com/en/enterprise-cloud@latest/admin/data-residency/feature-overview-for-github-enterprise-cloud-with-data-residency) |

---

## 5. Four architectures, and what each costs per question

### 5a. The arithmetic every option lives inside

**Every iteration re-sends everything so far, uncached.** A tool result of *R*
tokens returned at iteration *i* of an *n*-iteration turn is billed
*(n − i)* more times. Production's price is **$0.95 per million input tokens and
$4.00 per million output tokens** (`internal/app/llm_pricing.go:64`). The fixed
floor was measured at **≈11,000 tokens** when the registry held 12 tools
([`../plan/07-agentic-skills-roadmap.md`](../plan/07-agentic-skills-roadmap.md) §2a).
It holds 16 now, so the floor is higher and has not been re-measured.

Three levers therefore decide cost. None of them is the GitHub bill, which is
zero:

- **Schema tokens on every iteration.** The fewer and smaller the repository
  tools, the better, and **zero** on agents without a repository.
- **Result size, multiplied by the iterations that follow it.** Return line
  ranges and matches, never whole files.
- **Iterations spent finding things.** One good search replaces four
  list-and-read round trips, which makes search quality a cost lever and not only
  a quality one.

### 5b. A — the tenant registers GitHub's own MCP server through `T-M1`

**This works today with no code**, except for the guardrail (§1). The tenant
registers `https://api.githubcopilot.com/mcp/` with a PAT, approves tools, and
binds the server. The toolset can be narrowed by URL path (`/mcp/x/{toolset}/readonly`,
[remote-server.md](https://github.com/github/github-mcp-server/blob/main/docs/remote-server.md)).
The write tools would even route through `T-M4`'s approval. **As a product it
fails on five counts:**

1. **A PAT is a person's credential.** App installation-token auth is documented
   only for the local stdio server ([github-app-auth.md](https://github.com/github/github-mcp-server/blob/main/docs/github-app-auth.md)),
   and our client bans stdio (`internal/domain/mcp_server.go:25-27`). So least
   privilege would depend on a person minting a narrow PAT and remembering to
   rotate it.
2. **Every approved tool's full schema rides every iteration**
   (`internal/tools/mcp/source.go:112-213`), with no caching on Kimi. GitHub's
   own figures: loading 3–10 tools instead of the full set cut context by
   **60–90%**, and consolidating the Projects tools alone saved **≈23,000
   tokens** ([changelog 2025-12-10](https://github.blog/changelog/2025-12-10-the-github-mcp-server-adds-support-for-tool-specific-configuration-and-more/),
   [changelog 2026-01-28](https://github.blog/changelog/2026-01-28-github-mcp-server-new-projects-tools-oauth-scope-filtering-and-new-features/)).
   *Illustration, not a measurement:* a bound set of 15,000 schema tokens over a
   4-iteration turn adds 60,000 input tokens, or **+$0.057 a question**. That
   roughly doubles §5d's figure.
3. **A result over 256 KB is an error, not a truncation** (`MCP_MAX_RESPONSE_BYTES`,
   `internal/adapters/mcp/client.go:219`). 256 KB is about 64,000 tokens, a third
   of the turn's token budget in one call. So a large file either fails or
   dominates the turn.
4. **GitHub rewording a tool description disables it silently until an admin
   re-approves** (drift, `source.go:158`). That is correct for an unknown server
   and a standing chore for this one.
5. **Search is GitHub's legacy code search** (§4): no regex, default branch
   only, 10 searches a minute.

**Verdict:** this is what to tell a tenant who wants something this week, and
it is not what to build.

### 5c. B — native tools calling the GitHub API live, per call

Cheapest to build: no storage and no sync. `list` walks the Trees API, `read`
uses the Contents API, and `search` is REST code search. **Search is the
problem.** Legacy search without regex or substrings misses what an analyst
asks (`status = 7`, `'B2B-X'`), and each miss costs an iteration, which is the
most expensive unit in §5a. At 10 searches a minute per tenant, two people
asking at once share one limit. Rejected as the design. It is kept for **one**
job: reading a file at a ref other than the synced one.

### 5d. C — a snapshot in the control plane, scanned · **recommended**

- **Sync.** A background `repository:sync` job streams the default branch's
  tarball (one API call), unpacks it as a stream, and upserts **text files
  only** into `repository_files` keyed by `(company_id, repository_id, path)`.
  It works within caps: per-file size, total size, file count, a skip list for
  binaries, vendored code and lockfiles, and admin include/exclude globs.
  Unchanged blobs are skipped by SHA, so a push rewrites only what changed.
  Memory stays constant, which the 512 Mi worker needs, and there is no `git` and
  no `exec`.
- **When it runs.** A `push` webhook enqueues a sync, deduped per repository. A
  reconciler polls the head with an ETag, which costs nothing when unchanged,
  because webhooks are not retried (§4).
- **Search runs in two stages.** Postgres finds candidate chunks through a
  trigram index scoped by repository, under a statement timeout. Go's RE2 then
  finds the exact lines in those few candidates and returns `path:line` with two
  lines of context. The layout comes from §5e's measurement, not preference:
  text is stored as 200-line chunks, because whole files and an unindexed scan
  both measured badly.
- **Read is a row fetch** returning a line range with line numbers.
- **Every result carries `commit_sha` and `synced_at`.** An answer can cite
  `path#L10-L20@abc1234` as a link the user can open. The *"as of"* line is the
  same idea [`07-feature-candidates.md`](07-feature-candidates.md) §1a argued for
  warehouse data.

**Why Postgres and not object storage.** Three reasons:
- `generate_document`'s dependency on object storage is optional per deployment
  (`registry.go:225`). The Helm chart sets no storage variables at all, so
  whether production has object storage is **not verifiable from this tree**.
- A row per file makes sync incremental, and erasure becomes a cascade.
- The search index (§5e) sits on the same rows, so there is no second store to
  keep in step.

The price is control-plane size and control-plane CPU, which is why the caps are
decisions and not tuning: **50 MB of text per repository and 200 MB per
company.** Measured, chunks plus their index take about 0.9× the raw text (§5e).

**Cost, illustrated on the same 4-iteration turn** (≈1,000 schema tokens for
three tools, estimated, measured by `T-J7`):

| Iteration | Carries | Input tokens |
| --- | --- | ---: |
| 1 | floor + schemas + repo map | 12,300 |
| 2 | + a search result (1,500) | 13,800 |
| 3 | + a 120-line range (3,000) | 16,800 |
| 4 | + another range (3,000) | 19,800 |
| | **input 62,700 → $0.060; output ~1,500 → $0.006** | **≈ $0.066** |

The same question answered by reading two whole 60 KB files instead of two
ranges carries ≈98,700 input tokens: **≈ $0.10, about 1.5×**. A single 256 KB
file (option A's cap) cannot fit alongside the rest of the turn at all.

### 5e. Measured 2026-09-15: the index is not later

The first version of this section said a scan was enough until its latency said
otherwise, and deferred the index to `T-J8`. A benchmark on a scratch Postgres 16
on this machine (appendix) overturned that, for a reason latency alone would not
have shown.

Milliseconds, median of three, one core. Each cell is *57 MB / 186 MB* of text in
one repository, sharing a table with two other repositories (257 MB in all):

| How the search runs | literal | rare regex | case-insensitive | no match |
| --- | ---: | ---: | ---: | ---: |
| Whole files, no index | 2,265 / 2,249 | 2,345 / 2,536 | 2,321 / 2,306 | 2,338 / 2,277 |
| Whole files, `strpos`, no index | 163 / 545 | — | — | 153 / 484 |
| Whole files, trigram index on `content` alone | 569 / 570 | 2,118 / 2,092 | 1,170 / 1,191 | 3 / 2 |
| Whole files, index on `(repository_id, content)` | 130 / 413 | 453 / 1,468 | 260 / 835 | 2 / 3 |
| **200-line chunks, index on `(repository_id, content)`** | **47 / 137** | **387 / 1,275** | **184 / 563** | **2 / 4** |
| Go RE2 in memory, for comparison | 47 / 154 | 47 / 166 | 1,890 / 6,364 | 7 / 21 |

What it shows:

1. **Without an index, a search costs the whole table, not the repository.**
   57 MB and 186 MB both took 2.3 s. `EXPLAIN ANALYZE` shows a sequential scan
   over all 29,574 rows, with 29,466 removed by the filter. On a table every
   tenant shares, one company's search would pay for every other company's code.
   The 14 MB repository took 153 ms only because the planner used the primary key.
2. **Postgres's parallel workers do not help.** Setting its default of 2 changed
   nothing. The text lives in TOAST, so the heap looks small and the planner
   never parallelises.
3. **An index on `content` alone has the same flaw.** Its times do not move with
   the repository's size, and it made the 14 MB repository's literal search 4×
   slower (566 ms against 128 ms), because candidates from every repository are
   re-checked. `btree_gin` puts `repository_id` inside the GIN index, and times
   scale with the repository again.
4. **Chunks cut the re-check.** A candidate is 200 lines rather than a whole
   file, which makes a literal search 3× faster than the whole-file index.
5. **A pattern made of common trigrams stays slow.** `type [A-Za-z]+Tool struct`
   took 1.3 s at 186 MB even with the index, because every trigram in it is
   common in Go. That is the case the cap (§5d) and a statement timeout exist for.
6. **Searching in the worker instead is not better.** Go's RE2 is fast when a
   pattern contains a literal, and took 6.4 s on a case-insensitive one.
7. **The roadmap's *"RE2, so no catastrophic backtracking"* was wrong** for a
   search that runs in Postgres, whose regex engine is not RE2. The statement
   timeout is the bound there; RE2 applies only to the second stage.
8. **The rest is not a concern.** COPY loaded 186 MB in 7.3 s. Reading a 469 KB
   file by primary key took 3.1 ms. Building each index over 257 MB took 39–45 s.
   Chunks plus their index took 236 MB for 257 MB of text.

**Why this matters more than the numbers.** Production's control-plane Postgres
runs in a Docker container on this same 2-CPU machine (`ps` shows
`postgres: argentum argentum 172.18.0.1(…)`), beside k3s and nine other
products. A 2.3 s single-core scan is half of the machine for 2.3 s.

**Two caveats.** The large repositories are the corpus repeated, so their
trigram posting lists are denser than 13 different codebases would make them,
which leans the indexed figures pessimistic. And nothing here measured a GIN
index being updated during a resync, or the database under production's load.

**Zoekt stays the step after this** (Apache-2.0, trigram and regex, ctags-ranked,
[github.com/sourcegraph/zoekt](https://github.com/sourcegraph/zoekt)), for a
repository these caps refuse. It is a new deployment, so it waits on a tenant who
needs one.

**Code embeddings are not proposed.** Coding agents that work well navigate with
search and reads. Embedding a repository is a per-sync cost for a retrieval
style that has not been shown to beat regex on *"what does status 7 mean"*.
Markdown docs are the exception, and `docchunk` already exists for prose (plan
`T-J9`, triggered).

### 5f. Performance beyond search

- **Syncs must not crowd out chat turns.** Worker queue weights are priorities,
  not limits (`internal/config/config.go:1244-1275`), and `WORKER_CONCURRENCY`
  defaults to 10 (`config.go:754`). A burst of syncs can take every slot while no
  chat is queued, and a turn then waits behind them. So syncs get their own
  low-weight queue, as video already does (`internal/queue/enqueuer.go:68-72`),
  plus a lock allowing one sync per worker process, a push debounce, and jitter
  on the reconciler.
- **A search must not hold the database.** The control-plane pool is 25
  connections per process (`internal/adapters/postgres/db.go:23`), and no
  control-plane statement sets a `statement_timeout`. So every search sets one
  locally, a company runs at most two searches at once, and results are cached
  by repository, synced SHA, pattern and path filter. A cache entry dies with its
  SHA.
- **Turn latency is model steps, not searches.** A step on the production model
  takes seconds (not measured here); a 50–400 ms search does not change what a
  user waits for. Fewer steps does, which is the same lever as cost (§5a).
- **The webhook handler only verifies and enqueues.** GitHub waits 10 s for an
  answer (§4).
- **Not measured:** GitHub API latency for the activity tool and for minting a
  token, archive download speed, and gzip and tar decoding under the worker's
  1-CPU limit.

---

## 6. Trust

- **Issues, PR bodies and comments are written by strangers** on any public
  repository, and by customers on many private ones. The *toxic flow* that makes
  this dangerous has been publicly demonstrated against agents holding GitHub
  tools: a public issue instructs the agent to copy private-repository content
  into a public PR. That incident is recalled from public reporting and was not
  re-read in this session. GitHub's MCP server ships *lockdown mode* for this
  class and calls it *"a best-effort content filter… not an authorization
  boundary"* ([server-configuration.md](https://github.com/github/github-mcp-server/blob/main/docs/server-configuration.md)).
  So:
  - **Text whose `author_association` is not `OWNER`, `MEMBER` or
    `COLLABORATOR` taints at a gating kind**, like a document under `T-H9`.
  - **File contents taint at `KindData`**, like warehouse rows. The tenant's own
    team wrote them, and gating every code answer would be the off switch
    `taint.go:13-20` warns about.
- **The App's private key reaches every installation.** GitHub advises a key
  vault over an environment variable ([best practices](https://docs.github.com/en/apps/creating-github-apps/about-creating-github-apps/best-practices-for-creating-a-github-app)).
  This deployment's secrets are Bitwarden-injected environment variables, the
  same standing as `T-H14`'s envelope half, which waits on a KMS decision. The
  plan records the key beside that decision and does not solve it separately.
- **The installation is bound only after a user token proves it** (§4). That is
  the tenant-isolation acceptance line.
- **Repository tools are never offered on widget turns.** A tenant's customer on
  the tenant's website must not be able to read the tenant's source.
  Roadmap 12's decision 10, applied to a new kind of content.
- **A channel binding already means *anyone who can post here***
  (roadmap 12, decision 8). An agent bound to both a repository and a Discord
  channel answers from private code there. The binding copy says so. No new
  mechanism is needed.

---

## 7. The write phase, shaped now so the read phase does not block it

- **A second GitHub App**, *Argentum (write)*, with `contents: write` and
  `pull_requests: write`. Read-only tenants never see a permission prompt, and a
  tenant can grant write to fewer repositories than read (§4, installation-owner
  approval).
- **An action kind, `github_pull_request`**, through `T-10`, so approval,
  idempotency, the 24-hour TTL, audit and the re-check at approval time
  (`mcp_call.go:160`'s shape) come for free.
  - **The proposal** carries the base SHA the agent read, a branch under a fixed
    prefix, a title, a body, and edited files as full contents. The model is not
    trusted to write patches; the server computes the diff.
  - **The approval card shows that diff.** What was approved is what is written.
- **On execution**, three steps:
  1. Mint a token narrowed to the one repository.
  2. Create the branch at the base SHA.
  3. Write one `createCommitOnBranch` commit guarded by `expectedHeadOid`, then
     open the PR.
  - A draft PR on a private repository needs a paid GitHub plan
    ([pulls](https://docs.github.com/en/rest/pulls/pulls)), so it falls back to
    ready-for-review with the fact stated.
  - **The default branch is never written to.**
- **A turn that read a private repository may not propose a PR to a public
  one.** That one rule closes §6's toxic flow structurally, where approval alone
  depends on a reviewer spotting it.
- **The agent cannot run the tests.** There is no sandbox, no `exec`, and
  roadmap 11's decision 8 argues against cluster-level isolation. The PR relies
  on the repository's own CI, and the agent can read the check runs back
  (`checks: read`). **Code changes that need a build loop go to a coding agent
  the tenant already runs** (GitHub's Copilot coding agent on an assigned issue,
  or a Claude Code GitHub Action on a mention), not to a sandbox built here. Both
  products are recalled, not re-read in this session. `T-J12` is the half-day
  spike that checks.

---

## 8. Tools and technology — what this needs, and what it does not

**Needed:**

| What | Why | Cost |
| --- | --- | --- |
| **A GitHub App registered by Smartsoft**, read-only: `contents`, `metadata`, `issues`, `pull_requests`, all read; `push`, `installation` and `installation_repositories` webhooks; *request user authorization during installation* on | Auth (§4). An org-owner action on github.com, not code | Free |
| **Four deployment secrets**: App ID, private key, webhook secret, OAuth client secret (Bitwarden, like the others) | Minting tokens, verifying deliveries, proving installations | — |
| **A public webhook route**, `POST /webhook/github` | Push-triggered sync. The `/webhook` group and Traefik ingress already exist (`cmd/api/router.go:373-388`) | — |
| **A small GitHub client in `internal/github`**, hand-written like `internal/slack`, using the `golang-jwt/jwt/v5` already in `go.mod` | About eight endpoints. `google/go-github` is an acceptable alternative; nothing forces it | — |
| **`pg_trgm` and `btree_gin`**, created in production's Postgres | The search index (§5e). Both ship in Postgres's contrib; creating an extension usually needs a superuser, which migrations may not run as | An operator step |

**Deliberately not needed for v1:**
- `git` or cloning: the tarball stream replaces it.
- Zoekt, a vector database, code embeddings: §5e.
- GitHub's MCP server: §5b.
- A code-execution sandbox: §7.
- tree-sitter or universal-ctags: cgo or `exec`, and regex over definitions
  covers the common case.
- A new worker image or more worker memory: the sync streams.

**Possibly later, each behind a measurement:**
- Zoekt, if a named tenant's repository needs more than trigram-indexed Postgres
  gives (§5e).
- A second App registration per GHES instance for an enterprise tenant.
- GitLab and Bitbucket. Tables and tools are named *repository*, not *github*,
  and carry a `provider` column. GitLab's push webhook lists only the newest 20
  commits ([webhook events](https://docs.gitlab.com/user/project/integrations/webhook_events/)),
  so the reconciler is not optional there either.

---

## 9. What is not known, and is owed

1. **Whether any tenant keeps anything in GitHub that this product's users ask
   about.** The pilot is a supermarket chain
   ([`../coverage/gelael-pilot.md`](../coverage/gelael-pilot.md)). Whether its
   data team has a dbt or SQL repository is one question to ask. **It is the
   cheapest thing on this list and should be asked before `T-J1` is built.**
   Like the roster and voice tracks, this one is owner-set rather than pulled in
   by a trigger, and this document does not pretend otherwise.
2. **Search latency in production.** §5e measured it on a scratch Postgres on
   this machine. Production's container under its own load, and a GIN index
   being updated during a sync, were not measured. `T-J2` and `T-J4` log both.
3. **The real schema token cost** of the three tools, and the real cost of a
   repository question. §5d is arithmetic. `T-J7` reads it from `usage_events`.
4. **Whether `gpt-5-nano`**, the classifier model (`guardrails.yaml:93`),
   applies a widened topic test reliably in both directions. `T-J6`'s eval cases
   decide it.
5. **Whether production has object storage**, which is not visible in the Helm
   chart. The design does not depend on the answer.
6. **Whether `pg_trgm` and `btree_gin` can be created** in production's Postgres
   (a Docker container on this host) by the role migrations run as. Creating an
   extension usually needs a superuser. It now blocks `T-J2`.

---

## Appendix — how §5e's numbers were produced

Run 2026-09-15 on this machine: 2 CPUs and 7.4 GiB, with production running
beside it at a load average of about 0.45.

- **The database.** A throwaway Postgres 16.9 on loopback, from the
  embedded-postgres binaries already in `/tmp`, with `shared_buffers` at 256 MB.
  The database and the benchmark both ran at `nice 19`, and the data directory
  was deleted afterwards.
- **The corpus.** Argentum's own tracked text: 1,643 files and 14.3 MB, with
  `pnpm-lock.yaml`, binaries and files over 512 KB excluded. It was loaded as
  three repositories at 1×, 4× and 13× (14.3, 57.2 and 185.8 MB; 29,574 files,
  49,032 chunks), all into one table, so each search shares the table the way
  tenants would.
- **The measure.** Each figure is the median of three runs of `count(*)`, or
  `count(DISTINCT path)` for chunks, which reads every match. A real search with
  a `LIMIT` stops sooner on a common pattern.
- **Two runs.**
  - The first measured whole files without an index, Go RE2, and an index on
    `content` alone.
  - The second read the plans and measured parallel workers, `strpos`, and the
    two `(repository_id, content)` indexes.

The second run's program and the raw output of both are in
[`assets/09-search-benchmark.go.txt`](assets/09-search-benchmark.go.txt) and
[`assets/09-search-benchmark.txt`](assets/09-search-benchmark.txt).
