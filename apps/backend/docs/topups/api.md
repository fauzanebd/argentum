# Credit Top-up API (RIZ-83)

Dashboard-only `/api` routes. There is no `/v1` twin and no SDK entry: a top-up
is a person transferring money, and an integration has no business initiating
one.

All routes take the dashboard session (`Authorization: Bearer <access token>`).
Money is integers throughout: rupiah in whole rupiah, credit in micro-USD
(÷ 1 000 000 for dollars), and a rate as rupiah per dollar × 10 000 (the `_e4`
suffix — `173495700` is Rp 17.349,57 per $1).

## How a top-up works

1. An admin asks for an amount (`POST /topups`). The server prices it — the
   market USD→IDR rate plus the spread, or the fallback — freezes that rate and
   the credit on the request, and adds a random 3-digit code so the exact
   amount to transfer is unique among open requests (Rp 100.000 → Rp 100.347).
2. The admin transfers exactly that amount and uploads the receipt
   (`POST /topups/:id/proof`). The request moves to `in_review` and every
   operator is emailed.
3. An operator matches the exact amount on the bank or GoPay statement and
   approves (`POST /operator/topups/:id/approve`) or rejects with a reason. An
   approval adds the frozen credit in one transaction, exactly once, and drops
   the 60-second budget cache so the next message runs.

Statuses: `awaiting_payment` → `in_review` → `approved` | `rejected`; an unpaid
request becomes `expired` 48 hours after it was made (checked lazily, before
every create and list) or `cancelled` by its admin. A workspace has at most one
open (`awaiting_payment` or `in_review`) request.

## Workspace admin routes

Admin only; a member gets `403 {"error":"admin only"}`.

### `GET /topups/options`

```json
{
  "enabled": true,
  "min_idr": 20000, "max_idr": 5000000, "step_idr": 1000,
  "presets_idr": [50000, 100000, 250000],
  "preview_idr_per_usd_e4": 173495700,
  "expires_after_hours": 48,
  "payees": [{"id": "bri", "label": "BRI", "account_number": "0000000000", "account_name": "Payee Name"}]
}
```

`preview_idr_per_usd_e4` is the effective rate a request made now would freeze;
the page previews `⌊amount × 10¹⁰ ÷ rate⌋` micro-USD with it. The market rate
and the spread are never sent to a tenant. `enabled` is `false` and `payees` is
`[]` when `TOPUP_PAYEES` is unset.

### `POST /topups`

Body `{"amount_idr": 150000}` — a JSON integer. Every other field is ignored.

`201` with the request:

```json
{
  "id": "…", "status": "awaiting_payment",
  "amount_idr": 150000, "unique_code": 347, "expected_amount_idr": 150347,
  "credit_micro_usd": 8645747,
  "payee_id": null, "proof_uploaded_at": null,
  "expires_at": "…", "created_at": "…", "reviewed_at": null, "reject_reason": null
}
```

| Status | Body |
|---|---|
| 400 | `The minimum top-up is Rp 20.000.` / `The maximum top-up is Rp 5.000.000.` (bounds from config) / `Enter an amount in whole thousands of rupiah.` (not a thousand, a string, a fraction, or missing) |
| 409 | `You already have a top-up in progress.` |
| 503 | `Top-up is not available right now. Please try again later.` (no payees, or no free code after 20 draws) |

### `POST /topups/:id/proof`

Multipart: `file` (PNG, JPEG, WebP or PDF by its bytes, at most 5 MB) and
`payee_id`. Stored at `topups/<company>/<topup>/<uuid>.<ext>`; a replaced
proof's previous object is kept. `200` with the request in `in_review`.

| Status | Body |
|---|---|
| 400 | `Choose the account you paid to.` |
| 404 | another workspace's id, or no such id |
| 409 | `This top-up is closed. Start a new one.` (expired, cancelled or decided) |
| 413 | `That file is larger than 5 MB. Upload a smaller screenshot.` |
| 415 | `Upload a PNG, JPG, WebP or PDF file.` |
| 503 | `Proof upload is not available right now. Try again later.` (no object storage, or it failed) |

### `POST /topups/:id/cancel`

`200` with the request in `cancelled`. `409 Only a top-up waiting for payment can be cancelled.`
for any other status; `404` for another workspace's id.

### `GET /topups`

`{"topups": [Topup]}` — the workspace's last 20, newest first. No storage key,
no URL and no rate is in any element.

## Operator routes

Only users whose login email is in `PLATFORM_OPERATOR_EMAILS` (comma-separated,
case-insensitive; empty admits nobody). Everyone else — company admins
included — gets `403 {"error":"operators only"}`. Cross-tenant by design.

### `GET /operator/topups?view=review|reviewed`

`review` (default): every workspace's `in_review` top-ups, oldest proof first.
`reviewed`: the last 50 `approved` or `rejected`, newest first. Each element is a
Topup plus:

```json
{
  "company_name": "Toko Segar", "requested_by_email": "…", "reviewed_by_email": null,
  "payee_label": "BRI",
  "fx_source": "market", "fx_market_idr_per_usd_e4": 165234000, "fx_rate_date": "2026-10-12",
  "fx_spread_bp": 500, "fx_effective_idr_per_usd_e4": 173495700,
  "duplicate_of": {"company_name": "Kopi Senja", "expected_amount_idr": 250140, "created_at": "…"}
}
```

On a `fallback` request the market rate, date and spread are `null`.
`duplicate_of` names the earliest other top-up whose proof is byte-identical
and was uploaded first — a flag, never a block.

### `GET /operator/topups/:id/proof`

`{"url": "…", "content_type": "image/png", "expires_in": 300}` — a presigned
URL that stops working after five minutes. Sent with `Cache-Control: no-store`.

### `POST /operator/topups/:id/approve`

`200 {"topup": Topup, "balance_micro_usd": 5120000}`. In one transaction: the
request moves from `in_review` to `approved`, then the balance becomes
`balance + credit` and the grant `GREATEST(balance + credit, credit)`, with
`last_topup_at` set. A workspace with no credit row gets balance = grant =
credit. `409 This top-up was already reviewed.` when it is not in review — a
double click or a second operator; `404` when the id does not exist.

### `POST /operator/topups/:id/reject`

Body `{"reason": "…"}`, 1–300 characters after trimming; the customer sees it.
`200` with the request in `rejected`. `400 Write a reason the customer will see.`
for an empty one, `400 Keep the reason to 300 characters or fewer.` for a long
one, and the same `409` as approve.

## Configuration

See `.env.example` § "Manual credit top-up". `TOPUP_PAYEES` holds the accounts
and is never committed; unset, it is the kill switch. A configuration that
does not parse logs an error at boot and leaves top-up off; it never stops the
API. Every fallback-priced request logs `topup fx fallback` at warn with its
reason and counts `argentum_topup_fx_fallback_total{reason}`.
