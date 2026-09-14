# Voice — a question said out loud (roadmap 11, Track C)

The record for [`../plan/11-voice-and-exact-computation-roadmap.md`](../plan/11-voice-and-exact-computation-roadmap.md)
Track C: `T-W7` (speech in), `T-W8` (speech out), `T-W9` (the microphone and the player). Track A,
exact arithmetic, has its own record in [`exact-computation.md`](exact-computation.md).

**Status, 2026-09-14:** `T-W7` built, `make check` green (74 Go packages `ok`, lint `0 issues`, 88
dashboard tests), unit-gated, and its free arms run live on a scratch stack (§1f). `T-W8` and `T-W9` are not built. No speech provider key exists on this machine, so nothing
here has transcribed a real voice.

---

## 1. `T-W7`, and a transcript that is not a message

**Built 2026-09-14.** Migration `087`. No tool and no prompt change, so no `make eval` is owed.

A person in a conversation records a question. The dashboard sends the recording to
`POST /api/threads/:id/voice` and gets back what was heard:

```json
{"transcript": "berapa stok gudang barat minggu ini", "language": "id", "seconds": 2.75,
 "clip_id": "0a571cf1-…", "expires_at": "2026-09-21T09:57:24Z"}
```

Nothing else happens. No message is written and no turn starts. The transcript goes in the
composer, and the person sends it, edits it or throws it away (roadmap 11, decision 13).

### 1a. What was built

| Piece | Where |
| --- | --- |
| `speech.Transcriber` (`Transcribe`, `Enabled`, `Model`), the nop, and `New` — never an error, one log line saying why voice is off | `internal/speech/speech.go` |
| One client for the `/audio/transcriptions` request Groq and OpenAI both speak: `verbose_json`, temperature 0, the language hint, a filename with the extension the provider decodes by | `internal/speech/compat.go` |
| `speech.Accept`: the declared type **and** the file's first bytes, for WebM, Ogg, MP4, MP3, WAV and FLAC. `speech.NormalizeLanguage` | `internal/speech/speech.go` |
| `voice_clips`: object key, type, size, seconds, transcript, language, model, `expires_at`. `thread_id` and `user_id` are `SET NULL` | `migrations/control/087_voice_clips.{up,down}.sql` |
| `domain.VoiceClip`, `VoiceClipRepository`, `VoiceClipKeyPrefix` | `internal/domain/voice_clip.go` |
| `VoiceClipRepo`: a create that checks the conversation's company inside the insert, the sweep's cross-company delete list, two scoped deletes | `internal/adapters/postgres/voice_clip_repo.go` |
| `app.VoiceService`: limits, the credit check, the language hint, the provider call, metering, keeping the clip | `internal/app/voice_service.go` |
| `app.VoiceClips`: keep, `Sweep`, `EraseCompany` — the half the worker and the erasure need without a transcriber | the same file |
| `UsageEventSpeechTranscription`, `RecordTranscription`, per-model rates per hour of audio | `internal/domain/usage.go`, `internal/app/usage_speech.go` |
| `RetentionService.WithVoiceClips`: an erasure fails unless the clips went too | `internal/app/retention_service.go` |
| `VoiceHandler` and `VoiceTranscriptionResponse` | `internal/transport/http/handlers/voice.go`, `wire.go` |
| The route in `apiPolicy` (member) and **the first entry in `capabilityPolicy`** (`voice`). Registered only when `VoiceService.Enabled()` | `cmd/api/policy.go`, `router.go`, `bootstrap.go` |
| `voice:sweep` on `SPEECH_SWEEP_CRON`, hourly at :17, run whether or not speech is enabled | `internal/queue/tasks.go`, `cmd/worker/main.go`, `voice_sweep.go`, `internal/bootstrap/stack.go` |
| `SPEECH_ENABLED`, `_PROVIDER`, `_API_KEY`, `_BASE_URL`, `_STT_MODEL`, `_TIMEOUT_SECS`, `_MAX_CLIP_SECONDS`, `_RETENTION_DAYS`, `_SWEEP_CRON` | `internal/config/config.go`, `.env.example` |
| Voice's toggle copy says what a grant changes on screen today: nothing, until the microphone | `apps/dashboard/src/features/settings/access.ts` |
| The route documented | `apps/backend/docs/api.md` §Voice |
| `VoiceClip`, `VoiceTranscriptionResponse`, `UsageEventSpeechTranscription` | `packages/api-types` (generated) |

**The order a request is checked in:**

| Check | Refusal |
| --- | --- |
| Speech usable on this deployment | The route is not registered: the router's `404`, before authentication |
| Role (member), then the `voice` capability | `403 {"capability": "voice"}`, before a byte of the body is read |
| The conversation belongs to the caller's company, and the caller may read it (`T-Z10`) | `404`, before the body is read |
| The body at most `MaxClipBytes` | `413`, "a recording must be 60 seconds or shorter" |
| `duration_ms` present, and at most `SPEECH_MAX_CLIP_SECONDS` | `400`; `413` with the same sentence |
| `language`, if given, a two-letter code | `400` |
| The declared type accepted and the bytes that type | `415` |
| Credits | `402`, before the provider |
| The provider | `502`, "…try again, or type your question". Nothing kept, nothing billed |

### 1b. Decisions worth the words

- **Not registering the route is the "voice is off" answer.** A route that existed and refused would
  answer an ungranted person `403 capability: voice` on a deployment where no grant could help. The
  router's `404` says the true thing to everyone.
- **One implementation, two providers.** Research 08 §2a says to pick on Indonesian accuracy, and §6
  says nobody has measured it. So the client speaks the request both candidates accept, and the
  measurement changes `SPEECH_PROVIDER`, not code.
- **The byte cap is the limit that bounds a bill.** See §1d on duration. It is 384 kbit/s times the
  seconds limit, plus 64 KiB for the container. That is above what a browser writes for a voice, and
  under the 25 MB both providers refuse at, which is also why `SPEECH_MAX_CLIP_SECONDS` stops at 500.
- **Billed on the provider's measured length.** `verbose_json` returns `duration`, which no client
  wrote. The declared length is used only when the provider reports none, and the Info line says
  `measured: false` when that happens. A clip measured longer than the limit it was declared under is
  already paid for, so it is logged rather than refused.
- **A transcript is returned even when it could not be kept.** If the upload fails, the row is kept
  with no audio. If the row fails, the audio is taken back out. Either way the person gets what they
  said: decision 15's rule at a smaller scale.
- **Audio before row in `keep`; audio before row in the sweep; rows before prefix in the erasure.**
  Each order leaves nothing the product cannot find again. A row whose audio would not delete stays
  for the next tick. An erasure that removed the rows and failed on the prefix is recorded as failed,
  and running it again retries the prefix.
- **The credit check.** The ticket is silent on it. A tenant refused a turn is refused the transcript
  that would start one, rather than charged for a question they cannot then ask.
- **Nothing said is logged, at any level.** The log carries bytes, seconds, type, language, model,
  whether audio was kept, and the provider's own error sentence. Never the transcript or the audio.
- **The sweep runs whether or not speech is enabled.** A deployment that switched voice off still owes
  the deletion of last week's recordings.

### 1c. The acceptance items, quoted back

- [x] *A deployment with `SPEECH_ENABLED=false` boots, logs once, and the route 404s.*
  `TestVoiceRouteIsAbsentWithoutAProvider`, for a person holding the grant and one who does not;
  `TestNewIsTheNopWhenNotUsable` for the one log line's three causes. Live: the route in the router's
  table only with a provider configured (§1f).
- [x] *A user without the `voice` capability gets 403 before any audio is read.*
  `TestVoiceRefusesAnUngrantedPersonBeforeReadingTheRecording` counts the body's bytes read (zero) and
  the provider's calls (zero), for a member and an admin. `TestCapabilityGatedRoutesRefuseAnAdminWithoutAGrant`,
  which had skipped since `T-Z1`, now runs. Live: §1f arms 1 and 3.
- [x] *A clip over the cap is refused at the route and never reaches the provider.*
  `TestVoiceOverEitherCapNeverReachesTheService`, both caps. Live: `too_long` and `too_big`, provider
  +0. **Met differently from written** — the cap the route can enforce is bytes, not duration (§1d).
- [x] *A transcript is returned and **no message is created** — asserted by counting the thread's
  messages before and after.* Structurally: `VoiceService` and `VoiceHandler` hold no message
  repository and no enqueuer. **Counted live:** 1 message before, 1 after (§1f arm 2).
- [x] *A provider failure returns an error the UI can show and leaves no orphan clip.*
  `TestVoiceProviderFailureKeepsNothingAndBillsNothing`, `TestVoiceProviderFailureSaysWhatToDoAndNotWhatTheProviderSaid`,
  and the second half nobody listed, `TestVoiceRowFailureTakesTheAudioBackOut`. Live: `502`, clips
  2 → 2, usage 2 → 2.
- [x] *No audio and no transcript appears in any log line at `Info`.*
  `TestVoiceNothingSaidReachesTheLog`, at every level, across five paths. Live: zero lines of the API
  log carry the transcript.
- [~] *A clip past `expires_at` is deleted by the sweep, and its message survives.*
  `TestVoiceSweepRemovesTheAudioBeforeTheRow`, and `Due` on a real Postgres (§1f). **The worker's tick
  has not run** (§1g). "Its message survives" holds trivially: nothing links a clip to a message
  (§1d).
- [~] *`T-H6` erasure removes a user's clips and their objects.* **Built for a company**, because
  `T-H6` erases companies (§1d). `TestEraseCompanyDataTakesTheVoiceClipsWithIt`,
  `TestEraseCompanyDataFailsWhenTheRecordingsWouldNotDelete`, `TestVoiceEraseCompanyRemovesTheRowsAndThePrefix`.
  Live: the rows, 2 → 0. **The objects have not been removed live**: the scratch stack has no object
  storage.
- [x] *Language hint defaults to the tenant's, not to English.*
  `TestVoiceLanguageIsTheTenantsAndNeverEnglishByDefault`. Live: an IDR tenant's clip was sent `id`,
  and `en-US` from the person was sent `en`.

### 1d. Where the ticket was wrong, or silent

- **`Migration: 082`.** `T-W2` took `082`; `083`→`086` went since. It is `087`, as roadmap 11's
  status block said to expect.
- **"Duration … capped at the route, before a byte reaches a provider."** A route cannot know how long
  a recording is without decoding it, and the WebM a browser's `MediaRecorder` writes carries no
  duration element to read. Built as three things:
  - a declared length, required and checked, and trusted no further;
  - a byte cap derived from it, which is what actually bounds the bill;
  - billing on the length the provider measured.
- **"`T-H6` erasure removes a user's clips and their objects."** `T-H6` has no per-person erasure. It
  erases a company, and removing a member deactivates them (`TeamService.Remove`) rather than
  deleting the row. So the company's erasure takes every clip and the whole `voice/<company_id>/`
  prefix, and a removed member's clips age out at `SPEECH_RETENTION_DAYS`. A per-person erasure would
  be `T-H6`'s to add, not this ticket's.
- **"The message it became if it became one."** Nothing in `T-W7` can write that column. A transcript
  becomes a message when the person sends it through `POST /api/chat`, which would have to carry the
  clip's id, and that is `T-W9`'s composer. Left out of `087` rather than shipped unwritten, the shape
  of `T-K1`'s binding nobody read ([`skills.md`](skills.md) §5h). Adding it is one nullable `ALTER`.
- **Silent on a deleted conversation.** A clip that cascaded with its conversation would strand its
  audio where nothing could find it. `thread_id` is `SET NULL`, and the sweep deletes a clip whose
  conversation is gone on its next tick.
- **"Defaults to the tenant's."** A company has no language field. The hint is the tenant's branding
  locale, else `id` for a rupiah tenant, else **none** — the provider detects. Not English: a tenant
  that stated nothing has not said it speaks English.
- **"One provider implementation."** Built as the request two providers share, so the choice research
  08 §6 has not measured stays a setting.
- **The config list** had no clock for the sweep, and no base URL or timeout. `SPEECH_SWEEP_CRON`,
  `SPEECH_BASE_URL` and `SPEECH_TIMEOUT_SECS` were added.
- **"The member's disabled control is this ticket's"** — roadmap 11's note from `T-Z7`. It is `T-W9`'s:
  the control is the microphone, and building a button with no recorder behind it would be building
  `T-W9` early, `T-Z1` §5's argument about `T-Z7`. What this ticket owed the screen was the `today`
  sentence, rewritten.
- **Silent on credits and on a deployment with no object storage.** A tenant out of credit gets `402`
  before the provider. Without storage, a clip is its transcript row with no audio.

### 1e. Proven failing

Eleven mutations, one at a time by a script, each restored from a copy afterwards. Each failed its
named test, and none only broke the build:

| Mutation | Killed by |
| --- | --- |
| The route taken out of `capabilityPolicy` | `TestVoiceRefusesAnUngrantedPersonBeforeReadingTheRecording` |
| The conversation read check deleted | `TestVoiceHiddenConversationIsNotFoundBeforeTheRecordingIsRead` |
| Billed on the declared length | `TestVoiceTranscribesKeepsAndBillsOnTheMeasuredLength` |
| Erasure skipping the prefix | `TestVoiceEraseCompanyRemovesTheRowsAndThePrefix` |
| `Accept` without the byte check | `TestAcceptNeedsTheBytesToBeTheDeclaredAudio` |
| `RetentionService` skipping the clips | `TestEraseCompanyDataTakesTheVoiceClipsWithIt` |
| No removal of the audio when the row fails | `TestVoiceRowFailureTakesTheAudioBackOut` |
| The transcript added to the log line | `TestVoiceNothingSaidReachesTheLog` |
| The route registered without a provider | `TestVoiceRouteIsAbsentWithoutAProvider` |
| `DeleteForCompany` with no company predicate | `TestEveryVoiceClipWriteIsTenantScoped` |
| The credit check disabled | `TestVoiceRefusesATenantWithoutCreditsBeforeTheProvider` |

### 1f. Run live on a scratch stack (2026-09-14)

**Not production** — which runs `1.6.0` and has none of this. An embedded Postgres 16 and miniredis on
loopback in `/tmp/tw7-gate`, the working tree's `cmd/api` under `env -i`, and a stand-in provider on
`127.0.0.1:18199` answering Groq's and OpenAI's request shape. The stand-in records the request's
shape and never its audio. No object storage, no worker, no model, no provider key. Everything was
stopped afterwards. The full record, predictions included, is live-gate §7j.

- **`087` up, down, up, and all four statements on real rows in two companies: as predicted.** The
  insert refuses another company's conversation and a malformed id, a deleted conversation's clip
  becomes due with `thread_id` NULL, and the sweep's list carries no transcript.
- **The capability arm `T-Z1` left for this ticket (§7c): as predicted.** A member and an admin
  without the grant got `403 capability: voice`, with the provider not called. Granted, the member got
  `200`. After the revoke, the member's **very next request** got `403`, and after re-granting, `200`.
- **What the provider was sent:** `model`, `response_format: verbose_json`, `temperature: 0`,
  `language: id` for the IDR tenant, and `clip.webm` as `audio/webm` with 6,004 bytes. `en-US` from
  the person arrived as `en`.
- **What was written:** one clip row (the member, the conversation, 2.75 s, `id`, 7 days, no object
  key), one `speech_transcription` usage row at **31 µUSD**, and **no message**, 1 before and 1 after.
- **Refused at the route, provider +0 each:** over the declared length (`413`), a PDF called WebM
  (`415`), `indonesian` as a language (`400`), 3.2 MB (`413`), a conversation on restricted HR
  (`404`), and a conversation that does not exist (`404`).
- **Provider down:** `502` with the sentence, and clips and usage unchanged.
- **The API log:** zero lines carry the transcript. The Info line carries bytes, seconds,
  `measured: true`, `kept_audio: false`, the language and the model. The failure's Warn line carries
  `speech provider answered 503: over capacity`.
- **Erasure:** the company's clips 2 → 0, and the erasure record `completed`.

### 1g. What is owed, and what stays open

In [`live-gate-backlog.md`](live-gate-backlog.md) §7j, with predictions:

1. **A real provider on Indonesian speech**, which is also research 08 §6's unknowns 1 and 2, and
   what decides `SPEECH_PROVIDER`.
2. **`verbose_json` on a real endpoint:** whether `measured` is true.
3. **The audio half**, with object storage: the upload, the sweep's removal and the erasure's prefix.
4. **The worker's tick**, on `SPEECH_SWEEP_CRON`.
5. **`087` at deploy.**

**Open, and not this ticket's to close:**
- **Nothing links a clip to the message it became** (§1d). It needs `POST /api/chat` to carry the clip
  id, which is `T-W9`'s.
- **The dashboard cannot tell whether voice is on.** The route's absence is the only signal. `T-W9`
  needs a way to draw the microphone disabled on a deployment with no provider (decision 15), and
  probing a route with a recording is not one. A field on an existing read is the likely shape.
- **A person's erasure.** A removed member's clips outlive them by up to `SPEECH_RETENTION_DAYS`.
  `T-H6` has no per-person erasure to join.
- **The byte rate is a guess about browsers.** 384 kbit/s is above every default this document knows
  of. A browser that records voice above it would see `413` at a length under the limit. The first
  `413` in production with a `duration_ms` under the limit is the signal.
