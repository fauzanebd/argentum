package middleware

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
)

// wellFormedChain builds these routes behind RequireWellFormedIDs, followed by
// a link that records it ran — the stand-in for RequireResource, the handler and
// the query, none of which may see a malformed id:
//
//	GET    /things/:id                    :id a uuid
//	GET    /things/:id/runs/:runID        two uuids
//	GET    /access/:kind/:id              :kind exempt, :id a uuid
//	GET    /docs/:id/pages/:page          :page exempt
//	DELETE /chat/users/:id                an exempt route: :id is a platform user id
//	GET    /widgets/:widgetId             a name in neither table
func wellFormedChain(t *testing.T) (*gin.Engine, *[]string) {
	t.Helper()
	gin.SetMode(gin.TestMode)
	var reached []string
	r := gin.New()
	r.Use(RequireWellFormedIDs(
		[]string{"id", "runID"},
		map[string]string{"kind": "a kind's name", "page": "a page number"},
		map[string]string{"DELETE /chat/users/:id": "a chat platform's own user id"},
	))
	r.Use(func(c *gin.Context) {
		reached = append(reached, c.Request.Method+" "+c.Request.URL.Path)
		c.Next()
	})
	ok := func(c *gin.Context) { c.Status(http.StatusOK) }
	r.GET("/things/:id", ok)
	r.GET("/things/:id/runs/:runID", ok)
	r.GET("/access/:kind/:id", ok)
	r.GET("/docs/:id/pages/:page", ok)
	r.DELETE("/chat/users/:id", ok)
	r.GET("/widgets/:widgetId", ok)
	return r, &reached
}

const aUUID = "3f2b8c1e-9d4a-4e6b-8f0a-1c2d3e4f5a6b"

// TestRequireWellFormedIDs is Q1: what is refused, what passes, and that a
// refusal stops the chain before anything below it runs.
func TestRequireWellFormedIDs(t *testing.T) {
	cases := []struct {
		name   string
		method string
		path   string
		want   int
	}{
		// Refused: everything Postgres's uuid input would answer with 22P02.
		{"a word", http.MethodGet, "/things/x", http.StatusNotFound},
		{"a number", http.MethodGet, "/things/123", http.StatusNotFound},
		{"a quote", http.MethodGet, "/things/%27", http.StatusNotFound},
		{"something SQL-shaped", http.MethodGet, "/things/1%27%20OR%20%271%27=%271", http.StatusNotFound},
		{"a uuid one digit short", http.MethodGet, "/things/" + aUUID[:35], http.StatusNotFound},
		{"a uuid with a stray letter", http.MethodGet, "/things/" + aUUID[:35] + "g", http.StatusNotFound},
		// uuid.Parse takes this one; Postgres does not, so it is refused here.
		{"the urn form", http.MethodGet, "/things/urn:uuid:" + aUUID, http.StatusNotFound},
		{"a malformed second id", http.MethodGet, "/things/" + aUUID + "/runs/x", http.StatusNotFound},
		{"a malformed first id", http.MethodGet, "/things/x/runs/" + aUUID, http.StatusNotFound},
		{"a malformed id beside an exempt kind", http.MethodGet, "/access/agent/x", http.StatusNotFound},

		// Passed: every spelling Postgres accepts, so a well-formed id reaches
		// the handler exactly as before.
		{"canonical", http.MethodGet, "/things/" + aUUID, http.StatusOK},
		{"uppercase", http.MethodGet, "/things/" + strings.ToUpper(aUUID), http.StatusOK},
		{"braces", http.MethodGet, "/things/%7B" + aUUID + "%7D", http.StatusOK},
		{"32 bare hex digits", http.MethodGet, "/things/" + strings.ReplaceAll(aUUID, "-", ""), http.StatusOK},
		{"two well-formed ids", http.MethodGet, "/things/" + aUUID + "/runs/" + aUUID, http.StatusOK},

		// Passed: names that never carry a uuid, and the routes whose :id is not one.
		{"an exempt kind", http.MethodGet, "/access/agent/" + aUUID, http.StatusOK},
		{"an exempt page number", http.MethodGet, "/docs/" + aUUID + "/pages/1", http.StatusOK},
		{"an exempt route", http.MethodDelete, "/chat/users/U024BE7LH", http.StatusOK},
		// Unclassified passes at runtime; cmd/api's classification test is
		// what refuses it, at build time.
		{"a name in neither table", http.MethodGet, "/widgets/blue", http.StatusOK},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			r, reached := wellFormedChain(t)
			w := httptest.NewRecorder()
			r.ServeHTTP(w, httptest.NewRequest(tc.method, tc.path, nil))
			if w.Code != tc.want {
				t.Fatalf("%s %s: %d %s, want %d", tc.method, tc.path, w.Code, w.Body, tc.want)
			}
			if tc.want == http.StatusNotFound {
				if got := w.Body.String(); got != `{"error":"not found"}` {
					t.Errorf("body = %s, want the plain not-found", got)
				}
				if len(*reached) != 0 {
					t.Errorf("the chain ran past the refusal: %v", *reached)
				}
				return
			}
			if len(*reached) != 1 {
				t.Errorf("a well-formed request did not reach the next link: %v", *reached)
			}
		})
	}
}

// A name in both tables is a contradiction in the tables, refused when the
// chain is built rather than decided one way at request time.
func TestRequireWellFormedIDsRefusesAParamInBothTables(t *testing.T) {
	defer func() {
		if recover() == nil {
			t.Fatal("a parameter both checked and exempt was accepted")
		}
	}()
	RequireWellFormedIDs([]string{"id", "page"}, map[string]string{"page": "a page number"}, nil)
}

func TestWellFormedID(t *testing.T) {
	for in, want := range map[string]bool{
		aUUID:                                  true,
		strings.ToUpper(aUUID):                 true,
		"{" + aUUID + "}":                      true,
		strings.ReplaceAll(aUUID, "-", ""):     true,
		"00000000-0000-0000-0000-000000000000": true,
		"":                                     false,
		"x":                                    false,
		"urn:uuid:" + aUUID:                    false,
		"URN:UUID:" + aUUID:                    false,
		" " + aUUID:                            false,
	} {
		if got := WellFormedID(in); got != want {
			t.Errorf("WellFormedID(%q) = %v, want %v", in, got, want)
		}
	}
}
