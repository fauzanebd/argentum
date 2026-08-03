package mcpserver

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"time"

	"github.com/Ingenimax/agent-sdk-go/pkg/interfaces"
	sdk "github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/sirupsen/logrus"

	"github.com/fauzanebd/argentum/internal/domain"
	"github.com/fauzanebd/argentum/internal/tenantctx"
)

// Authenticator resolves a presented key. *app.APIKeyService satisfies it, and
// it is the same method `/v1` authenticates with — the hashing, the expiry, the
// revocation and the last-used stamp are all already decided there.
type Authenticator interface {
	Authenticate(ctx context.Context, token string) (*domain.APIKey, error)
}

// Handler is the whole HTTP surface: an authenticating wrapper around the
// SDK's streamable handler.
//
// Auth is HTTP middleware rather than an MCP-level check because a caller with
// no key must be refused before a session exists. The MCP handshake would
// otherwise succeed, the client would list tools, and every call would come
// back as a tool error — which reads to the operator as "the server is broken"
// rather than "your key is missing".
func Handler(tools []interfaces.Tool, auth Authenticator) http.Handler {
	// One server value, one tool list: the surface does not vary per tenant.
	// What varies is the *scopes* on the context, which is what each handler
	// checks — so two keys of the same company with different scopes see the
	// same tool list and get different answers, exactly as `/v1` does.
	srv := New(tools)
	mcpHandler := sdk.NewStreamableHTTPHandler(func(*http.Request) *sdk.Server { return srv }, nil)

	mux := http.NewServeMux()
	mux.HandleFunc("/health", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"status": "ok", "timestamp": time.Now().Unix()})
	})
	mux.Handle("/", authenticated(mcpHandler, auth))
	return mux
}

// authenticated resolves the bearer token, puts the tenant on the context, and
// refuses everything else.
//
// The context it builds is the one `internal/tools`' audit decorator reads, so
// every call through this server writes an `agent_actions` row attributed to
// the key — `actor_kind = api_key`, `actor_ref` the key id — with no work here
// beyond setting three values. That is the ticket's "same audit attribution"
// requirement, met by not having a second path.
func authenticated(next http.Handler, auth Authenticator) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		token := bearer(r)
		if token == "" {
			unauthorized(w, "missing API key: send Authorization: Bearer <key>")
			return
		}
		key, err := auth.Authenticate(r.Context(), token)
		if err != nil {
			// Deliberately one message for every failure — unknown, revoked,
			// expired. A caller learning which of the three they hit is a caller
			// probing key space with feedback.
			unauthorized(w, "invalid API key")
			return
		}

		ctx := tenantctx.WithCompanyID(r.Context(), key.CompanyID)
		ctx = tenantctx.WithActor(ctx, string(domain.ActorKindAPIKey), key.ID)
		// The `api` channel, not a new one: a channel is where a conversation
		// happens, and this is the same machine-to-machine door `/v1` opens. An
		// operator asking "what did this key do?" filters by key id, which is on
		// every row either way.
		ctx = tenantctx.WithChannel(ctx, string(domain.ChannelAPI))
		ctx = WithScopes(ctx, key.Scopes)

		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

func bearer(r *http.Request) string {
	h := r.Header.Get("Authorization")
	if h == "" {
		return ""
	}
	return strings.TrimSpace(strings.TrimPrefix(h, "Bearer "))
}

func unauthorized(w http.ResponseWriter, msg string) {
	w.Header().Set("WWW-Authenticate", `Bearer realm="argentum-mcp"`)
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusUnauthorized)
	if err := json.NewEncoder(w).Encode(map[string]any{"error": msg}); err != nil {
		logrus.WithError(err).Warn("mcp: could not write the unauthorized body")
	}
}
