# Email — how this product reaches somebody who is not looking at it

The plan is
[`../plan/10-freshness-metrics-email-roadmap.md`](../plan/10-freshness-metrics-email-roadmap.md)
Track C (`T-F6`, `T-F7`); the argument is
[`../research/07-feature-candidates.md`](../research/07-feature-candidates.md)
§1c. **Both tickets are built.**

| Ticket | Status |
| --- | --- |
| `T-F6` Email, and invites that arrive | **built 2026-09-11, unit-gated. No message has ever been sent — §5** |
| `T-F7` Watchers arrive by email | **built 2026-09-11, unit-gated. No migration needed — §4** |

---

## 1. The gap

`grep -rin "smtp\|sendgrid\|mailgun\|resend\|postmark"` over `apps/backend` and
`docs` returned **nothing**. The coverage matrix's Channels table had carried
`Email ❌ Not implemented` since the day it was written, and three shipped
features were quietly incomplete because of it:

- **`T-04`'s team invite produced a link and handed it to the inviting admin.**
  The dashboard said so out loud — *"Argentum does not send email yet, so pass
  this on yourself"* — which means onboarding a colleague was a
  copy-paste-into-WhatsApp step in a product that sells automation.
- **`T-H6`'s data export and erasure record had nowhere to go.** A UU PDP
  erasure request is made by a person, and the completion record could not
  reach them.
- **A tenant with no team chat could receive no push at all.** Watchers deliver
  to Slack, Discord, Lark and WhatsApp — every delivery channel this product
  had was a team-chat product — and not to the one address every business
  person has. For a great many Indonesian SMBs that is the whole push feature.

## 2. What was built

| Piece | Where |
| --- | --- |
| `Sender`, the SMTP implementation, the no-op | `internal/email/email.go` |
| RFC 5322 rendering, multipart, CRLF, dot-stuffing | `internal/email/render.go` |
| The invite and alert templates | `internal/email/templates.go` |
| `InviteMailerAdapter` — company name, inviter, accept URL | `internal/app/invite_mailer.go` |
| `AlertMailerAdapter` — a breach to an inbox | same file |
| `TeamService.WithMailer`, and `Emailed` on the result | `internal/app/team_service.go` |
| `domain.ChannelEmail`, save-time ref validation, delivery | `internal/domain/thread.go`, `internal/app/watcher_service.go` |
| Config, and the `.env.example` block | `internal/config/config.go`, `apps/backend/.env.example` |
| "Sent to …" vs "Invitation link for …" | `apps/dashboard/src/features/settings/team-tab.tsx` |
| Email in the watcher channel picker | `apps/dashboard/src/features/watchers/watcher-model.ts` |

## 3. Decisions worth the words

**This package sends. Nothing receives.** Inbound email is a new
untrusted-input surface with its own threat model — `T-H8`'s argument — and it
is deliberately out of the track. `domain.ChannelEmail` says so in its own doc
comment, because it is the only `Channel` constant that is one-way and the next
reader will otherwise assume a thread can be opened on it.

**SMTP rather than a provider SDK**, for two reasons that are both about who can
run this: every operator can point it at something they already have, and a test
deployment can point it at a local catcher with no account anywhere. A provider
goes behind the same `Sender` later without touching a caller — the arrangement
`whatsapp.Provider` already established for the Meta and Twilio backends.

**A deployment with nothing configured gets a no-op sender, one startup log
line, and `Enabled() == false`.** It is not an error type: a build with no relay
is a valid deployment and is what every existing one is. What it must not do is
*look* like it worked — which is the defect `T-P8`'s embedder resolver shipped,
returning no client and no error while the boot log printed "enabled".

**`APP_BASE_URL` is required for invites and optional for alerts**, and the
asymmetry is the point. An invitation *is* a link; a message whose entire
purpose is one link, carrying a link to nowhere, makes the recipient conclude
the product is broken rather than that their admin has not finished setting it
up. So SMTP without a base URL sends **no invitations at all** and the admin
keeps copying links — the behaviour they already have. An alert is a paragraph
the agent wrote; it stands on its own and simply omits the "open in Argentum"
line.

**The token is in the response whether or not it was mailed.** `emailed: false`
covers three situations an admin cannot tell apart and does not need to — no
relay, no base URL, a send that failed — and in all three the answer is the same
as it was before this product could send anything. Returning an error instead
would roll a perfectly good invitation back because a relay was briefly
unreachable.

**One message to every watcher recipient, not one each.** The recipients are
colleagues watching the same number, and N separate emails about one breach is
how a team mutes a watcher.

## 4. What the build found

**The email ref is validated at save time, not at send time.** A watcher fires
at 03:00. An address with a typo in it should be a 400 in front of the person
who typed it, not a `"failed"` row somebody reads the next morning. The check is
deliberately shallow — a full RFC 5322 validator rejects addresses that work,
and the only mistake worth catching in a form is a name, a phone number, or a
Slack channel pasted into the wrong field.

**The recipient list is capped at 20.** A watcher fires on a cron, so an
unbounded list is an unbounded send rate against one relay — and a list that
long is a mailing list the tenant should own rather than one this product should
store.

**`T-F7` needed no migration**, which the ticket said it would (`082`).
`watcher_channels` is a JSONB column, so a new channel value is a constant and a
`case`, not a schema change. The roadmap now says so.

**The plain-text part has to come first.** A client picks the *last* part of a
`multipart/alternative` it can display, so text-then-HTML shows HTML where it
can and falls back where it cannot. Reversed — which is the order that reads
naturally when writing the function — every modern client would show the plain
text. Pinned by a test.

**A bare `.` on its own line ends an SMTP message.** A body containing one would
be truncated there and the rest interpreted as SMTP commands. `net/smtp`'s data
writer does the stuffing too, but doing it in `Render` means its output is a
valid message on its own rather than only when it goes through that one writer.

**A non-ASCII subject has to be encoded.** Indonesian subject lines are the
normal case on this deployment, and a raw high byte in a header is mojibake in
half the clients that see it. `mime.QEncoding`, pinned by a test that asserts
the raw character is *absent*.

**The envelope sender is the bare address, not the header value.** SPF is
checked against the envelope, and handing a relay `Argentum <no-reply@…>` is how
a deployment's entire mail output starts bouncing with a syntax error nobody
reads.

**The accept URL has to match what the dashboard builds.** `team-tab.tsx`
constructs `${origin}/accept-invite?token=${encodeURIComponent(token)}`. If the
two disagree, half a workspace's invitations land on a different route from the
other half. A test asserts the shape *and* that the token is escaped — a raw `+`
in a query value reads as a space, which turns a valid single-use token into a
404 that looks exactly like an expired invite.

**Both name lookups are decoration and fail open.** A company name or inviter
that cannot be read leaves the template's fallback in place. An invitation that
arrives slightly less personal is better than one that does not arrive because a
database was briefly busy.

## 5. What is owed

**No message has ever been sent.** Every property above is proven against a
recording sender; nothing has opened a TCP connection to a relay. The whole
package is unit-gated and **zero percent live-gated**, and this is the arm that
would find the defects — it is a new protocol surface, which is the category
this repo's own delivery log says the live half always catches something in.

The gate is cheap and needs no production anything:

1. Run a local SMTP catcher (MailHog, Mailpit — anything on a port).
2. `EMAIL_ENABLED=true SMTP_HOST=localhost SMTP_PORT=1025
   SMTP_FROM='Argentum <no-reply@test>' APP_BASE_URL=http://localhost:5173`
3. Invite somebody from Settings → Team. **Read the actual message**: does the
   button render, does the link work when clicked, does the subject show
   correctly, does it survive a plain-text client.
4. Point a watcher at the email channel with two addresses and force a breach.
   Expect **one** message with two recipients, and `delivered` in the event's
   delivery status.

Filed in [`live-gate-backlog.md`](live-gate-backlog.md) §1 — the bucket that
needs the stack and nothing else, which is the bucket that has paid sixteen
times out of sixteen.

**Deliverability is not tested and cannot be, here.** SPF, DKIM and DMARC are an
operator's DNS records, not this package's code. What this package can get wrong
— the envelope sender, the encoded subject, the part order — is covered above.
What it cannot control is whether a given relay's reputation lands the message
in an inbox, and no amount of unit testing will say.

**`T-H6`'s export and erasure still do not email anything.** The primitive now
exists and that consumer has not been wired: it needs a decision about *what*
gets attached to an erasure confirmation, which is a compliance question rather
than a plumbing one. Recorded here rather than done quietly.
