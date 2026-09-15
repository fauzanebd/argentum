# A repository as a source — `T-J1` → `T-J12`

Written 2026-09-15 against `main` @ `9577cab`, from
[`../research/09-github-repository-source.md`](../research/09-github-repository-source.md),
which is the evidence for every claim this document does not re-derive. The
connection these tickets build is drawn on one page in
[`../research/assets/09-github-connection.html`](../research/assets/09-github-connection.html).

- **The read-only floor:** seven tickets, **~17.0 days (~15.5 backend, ~1.5 frontend)**.
- **Triggered, not scheduled:** two tickets.
- **The write phase:** three tickets, **~5.5 days**, shaped here and not scheduled.

**Why `J`.** `grep -rhoE "T-[A-Z]+[0-9]+" docs/` shows these letters taken:
`A B D F G H K M N P Q R S U V W Z`. `C`, `I` and `L` are rejected on the record
(roadmaps 09, 11 and 12: `T-C1` is one glyph from the finding `C-1`, and `T-I1`
and `T-L1` are one glyph from `T-11`). `O` reads as `0` (`T-O1` beside `T-01`).
`E` collides with the finding `E-5`. `G` would have been GitHub, but it is the
carousel track, and `R` is reports. `J` collides with nothing and cannot be
misread. `X` and `Y` appear only as placeholders (`T-XX`, `T-YY`) and were
passed over for that reason.

> **Status, 2026-09-15: nothing here is built, and two things come before
> `T-J1`.**
>
> 1. **Decision 1 is the owner's to confirm.** It widens the topic guardrail
>    for agents bound to a repository. The backlog's *Explicitly rejected* table
>    refuses removing topic enforcement, and this scopes it instead (research §1).
>    Without it, every read tool below is reachable only by questions that happen
>    to use BI vocabulary.
> 2. **Ask the pilot whether their data team keeps SQL or dbt in GitHub**
>    (research §9, unknown 1). It costs a message. Like the roster and voice
>    tracks, this track is owner-set rather than trigger-pulled, and the answer
>    decides whether use 1 of research §3 is the demo or a guess.
>
> **A prerequisite that is not code:** a Smartsoft org owner registers the
> read-only GitHub App (research §8). `T-J1` cannot run its live arm without it.
>
> **For a tenant who wants something this week:** GitHub's own MCP server
> registered through `T-M1` works today for BI-worded questions, with the five
> costs research §5b lists. Say so rather than build a shortcut.

> **Revised 2026-09-15, after a search benchmark (research §5e): the index is
> not later.**
>
> - **The finding.** Measured on a scratch Postgres on this machine, a search
>   with no index costs the whole table: 2.3 s whether the repository held 57 MB
>   or 186 MB. Production's control-plane Postgres shares these two CPUs.
> - **The change.** Text is stored as 200-line chunks under a GIN index on
>   `(repository_id, content)`: 47–137 ms for a literal, up to 1.3 s for a regex
>   no index can narrow. The cap drops from 200 MB to 50 MB a repository. Every
>   search runs under a timeout, a per-company limit and a cache (decision 13).
> - **The cost.** `T-J2` and `T-J4` grow by 1.5 days between them, and `T-J8`
>   becomes Zoekt.
> - **A new prerequisite:** `pg_trgm` and `btree_gin` created in production's
>   Postgres, which likely needs a superuser.

---

## 1. Decisions (locked — do not re-litigate inside the tickets)

**1. The topic scope widens per agent, and the rule stays.** An agent bound to a
repository has its topic test widened by one clause: *reading this
organization's own repositories — code, commits, pull requests, issues — is
reading its data*. An agent with no repository behaves byte-identically to
today. *"Teach me linked lists"* stays refused everywhere, because nothing of the
tenant's answers it (`guardrails.yaml:117`'s own test). **Owner to confirm.**

**2. A GitHub App, never a PAT.** The customer chooses which repositories the
App sees. Tokens are minted per use, narrowed to the bound `repository_ids` and a
permission subset, and **never stored** (research §4).

**3. An installation is bound only after a user token proves it.** The setup
URL's `installation_id` is spoofable. `GET /user/installations` with the
installing user's token is the check. The user token is then discarded, and no
refresh token is kept.

**4. Reading comes from a snapshot. Only another ref's file comes from the live
API.** The default branch is mirrored as text rows (research §5d). Tools never
fan out per-file API calls at turn time. GitHub's REST code search is not used
anywhere.

**5. A push webhook is a hint. The reconciler is the truth.** GitHub does not
retry deliveries. A periodic ETag-conditional head check (free on `304`) catches
every missed push.

**6. Empty binding means none.** `agent_repositories` follows
`agent_mcp_servers` and not `agent_sources`. Code is a DSN-class object, and an
agent that silently reaches every repository is the over-scoping failure that
is not recoverable.

**7. An agent without a repository pays nothing.** Repository tools, their
prompt lines and the repository map reach a turn only when its scope holds a
bound repository. The precedent is `offerRoomTools` (`stack.go:1064`). The
always-on channel is full ([`07-agentic-skills-roadmap.md`](07-agentic-skills-roadmap.md)
§2), and this track adds nothing to it.

**8. Results are ranges and matches, never files.** Every repository tool
returns bounded output with line numbers, `commit_sha` and `synced_at`. When it
stops short it says so in-band, with the call that gets the rest. A result
cap is truncation with a `note`, never an error (unlike `MCP_MAX_RESPONSE_BYTES`,
research §5b).

**9. Text by strangers gates. Code does not.** Issue, PR and comment bodies
whose `author_association` is not `OWNER`, `MEMBER` or `COLLABORATOR` taint at
a kind that gates actions, as `KindDocument` does. File contents taint at
`KindData` (research §6).

**10. The widget never reaches a repository.** Repository tools are not offered
on embed turns, whatever the agent's binding. Roadmap 12's decision 10.

**11. Named for repositories, built for GitHub.** Tables, tools and the wire say
*repository* and carry `provider`. Only `github` exists in v1, and github.com
only. GHES and GHE.com need a per-deployment API base and App registration
(research §4) and are not in this track.

**12. Writes are a second App and an action kind** (Track E). They are never a
tool that writes directly, never the default branch, and never a PR to a public
repository from a turn that read a private one.

**13. A search is bounded on the database every tenant shares.**
- Text is stored as 200-line chunks with a GIN index on
  `(repository_id, content gin_trgm_ops)`.
- Every search sets a local `statement_timeout`.
- A company runs at most two searches at once.
- A result is cached until its repository's synced SHA moves.

Measured, an unindexed search costs the whole table rather than the repository,
on a Postgres that shares two CPUs with production (research §5e).

---

## 2. What already exists, and is the reason this is 17.0 days

Research §2 is the full table. The pieces each ticket leans on:
- **`T-J1`:** `DSNCipher` and `golang-jwt/jwt/v5`, plus the Slack webhook's
  verify-then-dedupe shape.
- **`T-J2`:** asynq task names and the `document_chunks` precedent for tenant
  text in the control plane.
- **`T-J3`:** `agent_mcp_servers` and `agentscope`, plus `offerRoomTools`.
- **`T-J4`:** the four decorators every tool gets (`stack.go:725-789`) and
  `agentbudget.dataTools`.
- **`T-J5`:** the taint kinds.
- **Track E:** `T-10`'s action framework and `mcp_call`'s re-check at execute.

---

## 3. The tickets

### Track A — Connect, and a copy to read (7.0d) · do first

#### `T-J1` Connect a GitHub installation, proven to belong to the company
**Repo:** BE + FE · **Size:** 3.0d (BE 2.0, FE 1.0) · **Deps:** the App registered (not code) · **Migration:** claim at build time (`090` at writing)

##### Why
Decisions 2 and 3. A spoofed installation binds another company's code to this
tenant, and it is the single worst failure mode in this track.

##### Do
- **Config.** `GITHUB_APP_ID`, `GITHUB_APP_PRIVATE_KEY`, `GITHUB_APP_WEBHOOK_SECRET`,
  `GITHUB_APP_CLIENT_ID`, `GITHUB_APP_CLIENT_SECRET`.
  - All optional. With any missing, the feature is off, logged once at boot, and
    Settings says so (the `internal/email` nop shape).
- **`internal/github`.** An App JWT (RS256, `golang-jwt/jwt/v5`), `MintToken(ctx,
  installationID, repositoryIDs, permissions)`, and the handful of REST calls
  the track uses.
  - An in-process token cache keyed by `(installation, repo set, permissions)`
    that expires five minutes early.
  - A token is never logged, never stored, and never in an error string.
- **The tables.**
  - `repository_installations`: `company_id`, `provider`, `installation_id`,
    `account_login`, `suspended_at`, `created_by`. Unique on
    `(provider, installation_id)`, so one installation cannot bind two companies.
  - `repositories`: `company_id`, `installation_id`, `provider_repo_id`,
    `full_name`, `default_branch`, `private`, `enabled`, include and exclude
    globs, and sync fields (filled by `T-J2`).
- **Install flow.**
  1. The admin presses *Connect GitHub* and is redirected to the App's install
     page with a signed, single-use `state` naming company and user.
  2. The callback exchanges the OAuth code.
  3. It calls `GET /user/installations`.
  4. It binds only an installation that list contains.
- **Webhook `POST /webhook/github`.** Raw body → `X-Hub-Signature-256` constant-time
  check → dedupe on `X-GitHub-Delivery` (Slack's `RedisDeduper` shape) → route:
  - `installation.deleted` and `suspend` disable the installation.
  - `installation_repositories.removed` disables those repositories.
  - `push` is `T-J2`'s.
- **Admin routes.** List installations and repositories, enable or disable a
  repository, edit its globs, disconnect. Each gets its `cmd/api/policy.go` row
  (admin) and a `resourcePolicy` exemption with its reason.
- **Settings → Repositories.** The connect button, the repositories the
  installation grants with an enable switch each, and a sentence stating what
  enabling does and who can then read it (roadmap 12, decision 8's channel copy
  carries over).

##### Acceptance
- [ ] A callback whose `installation_id` is not in the installing user's
      `/user/installations` is refused and binds nothing. **This is the
      tenant-isolation arm.**
- [ ] One installation cannot be bound by a second company
- [ ] A replayed callback `state` is refused
- [ ] A delivery with a wrong or missing signature is `401` and enqueues nothing;
      the same delivery id twice is processed once
- [ ] A minted token's request body names exactly the bound repository ids and the
      read permissions, asserted on the wire
- [ ] No token appears in a log line, an error, a response body or a row
- [ ] Uninstalling the App on GitHub disables the company's repositories on the
      delivery, and the reconciler (`T-J2`) catches it if the delivery was lost
- [ ] The feature is absent, and says why, on a deployment with no App configured

---

#### `T-J2` A snapshot of the default branch, kept current
**Repo:** BE · **Size:** 3.5d (was 2.5d before research §5e) · **Deps:** `T-J1` · **Migration:** claim at build time, and it creates two extensions

##### Why
Decision 4. Research §5d is the design and §5a is why it is the cheap one.

##### Do
- **`repository_files`:** `company_id`, `repository_id` (cascading), `path`,
  `blob_sha`, `size_bytes`, `language`, `line_count`. Unique on
  `(repository_id, path)`. Every statement is `WHERE company_id = $1`.
- **`repository_chunks`:** the text itself, as 200-line chunks keyed
  `(repository_id, path, start_line)`, with a GIN index on
  `(repository_id, content gin_trgm_ops)` (decision 13).
  `read_repository_file` assembles a range from these by primary key (3.1 ms for
  a 469 KB file, measured).
  - **Extensions.** The migration runs `CREATE EXTENSION IF NOT EXISTS pg_trgm`
    and `btree_gin`, which production's role may not be allowed to do. Check
    before claiming the number, and if it can't, hand the operator the two
    statements.
  - **Chunk boundaries.** A match spanning two chunks is missed. Multi-line
    patterns are rare enough that the tool description says so, rather than
    overlapping chunks.
- **The `repository:sync` task** runs asynq-unique per repository, so a burst of
  pushes is one sync:
  1. Stream `GET /tarball/{default_branch}`.
  2. Unpack as a stream with constant memory, and skip what the caps refuse.
  3. Upsert changed blobs by SHA and delete paths that are gone, in one
     transaction per batch.
  4. Record `synced_sha`, `synced_at`, `file_count`, `text_bytes` and
     `sync_error`.
- **Caps**, each a config value with a stated default and each refused with a
  sentence rather than silently dropped:
  - text file ≤ **512 KB**
  - ≤ **50,000** files
  - ≤ **50 MB** of text per repository and **200 MB** per company. Both are
    measured bounds, not guesses: at 57 MB an indexed search took 2–387 ms
    (research §5e)
  - binaries skipped (a NUL in the first 8 KB)
  - a fixed skip list: `node_modules/`, `vendor/`, `dist/`, `build/`, `*.min.*`,
    lockfiles, images
  - the admin's globs
  - **A repository over the size cap does not sync.** It stops with the size,
    the cap and *"narrow it with include globs"*. A partial copy that answers as
    if whole is the wrong-answer class `T-F1` exists for.
- **Sync on:** enable, a `push` to the default branch, and a **reconciler**
  every `REPOSITORY_RECONCILE_MINUTES` (default 60). The reconciler asks for the
  branch head with `If-None-Match`, where a `304` is free.
- **Syncs never crowd out chat turns** (research §5f):
  - their own low-weight queue, as video already has
  - a Redis lock allowing one sync per worker process
  - a push debounced by 60 s
  - jitter on the reconciler, so a thousand repositories do not wake in the
    same minute
- **Erasure.** `RetentionService.EraseCompanyData` removes repository rows.
  Disabling a repository deletes its files. Disconnecting deletes everything.
- **Metrics.** Sync duration, bytes, and files skipped by reason.

##### Acceptance
- [ ] A sync of a fixture tarball stores exactly its text files, with each skip
      counted by reason
- [ ] A second sync after one file changed writes only that file's row and chunks,
      asserted, and the statement time, index update included, is logged
- [ ] Syncs for two repositories enqueued together run one after the other on one
      worker process, and a chat turn enqueued between them is not held behind both
- [ ] A repository over the size cap stores nothing and states the cap in `sync_error`
- [ ] Peak memory during a sync of a 150 MB fixture stays bounded, measured, and
      the number is written in the record
- [ ] A missed push is picked up by the reconciler; an unchanged head costs a `304`
- [ ] Company erasure leaves no `repository_files` row
- [ ] No row is readable under another `company_id`, asserted

---

#### `T-J3` Bind a repository to an agent, and offer the tools only there
**Repo:** BE + FE · **Size:** 1.5d (BE 1.0, FE 0.5) · **Deps:** `T-J1` · **Migration:** claim at build time

##### Do
- **The binding.** `agent_repositories`, where empty means none (decision 6).
  `Agent.RepositoryIDs`, `agentscope.Scope.RepositoryIDs` and `AllowsRepository`.
  It is folded into agent Create and Update the way `T-M3` folded MCP bindings,
  and the insert re-checks `company_id`.
- **Per-turn offering** (decision 7). An `offerRepositoryTools` beside
  `offerRoomTools` strips the repository tools from a turn whose scope has no
  enabled, synced repository, and from every embed turn (decision 10), whatever
  `allowed_tools` says.
- **The agent form.** A *Repositories* scope group with the copy *"empty means
  none"*, beside the MCP group that already says the same.
- **`GET /v1/agents`** publishes `repositories: [{id, full_name}]`, the
  `mcp_servers` precedent. The OpenAPI spec and SDKs are regenerated.
- **Roadmap 12.** No new `ResourceKind`: a repository is reached only through an
  agent, which is already a kind, and its routes are admin-only.
  `authztest`'s table gets a row saying so, so the decision is written down.

##### Acceptance
- [ ] An agent with no binding has a tool list byte-identical to today's, and its
      system prompt too — **the property that proves decision 7**
- [ ] A bound agent on a dashboard, `/v1` and a channel turn holds the tools; the
      same agent on a widget turn does not
- [ ] A binding naming another company's repository binds nothing
- [ ] A repository disabled mid-conversation is absent from the next turn

---

### Track B — Reading (7.0d)

#### `T-J4` `search_repository`, `read_repository_file`, `list_repository_files`
**Repo:** BE + FE (labels) · **Size:** 3.5d (was 3.0d before research §5e) · **Deps:** `T-J2`, `T-J3` · **Migration:** a backfill only if a template card gains the tools

##### Why
Research §3, uses 1, 3 and 4 with one set of tools, and §5d's cost shape.

##### Do
- **`search_repository`**
  - Parameters: `pattern` (RE2 syntax, compiled in Go before anything runs), an
    optional `repository`, `path_glob`, `literal: bool` (default true),
    `max_results` (default 30, cap 60).
  - Returns `path:line` with two lines of context, grouped by file.
  - Capped at **8 KB**, with a `note` naming the narrowing that gets the rest.
  - **Two stages** (decision 13).
    1. Postgres selects candidate chunks through the index, under
       `SET LOCAL statement_timeout` (default 2 s) and a limit.
    2. Go's RE2 finds the exact lines in those candidates.
    - The candidate stage reads the pattern as a Postgres regex, which is not
      RE2. That is why the timeout, not RE2, is the bound there, and why the
      tool description names the syntax both engines read alike.
    - A timeout answers *"that pattern is too broad; add a path_glob or a longer
      literal"*, which the model can act on.
  - At most two concurrent searches per company (a Redis semaphore), and a cache
    keyed by repository, synced SHA, pattern and glob.
  - **Each call logs its latency, candidate count and the repository's
    `text_bytes`**, the numbers `T-J8`'s trigger reads.
- **`read_repository_file`**
  - Parameters: `path`, `start_line`, `end_line`.
  - Returns at most **300 lines or 16 KB**, numbered.
  - A request for more returns the first range plus a `note` with the next
    call's arguments.
  - An optional `ref` reads another ref over the live Contents API, capped the
    same way and noted as live (decision 4).
- **`list_repository_files`**
  - Parameters: a directory or glob.
  - Returns paths with sizes and languages, capped at 200 entries.
- **Every result** carries `repository`, `commit_sha`, `synced_at` and a
  permalink template (`blob/{sha}/{path}#L{a}-L{b}`).
- **The repository map.** A bounded block (≤ **300 tokens**) on repository-bound
  turns only. It lists each bound repository's name, synced SHA and age,
  top-level directories, languages, and whether it looks like a dbt project
  (`dbt_project.yml`) or holds migrations. Built at sync time, not per turn.
- **Evidence and metering.** The three tools join `agentbudget.dataTools`, so a
  figure read out of a file (a discount rate in a config) is grounded. Each call
  meters a `repository_read` usage event priced like `SQLQueryCost`
  (`usage_service.go:64`), for the reason `T-M2` gave for not pricing at zero.
- **Every new tool needs four edits** (conventions §"Agent tools" 7):
  - `registry.go`
  - `promptTools` lines, plus one guideline: *search first; read the range the
    search found; cite `path:line`; say which commit the answer is as of*
  - `agentToolLabels`
  - `agent_templates.yaml`, with a decision per card, plus an **Engineering**
    card, since a card that reaches repositories is how a tenant finds the
    capability
  - **No backfill.** These tools widen data reach, which is conventions rule 8's
    exception, and they are offered only through a binding an admin makes anyway.

##### Acceptance
- [ ] A regex search over the fixture returns every match with line numbers and
      nothing from another repository or company
- [ ] A pattern the index cannot narrow hits the statement timeout and returns the
      sentence above, and its database connection is released, asserted
- [ ] A third concurrent search from one company waits, and never runs as a third
- [ ] The same search at the same synced SHA is served from the cache; after a
      sync it is not
- [ ] A read past the cap returns the first range and a `note` whose arguments,
      called, return the next range — asserted by calling it
- [ ] Every result carries `commit_sha` and `synced_at`
- [ ] A figure stated from a file read grounds a reply; the same figure with no
      read does not
- [ ] The three tools' descriptions plus schemas are measured in characters and the
      number written in the record (research §9, unknown 3)
- [ ] Prompt change → a paired `make eval` on the golden set shows no regression
      for agents with no repository

---

#### `T-J5` `repository_activity`: commits, pull requests, issues, releases
**Repo:** BE · **Size:** 2.0d · **Deps:** `T-J3` · **Migration:** none expected

##### Why
Research §3, use 2: *what shipped the day the number moved*. This is the
cross-source question, and the one no BI tool answers.

##### Do
- **One tool with a `kind` parameter** (`commits | pull_requests | issues |
  releases`) rather than four, because schema tokens ride every iteration
  (research §5a). It also takes a window (`since`, `until`), `path`, `author`,
  `state` and `number`.
- **It calls the live API**, with an ETag cache in Redis keyed per request, so
  the same question twice costs a `304`.
- **Compact rows.** SHA, date, author, first line, and for a PR the merged-at
  time, files changed and additions/deletions.
- **A single PR by number** adds its changed-file list and a patch excerpt
  capped at 8 KB.
- **Bodies are truncated** at 2 KB each. **Every body and comment carries
  `author_association`.** Text by anyone who is not `OWNER`, `MEMBER` or
  `COLLABORATOR` is tainted at a gating kind (decision 9): a new
  `taint.KindThirdParty`, which gates like `KindDocument`, with the package
  comment extended to say why.
- **Rate-limit honesty.** A `403`/`429` with `retry-after` is a result the model
  can act on (*"GitHub is rate limiting this workspace; try again in N
  seconds"*), never a retry loop inside the turn.

##### Acceptance
- [ ] *"PRs merged between two dates"* returns exactly the fixture's merged PRs in
      that window
- [ ] A comment by a non-collaborator taints the turn at the gating kind; a
      member's does not — **both asserted**
- [ ] A turn so tainted cannot execute an action without approval, even where the
      action's company setting says approval is off
- [ ] A repeated identical call within the cache window issues a conditional request
- [ ] A secondary-rate-limit response reaches the model as a sentence and the turn
      does not retry

---

#### `T-J6` The topic scope, and the persona, for a repository-bound turn
**Repo:** BE · **Size:** 2.0d · **Deps:** `T-J3`; **ships with `T-J4`** · **Migration:** none

##### Why
Research §1. Without it, `T-J4` is a tool behind a rule that refuses its
questions. It is never cut apart from `T-J4`.

##### Do
- **`guardrails.TopicScope`**, passed per turn the way `PIIMode` is, set from
  the turn's scope: `repository` when it holds a bound repository.
  `Analytics.WithTopicScope` is a shallow copy, the shape `WithLLM` has
  (`guardrails.go:89`).
- **The YAML says what widens.** A rule may declare `unless_topic: repository`,
  and `block_off_topic_programming_tutorial` does. The `type: llm` pattern of
  `require_analytics_topic` gains a `pattern_repository` variant whose TRUE list
  adds one clause. Keep the load-bearing first line (`guardrails.yaml:109-112`),
  or the golden stub reports every case blocked.
- **The widened TRUE clause, as written:** *"a question answered by reading this
  organization's own code repositories — what their code, SQL or models do,
  mean or define, and what was committed, merged or reported in them"*. The
  FALSE families stay, reworded to *"general programming help that nothing in
  their repositories answers"*.
- **One persona line** in `promptTools`' guideline set, gated on the repository
  tools being held: the agent answers questions about the tenant's repositories
  with the same honesty rules as data, cites `path:line`, and does not write
  code for problems the repository does not contain.
- **The guardrail golden cases, both directions, for both scopes.** The record
  names each case the classifier got wrong.

##### Acceptance
- [ ] With no repository: every existing golden guardrail case is unchanged, and
      *"how does our Go service compute the discount?"* is refused exactly as today
- [ ] With a repository: that question is admitted; *"teach me linked lists"* and
      *"write me a Python web scraper"* are still refused
- [ ] The regex block is skipped only under the repository scope, asserted both ways
- [ ] Recorded in [`../coverage/guardrail-overreach.md`](../coverage/guardrail-overreach.md)
      beside the existing false-positive history

---

### Track C — Proof (1.5d)

#### `T-J7` A fixture repository, the eval cases, and the real cost of a question
**Repo:** BE · **Size:** 1.5d · **Deps:** `T-J4`, `T-J5`, `T-J6` · **Migration:** none

##### Do
- **A fixture repository in `testdata/`:** a small dbt project over the demo star
  schema (`fact_sales`, `dim_customers`, `dim_products`, `dim_date`), with an
  enum file, a README carrying a prompt-injection line, and a commit and PR
  history with one merge on a date the demo data shows a dip. The eval tenant
  syncs it through a `RepositorySource` fake, so no eval calls GitHub.
- **Six cases:**
  1. A definition answered from `models/…sql` with a `path:line` citation.
  2. An enum meaning answered from code.
  3. *"What merged the day revenue dropped?"* across `query_metric` and
     `repository_activity`.
  4. The README's injected instruction not followed.
  5. A generic coding request refused for a repository-bound agent.
  6. The same definition question on an unbound agent calls no repository tool.
- **The cost read.** Tokens and `usage_events` per repository case, set against
  research §5d's ≈ $0.066. If the real figure is more than 2× that, the record
  says which lever (schemas, result sizes, iterations) moved it.
- **The live arms, filed** in [`../coverage/live-gate-backlog.md`](../coverage/live-gate-backlog.md):
  a real App, a real private repository, a push, an uninstall, and one question
  per case above against production's model.

##### Acceptance
- [ ] Six cases pass on the default model, and the golden set does not regress
- [ ] The cost per repository question is measured and written, not estimated
- [ ] The live arms are filed with what each needs

---

### Track D — Triggered, not scheduled

#### `T-J8` Zoekt, when trigram-indexed Postgres is not enough
**Size:** 3.0d, guessed · **Trigger:** a named tenant whose repository the 50 MB
cap refuses, or `T-J4`'s logged p95 search latency over **2 s**.
A Zoekt deployment with a shard per repository, fed by the same sync. It is a new
service to run, which is why it waits on a tenant.

**Revised 2026-09-15.** This ticket used to hold the trigram index, deferred until
the scan was shown to be slow. A benchmark showed something worse than slow: an
unindexed search costs every tenant's text, not the repository's. The index moved
into `T-J2` and `T-J4` (research §5e).

#### `T-J9` Repository prose into knowledge
**Size:** 2.0d, guessed · **Trigger:** repository-bound turns calling
`search_repository` repeatedly for prose questions, readable from `agent_actions`.
Markdown files go through `docchunk` into the hybrid search `search_documents`
already runs. That table is PDF-shaped (research §2), so it needs a document
kind or a sibling table; the ticket decides which.

---

### Track E — Write, later (5.5d) · shaped, not scheduled

#### `T-J10` The write App, and `github_pull_request` as an action kind
**Size:** 3.5d · **Deps:** Track B built and its live arms run.
The design is research §7 and decision 12:
- a second App
- a proposal of full file contents against the synced SHA
- a server-computed diff on the approval card
- a branch under `argentum/`
- `createCommitOnBranch` with `expectedHeadOid`
- a draft PR where the plan allows it
- re-checks at approval time
- **refused when the turn read a private repository and targets a public one**,
  and when the branch exists

#### `T-J11` After the PR: checks and review comments read back
**Size:** 1.5d · **Deps:** `T-J10`. Adds `checks: read`. The agent reports the
repository's own CI result on the PR it opened and reads review comments, with
decision 9's taint on the comments.

#### `T-J12` Spike: hand code work to a coding agent the tenant already runs
**Size:** 0.5d · **Deps:** none. Checks, against primary sources, whether
assigning an issue to GitHub's Copilot coding agent or mentioning a Claude Code
GitHub Action is a reliable way for Argentum to *delegate* a change that needs
a build loop. The alternative is a code-execution sandbox, which roadmap 11's
decision 8 already argues against. The output is a paragraph and a
recommendation, not code.

---

## 4. Cut order

Never cut: `T-J1`, `T-J2`, `T-J3`, **`T-J4` and `T-J6` together**, `T-J7`.

1. **`T-J5`** (2.0d). Uses 1, 3 and 4 still ship. Use 2, the cross-source
   question, is lost, and it is the best demo. Cut it last if the pilot's answer
   (status, item 2) says their question is *what changed* rather than *what does
   this mean*.
2. **`T-J1`'s repository picker** (0.5d of FE). Enable every repository the
   installation grants, with the copy saying so; the admin narrows on GitHub's
   side.
3. **`T-J3`'s `/v1` publishing** (0.25d). The binding still works over `/v1`; an
   integrator just cannot see it.

Floor after all three: **~14.25d.**

---

## 5. What is deliberately not here

| Not here | Why |
| --- | --- |
| GitHub's MCP server as the product | Research §5b: a person's PAT, eager schemas, errors over 256 KB, drift chores, legacy search. It still works through `T-M1` for a tenant who asks |
| Cloning, `git`, a workspace directory | No `exec` in this tree and a 512 Mi worker. The tarball stream does the job |
| Code embeddings | Research §5e. Regex and reads first; prose is `T-J9`'s |
| Running tests or builds | No sandbox, and none should be built for this. `T-J12` |
| GHES, GHE.com, GitLab, Bitbucket | Decision 11 keeps the names neutral. Each is a named tenant's trigger |
| A person-level grant on repositories | Reached only through an agent, which roadmap 12 already restricts (`T-J3`) |
| Watchers on repository events (*"tell #eng when a migration merges"*) | A real follow-on to `T-J5` and `T-08`. It needs `T-J5` in production first |
