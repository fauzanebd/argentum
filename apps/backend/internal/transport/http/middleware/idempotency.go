package middleware

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/sirupsen/logrus"

	"github.com/fauzanebd/argentum/internal/idempotency"
	"github.com/fauzanebd/argentum/internal/transport/http/apierr"
)

// idempotencyHeader is the header a caller sends. The name is the one every
// other API in this space uses; an integrator should not have to look it up.
const idempotencyHeader = "Idempotency-Key"

// replayHeader marks a response that is a replay rather than a fresh
// execution. A client that logs it can tell "my retry worked" from "my retry
// ran the job again", which is the one thing idempotency is supposed to make
// knowable.
const replayHeader = "Idempotent-Replay"

// Gin context keys the middleware and its handlers share.
const (
	ctxIdemResult = "idem_result"
	ctxIdemKey    = "idem_key"
)

// maxIdempotencyKeyLen bounds the caller's key. It becomes part of a Redis
// key, so an unbounded one is an unbounded allocation chosen by the caller.
const maxIdempotencyKeyLen = 255

// IdempotencyOption configures the middleware per route.
type IdempotencyOption func(*idempotencyConfig)

// Replayer answers a replayed request from the stored record. Return true
// when it has written the response; false falls through to the default,
// which writes the stored logical result verbatim.
//
// It exists for the two routes whose response cannot be reconstructed by
// echoing JSON: a report download URL has to be re-presigned before it is
// returned (T-A2), and an SSE chat replay has to re-attach to the turn's
// pubsub channel rather than answer at all (T-A3).
type Replayer func(c *gin.Context, rec *idempotency.Record) bool

type idempotencyConfig struct {
	required bool
	replay   Replayer
}

// IdempotencyRequired makes a missing `Idempotency-Key` a 400. Use it on
// every route that spends money or creates a document; a caller that has not
// thought about retries on those has a duplicate-billing bug waiting for its
// first network blip.
func IdempotencyRequired() IdempotencyOption {
	return func(cfg *idempotencyConfig) { cfg.required = true }
}

// IdempotencyReplayWith installs a custom replay path. See Replayer.
func IdempotencyReplayWith(fn Replayer) IdempotencyOption {
	return func(cfg *idempotencyConfig) { cfg.replay = fn }
}

// Idempotency deduplicates a write route by `Idempotency-Key` (T-A1).
//
// Install it per route rather than on the group. It is only meaningful on
// requests that change something: a GET is already idempotent, and recording
// one would put a Redis key and a 24-hour TTL behind every read an integrator
// makes. A caller that sends the header on a GET gets it ignored, which is
// what "accepted everywhere else" means in practice.
//
// **A failed request forgets its key.** If the handler answers 4xx or 5xx the
// record is discarded, because the next thing a well-behaved client does with
// a 500 is retry it — and a retry that is refused for 24 hours by the
// bookkeeping of the attempt that failed is worse than no idempotency at all.
//
// **Redis being unavailable does not fail the request.** The house rule for
// optional subsystems applies (see credits.go): the alternative is that a
// Redis hiccup takes the whole write surface down. The cost is that a retry
// during the outage can duplicate, and that is logged at Warn where an
// incident will find it.
func Idempotency(store idempotency.Store, opts ...IdempotencyOption) gin.HandlerFunc {
	cfg := &idempotencyConfig{}
	for _, opt := range opts {
		opt(cfg)
	}

	return func(c *gin.Context) {
		key := c.GetHeader(idempotencyHeader)
		if key == "" {
			if cfg.required {
				apierr.AbortParam(c, apierr.TypeInvalidRequest, "idempotency_key_required",
					"Send an `Idempotency-Key` header — a unique string per logical request — so a retry cannot run this twice.",
					idempotencyHeader)
				return
			}
			c.Next()
			return
		}
		if len(key) > maxIdempotencyKeyLen {
			apierr.AbortParam(c, apierr.TypeInvalidRequest, "idempotency_key_too_long",
				"An `Idempotency-Key` may be at most 255 characters.", idempotencyHeader)
			return
		}

		company := c.GetString("company_id")
		if company == "" || store == nil {
			// No tenant means APIKeyAuth did not run, which its own middleware
			// has already refused; no store means this deployment has no
			// Redis. Neither is this middleware's failure to report.
			c.Next()
			return
		}

		body, err := readAndRestoreBody(c)
		if err != nil {
			apierr.Abort(c, apierr.TypeInvalidRequest, "unreadable_body",
				"Could not read the request body.")
			return
		}
		hash := bodyHash(body)
		redisKey := idempotency.Key(company, key)

		existing, first, err := store.Begin(c.Request.Context(), redisKey, hash)
		if err != nil {
			logrus.WithError(err).WithFields(logrus.Fields{
				"company_id": company,
				"path":       c.Request.URL.Path,
			}).Warn("idempotency store unavailable; proceeding without replay protection")
			c.Next()
			return
		}

		if !first {
			replayOrRefuse(c, cfg, existing, hash)
			return
		}

		c.Set(ctxIdemKey, redisKey)
		c.Next()

		status := c.Writer.Status()
		if status >= http.StatusBadRequest {
			if err := store.Discard(c.Request.Context(), redisKey); err != nil {
				logrus.WithError(err).Warn("idempotency record not discarded after a failed request")
			}
			return
		}
		if err := store.Complete(c.Request.Context(), redisKey, status, declaredResult(c)); err != nil {
			logrus.WithError(err).WithField("company_id", company).
				Warn("idempotency record not completed; a retry of this request may run it again")
		}
	}
}

// inFlightBody is the one `/v1` response that carries more than the error
// envelope: a retry arriving mid-flight gets the ids of the work it is
// waiting on, so the caller can poll instead of retrying again. The error
// half is apierr.Detail rather than a map, so the envelope has exactly one
// definition.
type inFlightBody struct {
	Error    apierr.Detail   `json:"error"`
	InFlight json.RawMessage `json:"in_flight,omitempty"`
}

// replayOrRefuse handles the three ways a second request under one key ends.
func replayOrRefuse(c *gin.Context, cfg *idempotencyConfig, rec *idempotency.Record, bodyHash string) {
	if rec.BodyHash != bodyHash {
		// The case that catches a broken retry loop before it bills twice: a
		// client reusing one key across genuinely different requests would
		// otherwise silently receive the first one's answer forever.
		//
		// 409 rather than the 400 this class usually carries, because the
		// request is not malformed — it conflicts with one that already
		// happened, which is what a 409 means and what tells a client the
		// fix is a new key rather than a corrected body.
		apierr.AbortStatus(c, http.StatusConflict, apierr.TypeInvalidRequest, "idempotency_key_reuse",
			"That `Idempotency-Key` was already used with a different request body. Use a new key for a new request.",
			idempotencyHeader)
		return
	}

	if rec.Status == idempotency.StatusInFlight {
		// The common case, and the reason this is not simply a replay: a
		// client timeout plus a retry arrives while the original turn is
		// still running. Answering 409 with the id it is already waiting on
		// is what lets the client poll instead of starting a second one.
		c.Header(replayHeader, "true")
		c.AbortWithStatusJSON(http.StatusConflict, inFlightBody{
			Error: apierr.NewDetail(c, apierr.TypeInvalidRequest, "request_in_flight",
				"The original request under this `Idempotency-Key` is still running. Poll it rather than retrying."),
			InFlight: rec.Result,
		})
		return
	}

	c.Header(replayHeader, "true")
	if cfg.replay != nil && cfg.replay(c, rec) {
		c.Abort()
		return
	}
	if len(rec.Result) == 0 {
		apierr.Abort(c, apierr.TypeServer, "replay_unavailable",
			"The original request under this `Idempotency-Key` completed, but its result can no longer be replayed.")
		return
	}
	status := rec.HTTPStatus
	if status == 0 {
		status = http.StatusOK
	}
	c.Data(status, "application/json; charset=utf-8", rec.Result)
	c.Abort()
}

// DeclareIdempotentResult records the logical result a replay should
// reproduce — ids and status, never a payload. A handler calls it once, on
// the success path, before returning.
func DeclareIdempotentResult(c *gin.Context, v any) {
	raw, err := json.Marshal(v)
	if err != nil {
		logrus.WithError(err).Warn("idempotent result not recorded: unmarshalable value")
		return
	}
	c.Set(ctxIdemResult, json.RawMessage(raw))
}

// DeclareIdempotentProgress attaches what is known while the request is still
// running, so a retry arriving mid-flight gets a 409 that names the thread or
// report it is waiting on instead of a bare conflict. It writes through to
// the store immediately, because the point is to be visible to a request that
// arrives before this one finishes.
func DeclareIdempotentProgress(c *gin.Context, store idempotency.Store, v any) {
	key := c.GetString(ctxIdemKey)
	if key == "" || store == nil {
		return
	}
	raw, err := json.Marshal(v)
	if err != nil {
		return
	}
	if err := store.Progress(c.Request.Context(), key, raw); err != nil {
		logrus.WithError(err).Warn("idempotency progress not recorded")
	}
}

// declaredResult reads back what the handler declared, or nil.
func declaredResult(c *gin.Context) json.RawMessage {
	v, ok := c.Get(ctxIdemResult)
	if !ok {
		return nil
	}
	raw, _ := v.(json.RawMessage)
	return raw
}

// bodyHash is what "the same request" means here: the bytes, not a parsed
// shape. Two JSON documents differing only in key order are two different
// requests by this rule, which is the conservative direction — it can refuse
// a retry a smarter comparison would have replayed, and it cannot replay one
// that asked for something else.
func bodyHash(b []byte) string {
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}

// readAndRestoreBody drains the body for hashing and puts it back for the
// handler. The body is already bounded by MaxBodyBytes, which runs above
// this: buffering an unbounded one here would be a memory DoS with a
// well-formed header.
func readAndRestoreBody(c *gin.Context) ([]byte, error) {
	if c.Request.Body == nil {
		return nil, nil
	}
	body, err := io.ReadAll(c.Request.Body)
	if err != nil {
		return nil, err
	}
	c.Request.Body = io.NopCloser(bytes.NewReader(body))
	return body, nil
}
