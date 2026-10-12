package middleware

import (
	"fmt"
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

// WellFormedID reports whether s is an id Postgres's uuid input would accept,
// which is the question every lookup behind a uuid path parameter asks the
// database first — and answers, when the answer is no, with a 22P02 that until
// RIZ-26 reached the caller as a 500 quoting the driver.
//
// uuid.Parse is the check, less one form: it accepts the 45-character
// `urn:uuid:` prefix, which Postgres refuses with the same 22P02. Everything
// else it accepts Postgres accepts too — hyphenated, 32 bare hex digits,
// braces, any case — so a well-formed id reaches the handler exactly as it did
// before. The converse is not quite true and does not need to be: Postgres also
// takes a hyphen after any group of four digits, a spelling no client of this
// API has ever produced, and refusing it costs nobody anything.
//
// Exported for the handlers that read an id out of a request body rather than
// the path (the chat and room routes), so "well-formed" means one thing on
// both sides of the boundary.
func WellFormedID(s string) bool {
	if _, err := uuid.Parse(s); err != nil {
		return false
	}
	return !strings.HasPrefix(strings.ToLower(s), "urn:")
}

// RequireWellFormedIDs answers a path id that cannot be an id with the 404 a
// missing one gets, before anything below it reads the id (RIZ-26).
//
// The defect it closes was one class with three faces: a handler that passed
// `x` to its repository got a 22P02 and echoed it as a 500; a repository that
// wrapped it got a generic 500; and RequireResource, asking the grant store
// about `x`, got the same driver error and answered 503 "try again" — an
// outage, to anyone watching 5xx, caused by a typo in a URL. Fourteen
// repositories had not mapped the error. Mapping it in each is how fourteen got
// missed, so it is checked once, here, by parameter name:
//
//   - uuidParams are the names that always carry a row id. A value WellFormedID
//     refuses aborts with `404 {"error":"not found"}`.
//   - exemptParams are the names that never do — a resource kind, a capability,
//     a page number, a phone number. Passed in so a name cannot be in both
//     tables: that is a contradiction this refuses to start with.
//   - exemptRoutes are routes keyed like RolePolicy whose uuid-named parameter
//     is not a uuid on that route — the chat platforms' user ids, which are
//     Discord snowflakes, Lark open_ids and Slack member ids.
//
// A name in neither set passes. Failing closed on it would 404 a route somebody
// just added, in production, for a reason nobody would guess from the response;
// the classification test in cmd/api fails the build on it instead, which is
// the same arrangement RequireResource has with its own tables.
//
// **Where it sits** is after RequireCapability and before RequireResource: a
// caller with no session still gets 401 and a member on an admin route still
// gets 403, so the refusal they already got is unchanged, and the grant store
// is never asked about an id that names nothing. The 404 and not a 400 is the
// ticket's decision: a path that names nothing is a resource that does not
// exist, and the body is the one `/api/access/*` already answered a malformed
// id with.
func RequireWellFormedIDs(uuidParams []string, exemptParams, exemptRoutes map[string]string) gin.HandlerFunc {
	checked := make(map[string]bool, len(uuidParams))
	for _, name := range uuidParams {
		if _, exempt := exemptParams[name]; exempt {
			panic(fmt.Sprintf("RequireWellFormedIDs: :%s is both a uuid parameter and an exempt one", name))
		}
		checked[name] = true
	}
	return func(c *gin.Context) {
		if _, exempt := exemptRoutes[RouteKey(c.Request.Method, c.FullPath())]; exempt {
			c.Next()
			return
		}
		for _, p := range c.Params {
			if checked[p.Key] && !WellFormedID(p.Value) {
				AbortNotFound(c)
				return
			}
		}
		c.Next()
	}
}

// AbortNotFound is the answer a malformed id gets: the status and body a
// well-formed id that names nothing gets on the routes that already said so.
func AbortNotFound(c *gin.Context) {
	c.AbortWithStatusJSON(http.StatusNotFound, gin.H{"error": "not found"})
}
