# Free credit by approval (RIZ-89)

Dashboard-only `/api` routes. There is no `/v1` twin and no SDK entry.

Sign-up used to credit every new workspace $0.50 at once. Sign-up verifies
nothing, so that credit could be farmed by script. Now:

1. Sign-up files a **pending free-credit request** for `CREDITS_DEFAULT_GRANT_USD`
   (frozen on the request), naming the new admin and the client IP, and writes
   no balance. Every operator is emailed (`Free credit to approve: <workspace>`),
   when SMTP, `APP_BASE_URL` and `PLATFORM_OPERATOR_EMAILS` are all set.
2. Until an operator decides, every budget check refuses the workspace's turns
   with `402` and *"This workspace's free Argentum credit is waiting for
   approval, so I can't run that yet. …"* on the dashboard and the team's own
   channels. `/v1` and the embed widget answer exactly as before, with
   `credits_exhausted` and the usual sentence. A workspace on its own LLM key is
   never refused.
3. An operator approves (`POST /operator/free-credit/:id/approve`) or rejects.
   Either decision drops the 60-second budget cache, so the next message runs,
   or is told *"… was not approved …"*. The admin who signed up gets *Your
   Argentum free credit is ready* when credit was added.

A company that never received a grant (from before RIZ-79, never chatted) gets
its request at its next budget check, with no IP and no email. A company that
already holds a grant is untouched and never gets a request.

Statuses: `pending` → `approved` | `rejected`. One request per company, ever. A
rejection is final in v1. The workspace can still top up.

`CREDITS_GRANT_REQUIRES_APPROVAL=false` (exactly `false`; anything else keeps
approval on) restores RIZ-79 on every process: the grant at sign-up, the lazy
grant at the first check, no requests. Requests filed before stay listed.

## Operator routes

Only users whose login email is on `PLATFORM_OPERATOR_EMAILS`, the top-up
queue's gate. Everyone else gets `403 {"error":"operators only"}`.

Since RIZ-89 no account can be *created* with an operator's address. Sign-up
answers `409 {"error":"email already in use"}`, an invite answers as an
address in use does, and accepting an older invitation to one answers as a dead
link. At boot the api warns `PLATFORM_OPERATOR_EMAILS entry N has no active
account` for each listed address with no active account. Create the account
first, then list it.

### `GET /operator/free-credit?view=review|reviewed`

`review` (default): every pending request, oldest first. `reviewed`: the last
50 decisions, newest first.

```json
{"requests": [{
  "id": "…", "company_id": "…", "status": "pending", "amount_micro_usd": 500000,
  "created_at": "…", "reviewed_at": null,
  "company_name": "Toko Segar", "requested_by_email": "dina@tokosegar.id",
  "reviewed_by_email": "", "has_data_source": true, "same_ip_signups_7d": 0
}]}
```

`has_data_source` is whether the workspace has connected a database.
`same_ip_signups_7d` counts other sign-ups from the same IP within seven days
on either side of this one. It is `0` for a request with no IP. The IP itself is
never sent. `requested_by_email` is `""` for a request a budget check filed.

### `POST /operator/free-credit/:id/approve`

`200 {"request": {…}, "added_micro_usd": 500000, "balance_micro_usd": 500000}`.
In one transaction the request moves from `pending` to `approved` and the credit
is written:

| The company's credit row | Result | `added_micro_usd` |
|---|---|---|
| none, or grant 0 (any balance) | balance = grant = amount | amount |
| topped up (`last_topup_at` set) | balance += amount, grant = `GREATEST(balance, amount)` | amount |
| grant > 0, never topped up (granted while approval was off, or by an older pod mid-deploy) | unchanged | 0 |

A second approval is answered `409 {"error":"This request was already reviewed."}`,
whether it comes from a double click or a second operator. An unknown id is
answered `404 {"error":"no such request"}`.

### `POST /operator/free-credit/:id/reject`

No body. `200 {"request": {…}}` with `status: "rejected"`. No balance changes.
`409` / `404` as above.

## The dashboard

- `/operator/free-credit` (tabs beside `/operator/top-ups`, in no navigation).
- New-chat screen: a notice while free credit is pending or rejected. Admins
  also see a link to `/top-up`.
- Usage → Overview and `/top-up`: the credit card says `$0.50 free credit
  waiting for approval` or `Free credit not approved. …` and shows no bar.
