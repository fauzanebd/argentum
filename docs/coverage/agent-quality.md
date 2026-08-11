# `T-Q1`→`T-Q9` · Agent quality — coverage

**Code complete 2026-08-11** ([`../plan/02-agent-quality-roadmap.md`](../plan/02-agent-quality-roadmap.md)),
nine tickets in one sitting, **completely ungated**. Four of the nine change
what reaches the model on every turn and none of it had been observed.

**Partially gated 2026-08-11, same day.** The deterministic half — everything
that needs the control database and the demo warehouse and nothing else — was
run. It produced **three defects, all fixed and re-proven**, which is the
pattern [`live-gate-backlog.md`](live-gate-backlog.md) has recorded on every
ticket where the live half was run. All three are the same shape as the three
the roadmap already confessed to: **the tests and the code agreed with each
other, and both disagreed with production.**

**The other half did not run, and the reason is not a cost.** There is no
`.env` in this working tree — only `apps/backend/.env.example`, which is a stale
single-tenant file naming variables the current `config.Load` does not read. So
`LLM_API_KEY`, `ARGENTUM_JWT_SECRET`, `ARGENTUM_DSN_KEY` and `DB_PASSWORD` are
all absent, the API and worker cannot boot, and **model spend was zero for this
sitting** — not because it was declined, because there is no credential to spend
against. §7 lists exactly what each remaining gate needs.

---

## 1. Migrations `054` and `055`, up and down — **pass**

The one item on the roadmap's owed list that nothing else depends on, and the
one that had never been exercised in either direction. Run against the real
control Postgres (`argentum_postgres`, 30 companies, 494 threads, 1,070
messages, 741 `agent_actions`), from version 53.

```
$ psql -c "select version, dirty from schema_migrations"
 version | dirty
---------+-------
      53 | f

=== 054 UP ===
CREATE TABLE
CREATE INDEX
CREATE INDEX
=== 055 UP ===
CREATE EXTENSION
NOTICE:  extension "vector" already exists, skipping
CREATE TABLE
CREATE INDEX
 version | dirty
---------+-------
      55 | f
```

Both objects land as designed — the partial index, the CHECK, the unique
constraint and all three cascading foreign keys:

```
Indexes:
    "message_feedback_pkey" PRIMARY KEY, btree (id)
    "idx_message_feedback_company_recent" btree (company_id, created_at DESC)
    "idx_message_feedback_negative" btree (company_id, message_id) WHERE rating = '-1'::integer
    "uq_message_feedback_actor" UNIQUE CONSTRAINT, btree (message_id, actor_kind, actor_ref)
Check constraints:
    "message_feedback_rating_check" CHECK (rating = ANY (ARRAY['-1'::integer, 1]))
Foreign-key constraints:
    ... REFERENCES companies(id) ON DELETE CASCADE
    ... REFERENCES messages(id) ON DELETE CASCADE
    ... REFERENCES conversation_threads(id) ON DELETE CASCADE
```

Then **down, against populated tables** — two feedback rows and one
`query_examples` row with a real 1536-dimension vector, so the reversal is not
being asked the easy question:

```
=== 055 DOWN ===
DROP INDEX
DROP TABLE
=== 054 DOWN ===
DROP INDEX
DROP INDEX
DROP TABLE
 version | dirty
---------+-------
      53 | f

 message_feedback | query_examples | idx_negative
------------------+----------------+--------------
                  |                |
```

And up again, clean, with the sequence back at its start:

```
 message_feedback | query_examples | mf_rows | qe_rows | seq
------------------+----------------+---------+---------+-----
 message_feedback | query_examples |       0 |       0 |   1
```

**No defect.** One thing worth recording because it is easy to get wrong and
`055` gets it right: the down migration does **not** drop the `vector`
extension. `table_embeddings` has depended on it since migration 013, and a
down migration that took the extension with it would break a table it never
created.

## 2. Feedback storage semantics — **pass** (HTTP layer still owed)

The roadmap asks for a `POST /api/messages/:id/feedback` round trip and three
refusals. The route needs the API booted, which needs the credentials §7
describes. What *can* be gated without it is the half no unit test covers: the
storage semantics against the real table, using the repository's own SQL against
a real message on a real thread.

```
--- vote 1: thumbs up, dashboard user ---
                  id                  |          created_at           |          updated_at
--------------------------------------+-------------------------------+-------------------------------
 4aaab992-790e-40a8-bb26-e9054f0e093c | 2026-08-11 15:53:46.637702+00 | 2026-08-11 15:53:46.637702+00

--- vote 2: SAME actor changes mind to thumbs down ---
                  id                  |          created_at           |          updated_at
--------------------------------------+-------------------------------+-------------------------------
 4aaab992-790e-40a8-bb26-e9054f0e093c | 2026-08-11 15:53:46.637702+00 | 2026-08-11 15:53:46.645133+00

--- expect exactly 1 row, rating -1, created_at < updated_at ---
 rows | rating | updated_moved
------+--------+---------------
    1 |     -1 | t

--- a DIFFERENT actor is a second row, not a conflict ---
 actor_kind | actor_ref | rating
------------+-----------+--------
 embed      | visitor-9 |      1
 rows_now
----------
        2

--- rating = 0 must violate the CHECK ---
ERROR:  new row for relation "message_feedback" violates check constraint "message_feedback_rating_check"

--- Summarize(): FILTER counts ---
 rated | up | down
-------+----+------
     2 |  1 |    1
```

**The second vote replaces the first rather than duplicating** — same `id`,
`created_at` preserved, `updated_at` moved — which is the roadmap's third
refusal, proven at the layer that enforces it. The 400 and the 404 are decided
in `FeedbackService.Rate` and `MessageRepo.GetForCompany` above this; both have
unit tests, and neither has been seen over HTTP. Still owed.

## 3. `T-Q8`'s harvester against real history — **defect 1, fixed**

### The candidate query works, on 121 real turns

`CookbookCandidateRepo.Candidates` had never been run against anything but
fixtures. Against the deployment's real 741 `agent_actions` rows:

```
=== run_sql actions overall ===
 tool_name | result_status | count
-----------+---------------+-------
 run_sql   | blocked       |     7
 run_sql   | ok            |   209
 run_sql   | error         |    26

=== the harvester Candidates() WHERE clause, applied deployment-wide ===
 candidates
------------
        121
```

T-Q8's premise holds: this deployment's own history contains 121 question→SQL
pairs that clear every SQL-level filter. The `m.role = 'user'` predicate is
correct — `agent_actions.message_id` points at the question, confirmed 717 to 0.

### The verdict gate could never fire

The roadmap names one thing as the difference between T-Q8 being valuable and
being negative-value:

> **The one hard part is the label.** … Seeding the cookbook with confidently
> wrong SQL is the failure mode that would make this negative-value.

That gate did not work. Two facts, each true and each individually correct:

- **`message_feedback.message_id` is always an ASSISTANT message.**
  `FeedbackService.Rate` refuses anything else with `ErrNotAssistantMessage` —
  you rate an answer, not your own question.
- **`agent_actions.message_id` is always the USER message.** 717 of 717 real
  rows join to `role='user'`, 0 to `role='assistant'`, and `Candidates()`
  filters on it explicitly.

`CookbookService.Harvest` read `negative[c.MessageID]` — looking a *question* id
up in a table that only ever holds *answer* ids. The two spaces are disjoint by
construction, so `skipped_negative` was structurally always zero and every turn
a human had marked wrong was learned from anyway.

Proven on a real turn. A real candidate, its real answer, a real thumbs-down
written exactly as `Rate` writes it:

```
=== a real cookbook candidate ===
              company_id              |         candidate_message_id         | candidate_role
--------------------------------------+--------------------------------------+----------------
 de3caef9-5951-4888-a1cd-be77f6542c51 | 5556865e-9912-4da9-ae51-f7b8df4cf18e | user

=== the assistant message that ANSWERED it ===
          answer_message_id           |   role
--------------------------------------+-----------
 622ae2fa-ef8b-4dce-a5aa-a6fe87ec744a | assistant

=== a user thumbs-DOWN on that answer ===
           rated_message_id           | rating |        reason
--------------------------------------+--------+----------------------
 622ae2fa-ef8b-4dce-a5aa-a6fe87ec744a |     -1 | this answer is wrong

=== NegativeMessageIDs(company, [candidate ids]) -- the gate Harvest consults ===
 flagged_negative
------------------
(0 rows)

=== why: the two id spaces are disjoint ===
 feedback_on_assistant | feedback_on_user | actions_on_assistant | actions_on_user
-----------------------+------------------+----------------------+-----------------
                     1 |                0 |                    0 |             717
```

**Why no test caught it.** `TestHarvestRefusesToLearnFromAnAnswerMarkedWrong`
passed, and passed honestly — its fake filed the verdict against
`candidate("msg-bad")`'s own id, because the fake had only one id to use. The
test asserted the gate fires when the verdict is keyed the way the service reads
it, which is a tautology. Exactly the roadmap's own confession, a fourth time.

**Fix.** `domain.CookbookCandidate` gains `AnswerMessageID`, resolved in the
repository by a `LEFT JOIN LATERAL` to the first assistant message in the same
thread at or after the question. `Harvest` asks the verdict gate about
`VerdictKeys()` — question *and* answer, so the gate fails closed and cannot get
weaker if a verdict is ever filed the other way round.

All 121 real candidates resolve to an answer, and the gate now fires on the
exact turn that previously slipped through:

```
=== deployment-wide: how many candidates resolve to an answer? ===
 candidates | with_answer
------------+-------------
        121 |         121

=== NegativeMessageIDs over VerdictKeys() = [question, answer] ===
           flagged_negative
--------------------------------------
 622ae2fa-ef8b-4dce-a5aa-a6fe87ec744a
```

Two tests now cover it, and both fail against the pre-fix service:

```
--- FAIL: TestHarvestRefusesToLearnFromAnAnswerMarkedWrong (0.00s)
    learned 2, skipped 0 negative; want 1 and 1
--- FAIL: TestHarvestAsksAboutTheAnswerNotOnlyTheQuestion (0.00s)
    the verdict gate was asked about [msg-1] — never about the answer id,
    which is the only kind message_feedback holds
```

The second is the one that matters: it pins the *query* rather than the outcome,
so a future fake cannot make the gate look alive by handing it the wrong id.

**What is still owed here:** the harvest that *writes* an example. Every gate
above `client.Embed` is now proven; the embedding call needs credentials.

## 4. `T-Q9`'s zero-row probe — **defects 2 and 3, fixed**

Run against the demo warehouse (`argentum_postgres_demo`, 4 tables), which needs
no control-plane credentials — the DSN is in `docker-compose.yml`. The probe is
fully deterministic: given a connection and schema metadata it parses the WHERE
clause and runs `SELECT DISTINCT`. No model is involved, which is why this gate
was runnable at all.

### Defect 2 — the probe never ran on a real query

First run, on a genuinely zero-row query:

```
    query returned 0 rows
    PROBE RETURNED NOTHING for a query whose literals match no stored value
```

`parseEqualityFilters` located the WHERE clause with
`strings.Index(strings.ToLower(sql), " where ")` — a literal **space** on both
sides. Models write multi-line SQL, and the WHERE goes on its own line after a
newline. The index never matched, the function returned nothing, and **the whole
of T-Q9's first half was unreachable on every real query**.

Every case in `TestParseEqualityFiltersFindsWhatWasFilteredOn` was a single-line
query. Same shape as defect 1: the test and the code agreed, and production was
somewhere else.

Fixed with a word-boundary regex, `(?is)\bwhere\b`, which accepts any
whitespace on either side. Three cases added — WHERE on
its own line, WHERE followed by a newline, tab-indented WHERE — and all three
fail against the old finder:

```
--- FAIL: TestParseEqualityFiltersFindsWhatWasFilteredOn (0.00s)
    WHERE on its own line, as a model writes it: no filter found
    WHERE followed by a newline: no filter found
    tab-indented WHERE: no filter found
```

### The gate the ticket asked for, once it could run

`available_values` comes back with the column's actual contents, quoted, on both
filtered columns:

```
    available_values:
        [
          {
            "actual_values": ["\"August\"", "\"December\"", "\"July\"",
                              "\"November\"", "\"October\"", "\"September\""],
            "column": "month_name",
            "table": "dim_date",
            "you_filtered_for": "december"
          },
          {
            "actual_values": ["\"Bandung\"", "\"Jakarta\"", "\"Makassar\"",
                              "\"Medan\"", "\"Semarang\"", "\"Surabaya\"",
                              "\"Yogyakarta\""],
            "column": "city",
            "table": "dim_customers",
            "you_filtered_for": "jakarta"
          }
        ]
```

The quoting earns its keep exactly as its comment claims: `december` against
`"December"` and `jakarta` against `"Jakarta"` are visible as case mismatches at
a glance. `maxProbes = 2` held; the note was replaced by `probeNote` rather than
joined to it.

The original `E-5` shape — `month_name = 'December'` against the space-padded
`'December '` — could not be reproduced, because migration
`006_trim_dim_date_labels.sql` fixed the data. Case mismatch is the same class:
*the literal is not what is stored*.

### Defect 3 — the fabrication mechanism's own question shape was uncovered

Found while writing the gate. The first query tried was the obvious one:

```sql
SELECT SUM(fs.sales_amount) AS total FROM fact_sales fs
JOIN dim_date dd ON dd.date_id = fs.date_id
WHERE dd.month_name = 'december'
```

```
    aggregate over no matching rows: row_count=1 rows=[map[total:<nil>]]
```

**One row, not zero.** An aggregate over an empty set returns a single all-NULL
row in Postgres, MySQL and SQL Server alike. `run_sql` tested `result.Count == 0`
for both the zero-row note and the probe, so this result got **neither**: no
note, no `available_values`, `row_count: 1`, and a row in the payload.

That is not an edge case. It is the shape of `C-1`, the question this product
was built around — *"What were our total sales last month?"* — and of `E-5`,
where the padded label made the filter match nothing and the agent answered
**IDR 1,488,000**. T-16 closed the fabrication path for zero rows; the query
shape that produced the original fabrication does not return zero rows.

Fixed with `matchedNothing`, which treats one row of all-NULLs as no data. The
distinction that makes it safe is in the test: `COUNT(*)` over an empty set
returns `0`, not `NULL`, so an honest "there are none" is never rewritten into
"nothing matched", and a row with one real value among NULLs is still data.

After the fix, the same aggregate:

```
    aggregate-over-nothing payload the model sees:
        {
          "available_values": [ { "column": "month_name", "table": "dim_date",
              "you_filtered_for": "december",
              "actual_values": ["\"August\"", "\"December\"", … ] } ],
          "columns": ["total"],
          "note": "The query succeeded but matched ZERO rows. There is no figure
                   in this result. The `available_values` field shows what the
                   filtered columns ACTUALLY contain …",
          "row_count": 1,
          "rows": [ { "total": null } ]
        }
```

Three tests added, all failing against the pre-fix payload builder.

## 5. `T-Q6`/`T-Q7` — hydration carries the recent turns · **pass** (SQL half)

Defect 1 of the roadmap's own list — memory hydration replaying the *beginning*
of long conversations — is invisible below 20 messages, which is why every test
thread and every demo hid it. This deployment has a **58-message thread**, so it
can be seen for the first time.

```
=== thread 4d5f66db: total messages ===
 total |           first_at            |            last_at
-------+-------------------------------+-------------------------------
    58 | 2026-08-04 14:18:00.263524+00 | 2026-08-07 20:10:08.377792+00

=== OLD hydration -- ListByThread(id, 20, 0): ORDER BY created_at ASC LIMIT 20 ===
 n  |   role    |                        content                        |          created_at
----+-----------+-------------------------------------------------------+-------------------------------
 18 | assistant | I'll analyze the active customers metric alert. Let me | 2026-08-04 14:26:19.518329+00
 19 | user      | [Watcher alert: Customers above 10]  The metric "Activ | 2026-08-04 14:27:00.887341+00
 20 | assistant | I'll analyze the active customers metric alert. Let me | 2026-08-04 14:27:12.950044+00

=== NEW hydration -- ListRecentByThread(id, 20): ORDER BY created_at DESC, id DESC LIMIT 20, reversed ===
 n  |   role    |                        content                        |          created_at
----+-----------+-------------------------------------------------------+-------------------------------
 20 | user      | [Watcher alert: Customers above 10]  The metric "Activ | 2026-08-04 14:37:01.072957+00
 19 | assistant | I'll analyze the active customers metric alert. Let me | 2026-08-04 14:37:29.299201+00
 18 | user      | [Watcher alert: Customers above 10]  The metric "Activ | 2026-08-04 14:38:00.619418+00

=== overlap between the two windows ===
 overlapping_messages
----------------------
                    0
```

**Zero overlap.** The old window ends at 14:27 on 2026-08-04; the new one starts
at 14:37 and runs to the thread's last message on 2026-08-07 20:10. On this
thread the agent was hydrating into a conversation that had been over for three
days. The fix is real and the ordering is correct in both directions.

`ListRecentByThread` is also what `refreshSummary` now takes
(`thread_service.go:629`), through an interface upgrade with a fall-back — so
roadmap defect 2 is fixed by the same read.

**Still owed:** that the rolling-summary *block* appears in a turn's prompt on
such a thread, and the `PRIOR_WORK_TURNS=3` vs `=0` pair. Both need a turn.

`domain.MessageRoleTool` is still written by nothing on this deployment, as
expected — no turn has run on this build:

```
   role    | count
-----------+-------
 user      |   535
 assistant |   535
```

That is the T-Q6 measurement's baseline. After the pair in §7 runs, this table
should have a third row.

## 6. The eval set — **not run, no credential**

The set is **55 cases across 13 categories**, which is the 40→55 growth `T-Q1`
claims:

```
      8 guardrail          3 zero_row_trap
      6 time_window        3 wrong_grain
      6 simple_aggregate   3 no_chart_wanted
      5 metric_registry    3 multi_source
      5 indonesian         3 follow_up
      4 grouping_topn      3 dirty_schema
                           3 chart_dashboard
```

`make eval` was **not run**: `cmd/eval` needs `LLM_API_KEY` and a control
database it can write to, and this tree has neither. The roadmap's 70–85%
prediction is unmeasured and stays unmeasured. Rule 1 of
[`eval-baseline.md`](eval-baseline.md) is therefore unsatisfied for all nine
tickets, and — since §4 above changed `run_sql`'s payload on the zero-row and
empty-aggregate paths — it is now unsatisfied for one more change than it was
this morning.

`make eval-matrix` was **not run** and was not attempted: it multiplies the
spend by the number of models, and running it before the single-model number
exists would answer the second question before the first.

## 7. What is still owed, and exactly what each needs

**Blocked on credentials, not on cost.** The tree has no `.env`. Recreating one
with a valid `LLM_API_KEY`, `ARGENTUM_JWT_SECRET`, `ARGENTUM_DSN_KEY`,
`DB_PASSWORD` and WhatsApp placeholders (see
[`report-video.md`](report-video.md) §6) unblocks every row below. Note that the
control Postgres volume was initialised with a role of `metabase` rather than
the `argentum` the current `docker-compose.yml` declares, so `DB_USER` has to
match the volume.

| Owed | Needs | Why it matters |
| ---- | ----- | -------------- |
| `POST /api/messages/:id/feedback` round trip; own message → 400; other tenant's → 404 | API booted | §2 proved storage, not the door. The 404 is the tenant boundary. |
| `POST /api/cookbook/harvest` writing an example, then a turn retrieving one | API + embedding credentials | Everything up to `client.Embed` is proven in §3. This is the write and the read. |
| Re-harvest after a thumbs-down, confirming `skipped_negative` moves | same | §3 proves the gate decides correctly; this proves the counter moves end to end. |
| `PRIOR_WORK_TURNS=3` vs `=0`, second turn calling `get_schema` or not | model spend | The T-Q6 measurement. The knob exists for exactly this pair. |
| The rolling-summary block appearing on the 58-message thread | model spend | §5 proved the read; this proves the injection. |
| `make eval` on the 55 cases | model spend | The 70–85% prediction, and now also the `run_sql` payload change from §4. |
| `make eval-matrix` across 2–3 models | model spend ×N | Only after the single-model number exists. |
| `T-Q3` before/after on chart restraint | 2 × eval | A prompt change with an argument and no number. |
| The thumbs and reason box in the transcript; `role: tool` rows staying invisible | a browser | Unchanged from the roadmap. |

## 8. What this sitting cost

About ninety minutes, **$0.00 of model spend**, and three defects. Two of the
three (§4) were found by a gate the roadmap had filed under "needs the stack" —
the cheap bucket, again, for the fourth time in this repo's record.

**The control database was left at version 55 with both tables empty.** The
verdicts this gate wrote were deleted afterwards, deliberately: a fabricated
thumbs-down is exactly the kind of row that would corrupt the first real
reliability number `T-Q2` produces, and this table's whole value is that
everything in it came from a person.

The one thing this run needed that no document names: `DOCKER_API_VERSION=1.44`.
The CLI answers `client version 1.43 is too old` to every command, which reads
as "the daemon is down" and is not. [`live-gate-backlog.md`](live-gate-backlog.md)
§1a already records this costing a day; it is repeated here because the next
person to read *this* file will hit it before they read that one.

## 9. Files changed by this gate

| File | Change |
| ---- | ------ |
| `internal/domain/query_example.go` | `CookbookCandidate.AnswerMessageID`, `VerdictKeys()` |
| `internal/adapters/postgres/cookbook_candidate_repo.go` | `LEFT JOIN LATERAL` resolving the answer |
| `internal/app/cookbook_service.go` | verdict gate reads `VerdictKeys()`; `isNegative` |
| `internal/app/cookbook_service_test.go` | fake carries two ids; new `…AsksAboutTheAnswer…` test |
| `internal/tools/empty_result_probe.go` | `whereKeyword` regex replaces the space-bounded index |
| `internal/tools/empty_result_probe_test.go` | three multi-line WHERE cases |
| `internal/tools/run_sql.go` | `matchedNothing` — the empty-aggregate shape |
| `internal/tools/run_sql_test.go` | three cases, including the `COUNT(*) = 0` distinction |
| `packages/api-types/src/domain.ts` | regenerated by `make types` |

`go build ./...`, `go vet ./...` and `go test ./internal/{tools,app,domain,guardrails,eval}/...`
all clean. One unrelated failure in `cmd/api` (`TestV1EmitsNoCORSHeaders`)
belongs to concurrent work in `middleware/cors.go` and `router.go`, untouched
here.
