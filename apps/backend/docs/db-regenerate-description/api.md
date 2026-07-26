# Regenerate Database Connection Description

Re-runs the AI description generator for one of your registered databases. Use this when:

- The database schema changed and the existing description is stale.
- You want to refresh an auto-generated description.
- You want to replace a description you wrote manually with a fresh AI-generated one.

The description is what the AI assistant uses to decide which database to query when you have more than one source connected, so keeping it accurate helps the assistant pick the right database.

---

## Endpoint

```
POST /api/connections/{id}/regenerate-description
```

**Authentication:** required (same auth as all other connection endpoints).
**Path parameter:** `id` — the UUID of the connection to regenerate.
**Request body:** none.

---

## Behavior

When you call this endpoint:

1. The server connects to the target database and reads its schema (tables and columns).
2. It sends a compact summary of the schema to a small AI model.
3. The AI returns a one-sentence description of what the database appears to contain.
4. The new description is saved on the connection.
5. The full, updated connection record is returned.

The call is **synchronous** — the response only comes back after the AI has finished. Expect this to take roughly **5–30 seconds**, depending on the size of the schema. The maximum time the server will wait is **90 seconds**.

The new description always replaces the old one, even if you had previously written it yourself. After regeneration, the connection's `description_source` is set to `"auto"`. If you want to set a custom description again afterwards, use the `PATCH /api/connections/{id}` endpoint.

---

## Successful response

**Status:** `200 OK`
**Body:** the updated connection record.

```json
{
  "id": "0f8b9c2e-4a1d-4f9e-9a3b-7a5e0d8e1c33",
  "company_id": "5b4cda7e-1c2a-4f8a-9c11-2e6f0a3b4d22",
  "db_type": "postgres",
  "is_default": true,
  "label": "crm",
  "description": "Customers, orders, and refunds for an online retail store.",
  "description_source": "auto",
  "metabase_database_id": 7,
  "created_at": "2026-04-12T10:32:11.213Z",
  "updated_at": "2026-05-10T14:18:02.847Z"
}
```

Field notes:

| Field | What it means |
|-------|---------------|
| `description` | The new AI-generated text (one short sentence). |
| `description_source` | Always `"auto"` after a successful regeneration. |
| `updated_at` | Reflects the regeneration time. |
| Other fields | Unchanged from before the call. |

---

## Errors

| Status | When | Body |
|--------|------|------|
| `403 Forbidden` | The connection id belongs to a different organization than the one you are authenticated as. | `{"error": "unauthorized"}` |
| `404 Not Found` | No connection exists with that id. | `{"error": "not found"}` |
| `500 Internal Server Error` | The AI service is not configured on this deployment, or the underlying database could not be reached for schema introspection, or the AI returned an empty response. | `{"error": "<details>"}` |
| `504 Gateway Timeout` | Generation did not complete within 90 seconds. The connection record is unchanged; you may retry. | `{"error": "regeneration timed out; try again"}` |

A failed call leaves the existing description in place — nothing is partially written.

---

## Examples

### Curl

```bash
curl -X POST \
  -H "Authorization: Bearer $TOKEN" \
  https://api.example.com/api/connections/0f8b9c2e-4a1d-4f9e-9a3b-7a5e0d8e1c33/regenerate-description
```

### Typical flow

```text
1. GET    /api/connections                       → see your sources, note the id
2. POST   /api/connections/{id}/regenerate-description
3. (wait ~5–30 s)
4. ←      200 OK with the updated connection
5. GET    /api/connections                       → verify the new description
```

---

## Notes

- This endpoint does not change the database connection itself — only the description text saved alongside it.
- The connection must already be reachable from the server (i.e. the DSN is valid). If the server can't open the connection, the call fails with 500.
- One id per call. There is no batch form.
- Manual descriptions are overwritten on purpose — calling this endpoint is an explicit opt-in. If you want to keep a manual description, do not call this endpoint.
