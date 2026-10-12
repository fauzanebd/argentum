package middleware

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/alicebob/miniredis/v2"
	"github.com/gin-gonic/gin"
	"github.com/redis/go-redis/v9"

	"github.com/fauzanebd/argentum/internal/config"
)

// RIZ-79 review F1: who a request came from, as every address-keyed limit
// sees it. The shipped proxies sit on private addresses — the compose nginx on
// the Docker bridge, Traefik on the pod network — so the "via proxy" peers
// below are private and the clients public.

const (
	proxyPeer = "172.18.0.5:51000" // the compose nginx, say
	client    = "203.0.113.7"
	spoofed   = "6.6.6.6"
)

func defaultProxies() []string { return strings.Split(config.DefaultTrustedProxies, ",") }

func clientIPOf(t *testing.T, proxies []string, peer string, headers map[string]string) string {
	t.Helper()
	r := gin.New()
	TrustProxies(r, proxies)
	var got string
	r.GET("/", func(c *gin.Context) { got = c.ClientIP() })
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.RemoteAddr = peer
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	r.ServeHTTP(httptest.NewRecorder(), req)
	return got
}

func TestClientIPCannotBeWrittenByTheClient(t *testing.T) {
	cases := []struct {
		name    string
		proxies []string
		peer    string
		headers map[string]string
		want    string
	}{
		{
			// nginx's $proxy_add_x_forwarded_for appends to whatever the
			// client sent. Before this the leftmost entry won.
			name: "a spoofed hop ahead of the one the proxy appended", proxies: defaultProxies(),
			peer: proxyPeer, headers: map[string]string{"X-Forwarded-For": spoofed + ", " + client},
			want: client,
		},
		{
			name: "through two private proxies", proxies: defaultProxies(),
			peer: proxyPeer, headers: map[string]string{"X-Forwarded-For": spoofed + ", " + client + ", 10.42.0.9"},
			want: client,
		},
		{
			// Nobody proxies for a public peer, so its header is its own words.
			name: "a public peer naming someone else", proxies: defaultProxies(),
			peer: client + ":40000", headers: map[string]string{"X-Forwarded-For": spoofed},
			want: client,
		},
		{
			// Neither shipped proxy sets X-Real-IP, so it arrives as written.
			name: "X-Real-IP is not read", proxies: defaultProxies(),
			peer: proxyPeer, headers: map[string]string{"X-Real-IP": spoofed},
			want: "172.18.0.5",
		},
		{
			name: "TRUSTED_PROXIES=none", proxies: nil,
			peer: proxyPeer, headers: map[string]string{"X-Forwarded-For": spoofed + ", " + client},
			want: "172.18.0.5",
		},
		{
			// Fails closed: a list gin refuses trusts nobody, rather than
			// leaving gin's believe-everyone default in place.
			name: "a list gin refuses", proxies: []string{"not-an-address"},
			peer: proxyPeer, headers: map[string]string{"X-Forwarded-For": spoofed + ", " + client},
			want: "172.18.0.5",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := clientIPOf(t, tc.proxies, tc.peer, tc.headers); got != tc.want {
				t.Errorf("ClientIP() = %q, want %q", got, tc.want)
			}
		})
	}
}

// The three address-keyed limiters, each driven the way the review's scenario
// drives signup: one host behind the proxy, a fresh spoofed hop per request.
// One token each, so the second request must be refused.
func TestAddressLimitsIgnoreASpoofedHop(t *testing.T) {
	cases := []struct {
		name   string
		method string
		mount  func(r *gin.Engine, l *RateLimiter, ok gin.HandlerFunc)
		path   string
	}{
		{"signup", http.MethodPost, func(r *gin.Engine, l *RateLimiter, ok gin.HandlerFunc) {
			r.POST("/auth/signup", l.SignupMiddleware(), ok)
		}, "/auth/signup"},
		{"share", http.MethodGet, func(r *gin.Engine, l *RateLimiter, ok gin.HandlerFunc) {
			r.GET("/share/x", l.ShareMiddleware(), ok)
		}, "/share/x"},
		{"embed mint", http.MethodPost, func(r *gin.Engine, l *RateLimiter, ok gin.HandlerFunc) {
			r.POST("/api/embed/session", l.EmbedMintMiddleware(), ok)
		}, "/api/embed/session"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			srv := miniredis.RunT(t)
			rdb := redis.NewClient(&redis.Options{Addr: srv.Addr()})
			t.Cleanup(func() { _ = rdb.Close() })

			r := gin.New()
			TrustProxies(r, defaultProxies())
			tc.mount(r, NewRateLimiter(rdb, 1, 1.0/3600), func(c *gin.Context) { c.Status(http.StatusOK) })
			hit := func(xff string) int {
				w := httptest.NewRecorder()
				req := httptest.NewRequest(tc.method, tc.path, nil)
				req.RemoteAddr = proxyPeer
				req.Header.Set("X-Forwarded-For", xff)
				r.ServeHTTP(w, req)
				return w.Code
			}

			if code := hit("6.6.6.1, " + client); code != http.StatusOK {
				t.Fatalf("first = %d, want 200", code)
			}
			if code := hit("6.6.6.2, " + client); code != http.StatusTooManyRequests {
				t.Errorf("second, with a new spoofed hop = %d, want 429 — the header chose the bucket", code)
			}
			if code := hit("198.51.100.20"); code != http.StatusOK {
				t.Errorf("another client behind the same proxy = %d, want 200", code)
			}
		})
	}
}
