# Playbook: Add a Control-Plane Migration

**Time:** 0.5d for the migration + repository. Longer if the schema needs design.

The control plane is the only database Argentum migrates. Tenant analytical
databases are customer property and are never migrated — see
[`../workspace-context.md`](../workspace-context.md) §7.

---

All paths below are relative to `apps/backend/` unless shown otherwise.

## Step 0 — Claim the number

```bash
ls apps/backend/migrations/control/ | tail -3
```

Last applied is `020_thread_lark`. Sprint 1 pre-assigns 021–026 in
[`../../plan/01-tickets.md`](../../plan/01-tickets.md) — check that table before
claiming.

**Only one agent may hold a number at a time.** Two migrations with the same
number desync every environment, and the fix is manual.

## Step 1 — Design for forward compatibility

`cmd/api` applies migrations on boot. During a rolling deploy the **new schema
meets old code**. Therefore:

| Safe | Unsafe |
| ---- | ------ |
| `CREATE TABLE` | `DROP TABLE` |
| `ADD COLUMN` nullable, or with a default | `ADD COLUMN NOT NULL` without a default |
| `CREATE INDEX` | `DROP COLUMN` |
| Widening a type (varchar → text) | Narrowing a type |
| Adding an enum value | Removing an enum value |
| Adding a nullable FK | `RENAME` anything |

Removing a column takes two releases: stop reading it, ship; drop it, ship.

## Step 2 — Write both files

`apps/backend/migrations/control/NNN_snake_case.up.sql`:

```sql
-- NNN_my_thing: <one line on what this enables>
--
-- <Why the schema is shaped this way. Note any denormalization and why.
--  Note anything a future reader would otherwise assume is a mistake.>

CREATE TABLE my_things (
    id          uuid PRIMARY KEY,
    company_id  uuid NOT NULL REFERENCES companies(id) ON DELETE CASCADE,
    source_id   uuid REFERENCES db_connections(id) ON DELETE CASCADE,
    name        text NOT NULL,
    config      jsonb NOT NULL DEFAULT '{}'::jsonb,
    enabled     boolean NOT NULL DEFAULT false,
    created_at  timestamptz NOT NULL DEFAULT now(),
    updated_at  timestamptz NOT NULL DEFAULT now(),
    UNIQUE (company_id, name)
);

-- Every tenant-scoped read filters on company_id; this index is not optional.
CREATE INDEX idx_my_things_company ON my_things (company_id, created_at DESC);
```

`apps/backend/migrations/control/NNN_snake_case.down.sql`:

```sql
DROP TABLE IF EXISTS my_things;
```

**Both files, always.** Migrations 001–014 lack down files and that is technical
debt (finding Q-7), not a precedent.

### Schema conventions in this codebase

| Convention | Detail |
| ---------- | ------ |
| Primary keys | `uuid`, generated in Go, not by the database |
| Tenant scoping | `company_id uuid NOT NULL REFERENCES companies(id) ON DELETE CASCADE` — on every tenant-owned table, no exceptions |
| Timestamps | `timestamptz`, `NOT NULL DEFAULT now()` |
| Flexible config | `jsonb NOT NULL DEFAULT '{}'::jsonb` |
| Encrypted values | `text` column named `*_encrypted`, ciphertext from `internal/crypto` |
| Booleans | `NOT NULL` with an explicit default |
| Enum-ish columns | `text` + application-level validation, not a PG enum (enums are painful to extend under forward-compat rules) |
| Indexes | One per real query pattern. `(company_id, created_at DESC)` is the common shape |

## Step 3 — Domain type and repository interface

`internal/domain/my_thing.go`:

```go
package domain

// MyThing is <what it represents in the business domain>.
type MyThing struct {
    ID        string                 `json:"id"`
    CompanyID string                 `json:"company_id"`
    SourceID  string                 `json:"source_id,omitempty"`
    Name      string                 `json:"name"`
    Config    map[string]interface{} `json:"config,omitempty"`
    Enabled   bool                   `json:"enabled"`
    CreatedAt time.Time              `json:"created_at"`
    UpdatedAt time.Time              `json:"updated_at"`
}

// MyThingRepository is the persistence contract for my_things.
type MyThingRepository interface {
    Create(ctx context.Context, t *MyThing) error
    GetByID(ctx context.Context, companyID, id string) (*MyThing, error)
    ListByCompany(ctx context.Context, companyID string) ([]*MyThing, error)
    Update(ctx context.Context, t *MyThing) error
    Delete(ctx context.Context, companyID, id string) error
}
```

**Note `companyID` on `GetByID` and `Delete`.** Every tenant-scoped method takes it
and every query includes it. This is the isolation boundary, enforced by
convention because nothing else enforces it.

## Step 4 — Implement the repository

`internal/adapters/postgres/my_thing_repo.go`. Copy the shape of an existing repo —
`thread_repo.go` for a straightforward one, `scheduled_task_repo.go` for one with
child rows.

```go
func (r *MyThingRepo) GetByID(ctx context.Context, companyID, id string) (*domain.MyThing, error) {
    const q = `
        SELECT id, company_id, source_id, name, config, enabled, created_at, updated_at
        FROM my_things
        WHERE company_id = $1 AND id = $2`
    // company_id first: a cross-tenant id must return not-found, never a row.
    row := r.db.QueryRowContext(ctx, q, companyID, id)
    // ... scan; map sql.ErrNoRows to domain.ErrNotFound
}
```

- Parameterized queries only.
- `sql.ErrNoRows` → `domain.ErrNotFound`, never returned raw.
- `jsonb` scans into `[]byte`, then `json.Unmarshal`.

## Step 5 — Wire it

- `cmd/api/bootstrap.go` — construct the repo, pass to the service that needs it
- `cmd/worker/main.go` — same, if the worker needs it
- Both processes if both do. They construct independently; there is no shared container.

## Step 6 — Verify

```bash
export DATABASE_URL="postgres://analytics:...@localhost:5432/argentum?sslmode=disable"

migrate -path apps/backend/migrations/control -database "$DATABASE_URL" up
migrate -path apps/backend/migrations/control -database "$DATABASE_URL" down 1
migrate -path apps/backend/migrations/control -database "$DATABASE_URL" up

# and the real path
cd apps/backend && go run ./cmd/api    # watch the migration log lines
```

Then repository tests:
```bash
go test ./internal/adapters/postgres/... -run TestMyThing -v
```

Must include a **cross-tenant test**: create a row under company A, `GetByID` it
with company B's id, assert not-found. This is the test that catches the missing
`company_id` predicate — the single most damaging bug class in a multi-tenant app.

---

## Gate

- [ ] Both `.up.sql` and `.down.sql` exist
- [ ] `up → down → up` round-trip clean, output pasted
- [ ] `go run ./cmd/api` applies it cleanly, log line pasted
- [ ] Forward-compatible — state which rule from Step 1 applies
- [ ] Cross-tenant repository test passes, output pasted
- [ ] Indexes cover the query patterns added
- [ ] Number was not already claimed

## Common mistakes

| Mistake | Consequence |
| ------- | ----------- |
| Editing an already-applied migration | Environments desync; no automatic fix |
| Skipping `.down.sql` | Cannot roll back a bad deploy |
| `NOT NULL` without a default | Migration fails on a non-empty table |
| Omitting `company_id` from a query | Cross-tenant data leak |
| Forgetting `ON DELETE CASCADE` | Orphan rows accumulate; company deletion fails on FK |
| No index on `company_id` | Sequential scans as soon as there is real data |
| Wiring in `bootstrap.go` but not `cmd/worker/main.go` | Nil dependency panics on the first task |
