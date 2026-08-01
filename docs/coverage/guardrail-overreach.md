# T-07b · Guardrail over-reach — coverage

**Status: PARTIAL (2026-08-02).** The two reported rule false-positives (Q-4, Q-6)
are fixed at the policy level and covered by the golden suite. The activation and
per-company parts are **not** done — they are coupled to a `make eval` regression
run that needs a live LLM, and shipping activation without them would regress
production by over-redacting.

## Done, and verified by `go test ./internal/guardrails/...`

- **`block_system_prompt_leak` narrowed (Q-6).** The old pattern matched the bare
  phrase `you are an ai`, which a normal answer to "what can you do?" contains, so
  the rule refused legitimate self-description. It now requires first-person
  disclosure shape (`my instructions are…`, `my system prompt says…`). New golden
  cases assert the leak shapes still block and that "I am an AI assistant…", "You
  are an AI assistant…" and "I can't share my system prompt" now pass.
- **`redact_nik` narrowed and made reachable (Q-4).** The old pattern was a bare
  `\b\d{16}\b` that blanked any sixteen-digit run (order ids, account numbers). It
  now fires only when a `nik`/`ktp`/`no. identitas` label sits within ten
  non-digit chars of the number, and the replacement keeps the label. It was moved
  **ahead of `redact_credit_cards`** so a labelled NIK reads as a NIK, not a card.
  `TestRedactNIKFiresOnLabelledContextOnly` asserts both directions; the old
  `TestRedactNIKIsShadowedByTheCreditCardRule` (which pinned the shadowing bug) is
  replaced.

## Not done — the rest of T-07b

- **Output rules are still not applied on the streaming path.** The ticket's stated
  prerequisite ("the rules have never run") is unaddressed. The seam is
  `ChatRunner` (the same place `guardrails.CheckFabrication` runs, T-16): the runner
  would hold a `*guardrails.Analytics` and call `ProcessOutput` on the assembled
  response before the final event. It is not wired here because activating
  redaction globally, without the per-company mode below, would actively over-redact
  legitimate BI output — the exact Q-4 failure, now switched on.
- **`companies.pii_redaction_mode` (`strict`|`contact_ok`|`off`) not added.** Needs
  a migration, a column, and the mode plumbed from the turn's company into the
  `ProcessOutput` call so `redact_emails`/`redact_phone_numbers` can be skipped
  under `contact_ok`/`off`. This and the activation must land together.
- **`make eval` before/after not run.** Switching output rules on is a
  behaviour change on every turn; the ticket requires an eval run on both sides.
  Needs a live LLM, unavailable in this session.

**Next step:** wire activation + the per-company mode as one atomic change, then run
`make eval` on both sides and paste the scores. Until then production behaviour is
unchanged (output rules still dormant); the YAML is merely correct for when they
are switched on.
