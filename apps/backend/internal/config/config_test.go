package config

import (
	"net/url"
	"reflect"
	"strings"
	"testing"

	"github.com/fauzanebd/argentum/internal/queue"
)

// --- Effective*() fallback chains -------------------------------------------
//
// Seven accessors exist because the same setting is reachable through two env
// vars, and every one of them is read on a hot path (client construction,
// pricing lookup, queue dial). A fallback that returns "" instead of the value
// behind it does not fail loudly — it constructs a client against the wrong
// provider, or prices a turn at the default rate.

func TestEffectiveLLMInterface(t *testing.T) {
	cases := []struct {
		name     string
		iface    string
		provider string
		want     string
	}{
		{"interface wins", "anthropic", "custom", "anthropic"},
		{"falls back to provider", "", "openai", "openai"},
		{"both empty", "", "", ""},
		{"trims and lowercases the interface", "  Anthropic  ", "custom", "anthropic"},
		{"trims and lowercases the fallback", "", "  OpenAI\t", "openai"},
		// Whitespace-only is the shape a half-filled .env produces
		// (`LLM_INTERFACE= `), and it must fall through rather than becoming
		// an empty interface name that fails validation with a confusing message.
		{"whitespace-only interface falls through", "   ", "gemini", "gemini"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			c := &Config{LLMInterface: tc.iface, LLMProvider: tc.provider}
			if got := c.EffectiveLLMInterface(); got != tc.want {
				t.Errorf("EffectiveLLMInterface() = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestEffectiveLightLLMInterface(t *testing.T) {
	cases := []struct {
		name     string
		iface    string
		provider string
		want     string
	}{
		{"interface wins", "gemini", "custom", "gemini"},
		{"falls back to provider", "", "anthropic", "anthropic"},
		{"both empty", "", "", ""},
		{"trims and lowercases", " GEMINI ", "", "gemini"},
		{"whitespace-only falls through", "  ", "OpenAI", "openai"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			c := &Config{LightLLMInterface: tc.iface, LightLLMProvider: tc.provider}
			if got := c.EffectiveLightLLMInterface(); got != tc.want {
				t.Errorf("EffectiveLightLLMInterface() = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestEffectiveLightLLMModel(t *testing.T) {
	// The metering layer looks pricing up by this string. Empty means the
	// guardrail model's tokens get priced at DefaultPricing, which is the
	// class of bug T-02c existed to fix — so the fallback matters.
	cases := []struct {
		name  string
		light string
		main  string
		want  string
	}{
		{"light model wins", "gpt-5-mini", "claude-haiku-4.5", "gpt-5-mini"},
		{"falls back to the main model", "", "claude-haiku-4.5", "claude-haiku-4.5"},
		{"trims", "  gpt-5-nano  ", "", "gpt-5-nano"},
		{"whitespace-only falls through", "   ", "claude-haiku-4.5", "claude-haiku-4.5"},
		{"trims the fallback too", "", "  claude-haiku-4.5 ", "claude-haiku-4.5"},
		{"both empty", "", "", ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			c := &Config{LightLLMModel: tc.light, LLMModel: tc.main}
			if got := c.EffectiveLightLLMModel(); got != tc.want {
				t.Errorf("EffectiveLightLLMModel() = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestEffectiveClassifierModel(t *testing.T) {
	cases := []struct {
		name       string
		classifier string
		main       string
		want       string
	}{
		{"classifier model wins", "gpt-5-nano", "claude-haiku-4.5", "gpt-5-nano"},
		{"falls back to the main model", "", "claude-haiku-4.5", "claude-haiku-4.5"},
		{"trims", " gpt-5-nano ", "", "gpt-5-nano"},
		{"whitespace-only falls through", "  ", "claude-haiku-4.5", "claude-haiku-4.5"},
		{"both empty", "", "", ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			c := &Config{ClassifierModel: tc.classifier, LLMModel: tc.main}
			if got := c.EffectiveClassifierModel(); got != tc.want {
				t.Errorf("EffectiveClassifierModel() = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestEffectiveEmbeddingAPIKey(t *testing.T) {
	// The borrow-the-LLM-key branch is only sound when the primary LLM speaks
	// OpenAI's wire protocol. Borrowing an Anthropic key for an OpenAI
	// embeddings call sends a live credential to the wrong vendor, so the
	// non-OpenAI case must return "" — the caller reads that as "disabled".
	cases := []struct {
		name         string
		embeddingKey string
		llmKey       string
		iface        string
		provider     string
		want         string
	}{
		{"explicit key wins", "sk-embed", "sk-llm", "openai", "", "sk-embed"},
		{"explicit key wins even on anthropic", "sk-embed", "sk-llm", "anthropic", "", "sk-embed"},
		{"borrows the llm key on openai", "", "sk-llm", "openai", "", "sk-llm"},
		{"borrows via the provider fallback", "", "sk-llm", "", "openai", "sk-llm"},
		{"refuses to borrow an anthropic key", "", "sk-llm", "anthropic", "", ""},
		{"refuses to borrow a gemini key", "", "sk-llm", "gemini", "", ""},
		{"no key anywhere", "", "", "openai", "", ""},
		{"trims the explicit key", "  sk-embed  ", "", "openai", "", "sk-embed"},
		{"trims the borrowed key", "", "  sk-llm  ", "openai", "", "sk-llm"},
		{"whitespace-only explicit key falls through", "   ", "sk-llm", "openai", "", "sk-llm"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			c := &Config{
				EmbeddingAPIKey: tc.embeddingKey,
				LLMAPIKey:       tc.llmKey,
				LLMInterface:    tc.iface,
				LLMProvider:     tc.provider,
			}
			if got := c.EffectiveEmbeddingAPIKey(); got != tc.want {
				t.Errorf("EffectiveEmbeddingAPIKey() = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestResolvedAsynqRedisURL(t *testing.T) {
	cases := []struct {
		name  string
		asynq string
		redis string
		want  string
	}{
		{"dedicated asynq url wins", "redis://asynq:6379", "redis://cache:6379", "redis://asynq:6379"},
		{"falls back to the shared redis", "", "redis://cache:6379", "redis://cache:6379"},
		{"both empty", "", "", ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			c := &Config{AsynqRedisURL: tc.asynq, RedisURL: tc.redis}
			if got := c.ResolvedAsynqRedisURL(); got != tc.want {
				t.Errorf("ResolvedAsynqRedisURL() = %q, want %q", got, tc.want)
			}
		})
	}
}

// --- redisDialAddr -----------------------------------------------------------

func TestRedisDialAddr(t *testing.T) {
	// go-redis wants "host:port" and will not parse a URI. Anything this
	// returns unchanged had better already be a dial address.
	cases := []struct {
		name string
		in   string
		want string
	}{
		{"bare host:port passes through", "localhost:6380", "localhost:6380"},
		{"bare host with no port passes through", "redis", "redis"},
		{"empty", "", ""},
		{"redis uri", "redis://localhost:6380", "localhost:6380"},
		{"redis uri without a port defaults to 6379", "redis://cache.internal", "cache.internal:6379"},
		{"rediss uri", "rediss://cache.internal:6380", "cache.internal:6380"},
		{"credentials are stripped", "redis://user:pass@cache.internal:6380", "cache.internal:6380"},
		{"database suffix is stripped", "redis://cache.internal:6380/3", "cache.internal:6380"},
		{"query params are stripped", "redis://cache.internal:6380?dial_timeout=5s", "cache.internal:6380"},
		// A bracketed IPv6 host has to stay bracketed or the dial address is
		// unparseable — net.JoinHostPort is what puts them back.
		{"ipv6 uri", "redis://[::1]:6380", "[::1]:6380"},
		{"ipv6 uri without a port", "redis://[fe80::1]", "[fe80::1]:6379"},
		// Unparseable-but-scheme-shaped input is returned as-is rather than
		// mangled: the dial then fails with the operator's own string in the
		// error, which is the debuggable outcome.
		{"scheme with no host", "redis://", "redis://"},
		{"malformed uri", "redis://%zz", "redis://%zz"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := redisDialAddr(tc.in); got != tc.want {
				t.Errorf("redisDialAddr(%q) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}
}

func TestRedisDialAddrUsesRedisURLNotAsynq(t *testing.T) {
	c := &Config{RedisURL: "redis://cache:6380", AsynqRedisURL: "redis://asynq:6379"}
	if got := c.RedisDialAddr(); got != "cache:6380" {
		t.Errorf("RedisDialAddr() = %q, want cache:6380", got)
	}
}

// --- DatabaseURL -------------------------------------------------------------

func TestDatabaseURLIsParseableWithSpecialCharacters(t *testing.T) {
	// golang-migrate parses this with net/url. A password containing @ or /
	// unescaped moves the host boundary and the migration runs against the
	// wrong server — or, more often, fails at 3am with "dial tcp: lookup p".
	cases := []struct {
		name     string
		password string
	}{
		{"plain", "s3cr3t"},
		{"at sign", "p@ssw0rd"},
		{"colon", "pass:word"},
		{"slash", "pass/word"},
		{"question mark and hash", "pa?ss#word"},
		{"percent", "100%pass"},
		{"space", "pass word"},
		{"everything", "p@:s/s?w#o%r d&=+"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			c := &Config{
				DBHost:     "db.internal",
				DBPort:     5432,
				DBUser:     "analytics",
				DBPassword: tc.password,
				DBName:     "warehouse",
				DBSSLMode:  "require",
			}
			raw := c.DatabaseURL()

			u, err := url.Parse(raw)
			if err != nil {
				t.Fatalf("url.Parse(%q): %v", raw, err)
			}
			if u.Scheme != "postgres" {
				t.Errorf("scheme = %q, want postgres", u.Scheme)
			}
			if u.Host != "db.internal:5432" {
				t.Errorf("host = %q, want db.internal:5432", u.Host)
			}
			if u.Path != "/warehouse" {
				t.Errorf("path = %q, want /warehouse", u.Path)
			}
			if got := u.User.Username(); got != "analytics" {
				t.Errorf("user = %q, want analytics", got)
			}
			gotPass, ok := u.User.Password()
			if !ok {
				t.Fatal("no password in the parsed URL")
			}
			if gotPass != tc.password {
				t.Errorf("password round-tripped as %q, want %q", gotPass, tc.password)
			}
			if got := u.Query().Get("sslmode"); got != "require" {
				t.Errorf("sslmode = %q, want require", got)
			}
		})
	}
}

func TestDatabaseURLShape(t *testing.T) {
	c := &Config{
		DBHost:     "localhost",
		DBPort:     5432,
		DBUser:     "analytics",
		DBPassword: "secret",
		DBName:     "demo_analytics",
		DBSSLMode:  "disable",
	}
	want := "postgres://analytics:secret@localhost:5432/demo_analytics?sslmode=disable"
	if got := c.DatabaseURL(); got != want {
		t.Errorf("DatabaseURL() = %q, want %q", got, want)
	}
}

// --- WorkerQueueMap ----------------------------------------------------------

func TestWorkerQueueMap(t *testing.T) {
	// asynq weights decide how the worker splits its concurrency. A parse
	// that silently drops a queue means tasks on it are never consumed, which
	// looks like a stuck job rather than a config typo.
	cases := []struct {
		name string
		in   string
		want map[string]int
	}{
		{"single", "default:10", map[string]int{"default": 10}},
		{"multiple", "critical:6,default:3,low:1", map[string]int{"critical": 6, "default": 3, "low": 1}},
		{"surrounding spaces", " default : 10 , low : 1 ", map[string]int{"default": 10, "low": 1}},
		{"name with no weight defaults to 1", "default,low", map[string]int{"default": 1, "low": 1}},
		{"mixed", "default:10,low", map[string]int{"default": 10, "low": 1}},

		// Malformed input. None of these may produce an empty map — an empty
		// asynq queue set means the worker consumes nothing at all.
		{"empty falls back", "", map[string]int{"default": 10}},
		{"commas only falls back", ",,,", map[string]int{"default": 10}},
		{"non-numeric weight falls back to 1", "default:abc", map[string]int{"default": 1}},
		{"zero weight falls back to 1", "default:0", map[string]int{"default": 1}},
		{"negative weight falls back to 1", "default:-5", map[string]int{"default": 1}},
		{"empty weight falls back to 1", "default:", map[string]int{"default": 1}},
		{"nameless entry is dropped", ":5", map[string]int{"default": 10}},
		{"nameless entry alongside a good one", ":5,low:2", map[string]int{"low": 2}},
		// Only the first colon separates; the rest is the weight text, which
		// then fails Atoi and lands on 1 rather than inventing a queue name.
		{"second colon is part of the weight", "default:10:20", map[string]int{"default": 1}},
		{"duplicate name keeps the last weight", "default:3,default:7", map[string]int{"default": 7}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			c := &Config{WorkerQueues: tc.in}
			got := c.WorkerQueueMap()
			// The video lane is added to every parse (T-V3), so the cases above
			// state what the *setting* produces and this states what the map
			// always also holds. Written this way rather than by adding
			// `"video": 1` to sixteen expectations, because the next reader
			// should be able to see which entries came from the environment.
			want := map[string]int{queue.QueueVideo: 1}
			for k, v := range tc.want {
				want[k] = v
			}
			if !reflect.DeepEqual(got, want) {
				t.Errorf("WorkerQueueMap(%q) = %v, want %v", tc.in, got, want)
			}
			if len(got) == 0 {
				t.Error("empty queue map: the worker would consume nothing")
			}
		})
	}
}

// TestVideoQueueIsAlwaysConsumed pins the reason the entry above is added
// rather than configured.
//
// `WORKER_QUEUES` is set per deployment. A video lane that had to be listed
// there would reach no deployment that already exists, and every video would
// sit in Redis forever with nothing in any log to say why — the queue is
// named on the task, so asynq would accept it and simply never hand it to a
// server that is not consuming that queue.
func TestVideoQueueIsAlwaysConsumed(t *testing.T) {
	for _, setting := range []string{"", "default:10", "critical:6,default:3", "video:5"} {
		c := &Config{WorkerQueues: setting}
		if _, ok := c.WorkerQueueMap()[queue.QueueVideo]; !ok {
			t.Fatalf("WORKER_QUEUES=%q leaves the video queue unconsumed", setting)
		}
	}
	// An explicit weight is honoured rather than overwritten: an operator who
	// has said what they want has said it.
	if got := (&Config{WorkerQueues: "default:10,video:5"}).WorkerQueueMap()[queue.QueueVideo]; got != 5 {
		t.Fatalf("explicit video weight = %d, want 5", got)
	}
}

// --- splitCSV ----------------------------------------------------------------

func TestSplitCSV(t *testing.T) {
	// CORS_ORIGINS goes through this. An origin that survives with its
	// whitespace attached never matches a browser's Origin header.
	cases := []struct {
		name string
		in   string
		want []string
	}{
		{"empty", "", nil},
		{"single", "http://localhost:5173", []string{"http://localhost:5173"}},
		{"multiple", "a,b,c", []string{"a", "b", "c"}},
		{"spaces trimmed", " a , b ,\tc\t", []string{"a", "b", "c"}},
		{"blank segments dropped", "a,,b,", []string{"a", "b"}},
		{"only separators", ",,,", []string{}},
		{"only whitespace", "  ", []string{}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := splitCSV(tc.in)
			if len(got) != len(tc.want) {
				t.Fatalf("splitCSV(%q) = %v, want %v", tc.in, got, tc.want)
			}
			for i := range got {
				if got[i] != tc.want[i] {
					t.Errorf("splitCSV(%q)[%d] = %q, want %q", tc.in, i, got[i], tc.want[i])
				}
			}
		})
	}
}

// --- Validate ----------------------------------------------------------------

func validCfg() *Config {
	return &Config{
		LLMInterface:          "openai",
		LightLLMInterface:     "openai",
		LLMAPIKey:             "sk-test",
		JWTSecret:             "0123456789abcdef0123456789abcdef",
		DSNEncryptionKeyHex:   "00",
		DBPassword:            "secret",
		WhatsAppProvider:      "whatsapp_business",
		WhatsAppAccessToken:   "wa-token",
		WhatsAppPhoneNumberID: "123",
	}
}

func TestValidateAcceptsAGoodConfig(t *testing.T) {
	if err := validCfg().Validate(); err != nil {
		t.Fatalf("Validate() = %v, want nil", err)
	}
}

func TestValidateRejectsMissingSecrets(t *testing.T) {
	cases := []struct {
		name    string
		mutate  func(*Config)
		wantMsg string
	}{
		{"no llm key", func(c *Config) { c.LLMAPIKey = "" }, "LLM_API_KEY"},
		{"no jwt secret", func(c *Config) { c.JWTSecret = "" }, "ARGENTUM_JWT_SECRET"},
		{"no dsn key", func(c *Config) { c.DSNEncryptionKeyHex = "" }, "ARGENTUM_DSN_KEY"},
		{"no db password", func(c *Config) { c.DBPassword = "" }, "DB_PASSWORD"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			c := validCfg()
			tc.mutate(c)
			err := c.Validate()
			if err == nil {
				t.Fatal("Validate() = nil, want an error")
			}
			if !strings.Contains(err.Error(), tc.wantMsg) {
				t.Errorf("err = %q, want it to name %s", err, tc.wantMsg)
			}
		})
	}
}

func TestValidateChecksBothInterfaceKinds(t *testing.T) {
	cases := []struct {
		name    string
		mutate  func(*Config)
		wantMsg string
	}{
		{"unknown primary", func(c *Config) { c.LLMInterface = "bedrock" }, "LLM_INTERFACE"},
		{"unknown light", func(c *Config) { c.LightLLMInterface = "bedrock" }, "LIGHT_LLM_INTERFACE"},
		// "custom" is the default LLM_PROVIDER, so a config that sets only the
		// provider and never the interface must fail loudly rather than
		// constructing a client against a protocol that does not exist.
		{"provider-only custom", func(c *Config) { c.LLMInterface = ""; c.LLMProvider = "custom" }, "LLM_PROVIDER"},
		{"empty both", func(c *Config) { c.LLMInterface = ""; c.LLMProvider = "" }, "LLM_INTERFACE"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			c := validCfg()
			tc.mutate(c)
			err := c.Validate()
			if err == nil {
				t.Fatal("Validate() = nil, want an error")
			}
			if !strings.Contains(err.Error(), tc.wantMsg) {
				t.Errorf("err = %q, want it to name %s", err, tc.wantMsg)
			}
		})
	}

	for _, iface := range []string{LLMInterfaceOpenAI, LLMInterfaceAnthropic, LLMInterfaceGemini} {
		t.Run("accepts "+iface, func(t *testing.T) {
			c := validCfg()
			c.LLMInterface = iface
			c.LightLLMInterface = iface
			if err := c.Validate(); err != nil {
				t.Errorf("Validate() with %s = %v, want nil", iface, err)
			}
		})
	}
}

func TestValidateWhatsAppCredentialsAreProviderScoped(t *testing.T) {
	// Twilio and the Business API read entirely different env vars. Demanding
	// both would make it impossible to run either.
	t.Run("business api needs its own pair", func(t *testing.T) {
		c := validCfg()
		c.WhatsAppAccessToken = ""
		if err := c.Validate(); err == nil {
			t.Fatal("Validate() = nil with no WhatsApp access token")
		}
		c = validCfg()
		c.WhatsAppPhoneNumberID = ""
		if err := c.Validate(); err == nil {
			t.Fatal("Validate() = nil with no WhatsApp phone number id")
		}
	})

	t.Run("empty provider is treated as the business api", func(t *testing.T) {
		c := validCfg()
		c.WhatsAppProvider = ""
		if err := c.Validate(); err != nil {
			t.Errorf("Validate() = %v, want nil", err)
		}
		c.WhatsAppAccessToken = ""
		if err := c.Validate(); err == nil {
			t.Error("Validate() = nil with no access token on the default provider")
		}
	})

	t.Run("twilio ignores the business api vars", func(t *testing.T) {
		c := validCfg()
		c.WhatsAppProvider = "twilio"
		c.WhatsAppAccessToken = ""
		c.WhatsAppPhoneNumberID = ""
		c.TwilioAccountSID = "AC123"
		c.TwilioAuthToken = "tok"
		c.TwilioFromNumber = "+14155238886"
		if err := c.Validate(); err != nil {
			t.Errorf("Validate() = %v, want nil", err)
		}
		for _, drop := range []func(*Config){
			func(c *Config) { c.TwilioAccountSID = "" },
			func(c *Config) { c.TwilioAuthToken = "" },
			func(c *Config) { c.TwilioFromNumber = "" },
		} {
			cc := *c
			drop(&cc)
			if err := cc.Validate(); err == nil {
				t.Error("Validate() = nil with an incomplete Twilio triple")
			}
		}
	})

	t.Run("an unknown provider skips the whatsapp checks", func(t *testing.T) {
		c := validCfg()
		c.WhatsAppProvider = "none"
		c.WhatsAppAccessToken = ""
		c.WhatsAppPhoneNumberID = ""
		if err := c.Validate(); err != nil {
			t.Errorf("Validate() = %v, want nil", err)
		}
	})
}

// --- Fail-closed in production (T-H3) ----------------------------------------
//
// Two settings degraded quietly instead of refusing to start. The webhook app
// secret unset made VerifyWebhook `return true`, which is the whole of
// /webhook/whatsapp's authentication; an empty CORS_ORIGINS makes the
// middleware reflect any Origin while the dashboard authenticates with a
// cookie. Both are survivable on a laptop and neither is in production, so the
// refusal is scoped to Env=production rather than made unconditional — a
// development stack that has never configured WhatsApp still boots.

func TestValidateRequiresTheWebhookSecretInProduction(t *testing.T) {
	c := validCfg()
	c.Env = "production"
	c.CORSOrigins = []string{"https://app.example.com"}

	if err := c.Validate(); err == nil {
		t.Fatal("Validate() = nil in production with no WHATSAPP_APP_SECRET")
	} else if !strings.Contains(err.Error(), "WHATSAPP_APP_SECRET") {
		t.Errorf("err = %q, want it to name WHATSAPP_APP_SECRET", err)
	}

	c.WhatsAppAppSecret = "app-secret"
	if err := c.Validate(); err != nil {
		t.Errorf("Validate() = %v with the secret set", err)
	}

	// Development boots without it. VerifyWebhook answers false rather than
	// true there, so the endpoint is closed either way — this is about whether
	// the process starts.
	dev := validCfg()
	dev.Env = "development"
	if err := dev.Validate(); err != nil {
		t.Errorf("Validate() = %v in development with no app secret", err)
	}

	// Twilio's signing key is TWILIO_AUTH_TOKEN, which the triple below already
	// requires — in every environment, which is why there is no production-only
	// branch for it.
	tw := validCfg()
	tw.Env = "production"
	tw.CORSOrigins = []string{"https://app.example.com"}
	tw.WhatsAppProvider = "twilio"
	tw.TwilioAccountSID, tw.TwilioAuthToken, tw.TwilioFromNumber = "AC1", "tok", "+1415"
	if err := tw.Validate(); err != nil {
		t.Errorf("Validate() = %v for a complete Twilio triple in production", err)
	}
	tw.TwilioAuthToken = ""
	if err := tw.Validate(); err == nil {
		t.Error("Validate() = nil in production with no Twilio auth token")
	}
}

func TestValidateRequiresCORSOriginsInProduction(t *testing.T) {
	c := validCfg()
	c.Env = "production"
	c.WhatsAppAppSecret = "app-secret"

	if err := c.Validate(); err == nil {
		t.Fatal("Validate() = nil in production with an empty CORS_ORIGINS")
	} else if !strings.Contains(err.Error(), "CORS_ORIGINS") {
		t.Errorf("err = %q, want it to name CORS_ORIGINS", err)
	}

	c.CORSOrigins = []string{"https://app.example.com"}
	if err := c.Validate(); err != nil {
		t.Errorf("Validate() = %v with an origin list", err)
	}

	// An empty list is the default only when an operator has explicitly set the
	// variable to nothing; the built-in default is localhost, which is why this
	// does not break a development boot.
	dev := validCfg()
	dev.Env = "development"
	if err := dev.Validate(); err != nil {
		t.Errorf("Validate() = %v in development with an empty CORS_ORIGINS", err)
	}
}

func TestIsDevelopmentAndIsProduction(t *testing.T) {
	cases := []struct {
		env      string
		wantDev  bool
		wantProd bool
	}{
		{"development", true, false},
		{"production", false, true},
		{"staging", false, false},
		{"", false, false},
		// Exact match, not a prefix or a case-insensitive one: ENV=Production
		// reading as "not production" is worth pinning so a future change to
		// either helper has to be deliberate.
		{"Production", false, false},
	}
	for _, tc := range cases {
		t.Run(tc.env, func(t *testing.T) {
			c := &Config{Env: tc.env}
			if got := c.IsDevelopment(); got != tc.wantDev {
				t.Errorf("IsDevelopment() = %v, want %v", got, tc.wantDev)
			}
			if got := c.IsProduction(); got != tc.wantProd {
				t.Errorf("IsProduction() = %v, want %v", got, tc.wantProd)
			}
		})
	}
}

// --- env helpers -------------------------------------------------------------

func TestGetEnvAsIntFallsBackOnGarbage(t *testing.T) {
	// A typo in a numeric env var must not become 0. AGENT_MAX_ITERATIONS=0
	// is an agent that cannot call a tool; PORT=0 is a random listen port.
	t.Setenv("ARGENTUM_TEST_INT", "not-a-number")
	if got := getEnvAsInt("ARGENTUM_TEST_INT", 42); got != 42 {
		t.Errorf("getEnvAsInt with garbage = %d, want the default 42", got)
	}

	t.Setenv("ARGENTUM_TEST_INT", "")
	if got := getEnvAsInt("ARGENTUM_TEST_INT", 42); got != 42 {
		t.Errorf("getEnvAsInt with an empty value = %d, want the default 42", got)
	}

	t.Setenv("ARGENTUM_TEST_INT", "7")
	if got := getEnvAsInt("ARGENTUM_TEST_INT", 42); got != 7 {
		t.Errorf("getEnvAsInt = %d, want 7", got)
	}

	t.Setenv("ARGENTUM_TEST_INT", "-3")
	if got := getEnvAsInt("ARGENTUM_TEST_INT", 42); got != -3 {
		t.Errorf("getEnvAsInt = %d, want -3", got)
	}
}

func TestGetEnvTreatsEmptyAsUnset(t *testing.T) {
	t.Setenv("ARGENTUM_TEST_STR", "")
	if got := getEnv("ARGENTUM_TEST_STR", "fallback"); got != "fallback" {
		t.Errorf("getEnv with an empty value = %q, want the default", got)
	}
	t.Setenv("ARGENTUM_TEST_STR", "set")
	if got := getEnv("ARGENTUM_TEST_STR", "fallback"); got != "set" {
		t.Errorf("getEnv = %q, want set", got)
	}
}

func TestTrimSpace(t *testing.T) {
	cases := []struct{ in, want string }{
		{"", ""},
		{"   ", ""},
		{"\t\t", ""},
		{"a", "a"},
		{"  a  ", "a"},
		{"\ta b\t", "a b"},
		{" \t mixed \t ", "mixed"},
	}
	for _, tc := range cases {
		if got := trimSpace(tc.in); got != tc.want {
			t.Errorf("trimSpace(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}
