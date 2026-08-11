package handlers

import (
	"strings"
	"testing"
)

// The host/port form's DSN, and the encryption choice it used to make for the
// tenant.
//
// The finding (T-S3's gate, 2026-08-02): the form pinned `sslmode=require` with
// no way to say otherwise, so a database that does not speak TLS could be saved
// through the UI and could never be read — and because nothing opened the
// connection at save time, the tenant found out one turn later, after an agent
// had spent its budget discovering it.

func TestBuildDSNCarriesTheChosenSSLMode(t *testing.T) {
	cases := []struct {
		name    string
		dbType  string
		mode    string
		want    string
		wantErr bool
	}{
		{name: "postgres defaults to require", dbType: "postgres", mode: "", want: "sslmode=require"},
		{name: "postgres can disable", dbType: "postgres", mode: "disable", want: "sslmode=disable"},
		{name: "postgres can verify", dbType: "postgres", mode: "verify-full", want: "sslmode=verify-full"},
		// The mysql driver spells the same choice differently, which is exactly
		// why the mapping is in one table rather than in each caller.
		{name: "mysql defaults to tls", dbType: "mysql", mode: "", want: "tls=true"},
		{name: "mysql can disable", dbType: "mysql", mode: "disable", want: "tls=false"},
		// `tls=true` verifies the chain and the address, so a server holding the
		// certificate mysqld generates for itself is refused with `x509: cannot
		// validate certificate for <ip> because it doesn't contain any IP SANs`.
		// skip-verify is the choice that keeps the encryption and drops the
		// check, and it is the tenant's to make rather than the form's.
		{name: "mysql can encrypt without verifying", dbType: "mysql", mode: "skip-verify", want: "tls=skip-verify"},
		// libpq spells the same intent `require`: encrypted, unverified.
		{name: "postgres spells skip-verify require", dbType: "postgres", mode: "skip-verify", want: "sslmode=require"},
		// go-sql-driver reaches verify-ca only through a registered tls.Config
		// carrying the root pool. Accepting the word here could only mean
		// skip-verify — a promise to check the CA, kept by checking nothing.
		{name: "mysql refuses verify-ca rather than faking it", dbType: "mysql", mode: "verify-ca", wantErr: true},
		{name: "an unknown mode is refused", dbType: "postgres", mode: "sslmode=disable", wantErr: true},
	}
	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			dsn, err := buildDSN(tt.dbType, "", "db.example.com", "5432", "u", "p", "warehouse", tt.mode, false)
			if tt.wantErr {
				if err == nil {
					t.Fatalf("buildDSN(%q) = %q, want an error", tt.mode, dsn)
				}
				return
			}
			if err != nil {
				t.Fatalf("buildDSN: %v", err)
			}
			if !strings.Contains(dsn, tt.want) {
				t.Errorf("dsn = %q, want it to contain %q", dsn, tt.want)
			}
		})
	}
}

// A raw DSN is the admin's own string and is never rewritten — including its
// encryption parameters, which is what "advanced mode" means.
func TestBuildDSNLeavesARawStringAlone(t *testing.T) {
	raw := "postgres://u:p@db.example.com:5432/warehouse?sslmode=verify-ca&application_name=argentum"
	dsn, err := buildDSN("postgres", raw, "", "", "", "", "", "disable", false)
	if err != nil {
		t.Fatalf("buildDSN: %v", err)
	}
	if dsn != raw {
		t.Errorf("dsn = %q, want the raw string unchanged", dsn)
	}
}

// SQL Server reads the same field the other two do (T-H3). It used to ignore it
// and pin TrustServerCertificate=true, which is encryption against a listener
// and nothing at all against something that can answer the address.
func TestBuildDSNSQLServerVerifiesByDefault(t *testing.T) {
	dsn, err := buildDSN("sqlserver", "", "db.example.com", "1433", "u", "p", "warehouse", "", false)
	if err != nil {
		t.Fatalf("buildDSN: %v", err)
	}
	for _, want := range []string{"encrypt=true", "TrustServerCertificate=false", "tlsmin=1.2"} {
		if !strings.Contains(dsn, want) {
			t.Errorf("dsn = %q, want it to contain %q", dsn, want)
		}
	}
}

func TestBuildDSNSQLServerHonoursTheChosenMode(t *testing.T) {
	cases := []struct {
		mode    string
		want    []string
		wantErr bool
	}{
		{mode: "require", want: []string{"encrypt=true", "TrustServerCertificate=false"}},
		{mode: "verify-full", want: []string{"encrypt=true", "TrustServerCertificate=false"}},
		// The self-signed certificate every default SQL Server installation
		// presents. It stays reachable, but only by saying so.
		{mode: "skip-verify", want: []string{"encrypt=true", "TrustServerCertificate=true"}},
		{mode: "disable", want: []string{"encrypt=disable"}},
		{mode: "nonsense", wantErr: true},
	}
	for _, tc := range cases {
		t.Run(tc.mode, func(t *testing.T) {
			dsn, err := buildDSN("sqlserver", "", "db.example.com", "1433", "u", "p", "warehouse", tc.mode, false)
			if tc.wantErr {
				if err == nil {
					t.Fatalf("buildDSN(%q) = %q, want an error", tc.mode, dsn)
				}
				return
			}
			if err != nil {
				t.Fatalf("buildDSN: %v", err)
			}
			for _, want := range tc.want {
				if !strings.Contains(dsn, want) {
					t.Errorf("dsn = %q, want it to contain %q", dsn, want)
				}
			}
		})
	}
}

// --- The production TLS floor (T-H3) -----------------------------------------
//
// Both halves of it. The form path refuses the modes that permit plaintext, and
// the raw path — which is returned verbatim and therefore never saw the mode
// table — is read for the same property before it is stored, because "paste
// your own DSN" was otherwise the documented way around the rule.

func TestBuildDSNRefusesPlaintextModesInProduction(t *testing.T) {
	for _, dbType := range []string{"postgres", "mysql", "sqlserver"} {
		for _, mode := range []string{"disable", "prefer", "allow"} {
			t.Run(dbType+"/"+mode, func(t *testing.T) {
				dsn, err := buildDSN(dbType, "", "db.example.com", "5432", "u", "p", "warehouse", mode, true)
				if err == nil {
					t.Fatalf("buildDSN(%s, %s) = %q with requireTLS, want a refusal", dbType, mode, dsn)
				}
			})
		}
	}
	// And the modes that do encrypt still go through — a floor that refuses
	// everything is a feature nobody can use.
	for _, tc := range []struct{ dbType, mode string }{
		{"postgres", "require"}, {"postgres", "verify-full"}, {"postgres", "skip-verify"},
		{"mysql", "require"}, {"mysql", "skip-verify"},
		{"sqlserver", "require"}, {"sqlserver", "skip-verify"}, {"sqlserver", ""},
	} {
		t.Run(tc.dbType+"/"+tc.mode+"/allowed", func(t *testing.T) {
			if _, err := buildDSN(tc.dbType, "", "db.example.com", "5432", "u", "p", "warehouse", tc.mode, true); err != nil {
				t.Errorf("buildDSN(%s, %q) with requireTLS = %v, want nil", tc.dbType, tc.mode, err)
			}
		})
	}
}

func TestRawDSNEncrypts(t *testing.T) {
	cases := []struct {
		name    string
		dbType  string
		raw     string
		wantErr bool
	}{
		{"postgres uri with require", "postgres", "postgres://u:p@db.example.com:5432/w?sslmode=require", false},
		{"postgres uri with verify-full", "postgres", "postgres://u:p@db.example.com:5432/w?sslmode=verify-full", false},
		{"postgres uri with disable", "postgres", "postgres://u:p@db.example.com:5432/w?sslmode=disable", true},
		// libpq's default. It negotiates TLS and falls back to plaintext without
		// saying so, which is the case a "does it say disable" check misses.
		{"postgres uri with prefer", "postgres", "postgres://u:p@db.example.com:5432/w?sslmode=prefer", true},
		{"postgres uri saying nothing", "postgres", "postgres://u:p@db.example.com:5432/w", true},
		// The keyword/value form is not a URL and would defeat a url.Parse.
		{"postgres keyword form", "postgres", "host=db.example.com port=5432 dbname=w sslmode=verify-full", false},
		{"postgres keyword form with disable", "postgres", "host=db.example.com dbname=w sslmode=disable", true},
		{"mysql with tls", "mysql", "u:p@tcp(db.example.com:3306)/w?tls=true", false},
		{"mysql with skip-verify", "mysql", "u:p@tcp(db.example.com:3306)/w?tls=skip-verify", false},
		{"mysql with tls=false", "mysql", "u:p@tcp(db.example.com:3306)/w?tls=false", true},
		// The driver's own `prefer`: negotiates TLS, connects in the clear when
		// the server declines, says nothing either way.
		{"mysql with tls=preferred", "mysql", "u:p@tcp(db.example.com:3306)/w?tls=preferred", true},
		{"mysql saying nothing", "mysql", "u:p@tcp(db.example.com:3306)/w?parseTime=true", true},
		{"sqlserver encrypt true", "sqlserver", "sqlserver://u:p@db:1433?database=w&encrypt=true", false},
		{"sqlserver encrypt disable", "sqlserver", "sqlserver://u:p@db:1433?database=w&encrypt=disable", true},
		{"sqlserver saying nothing", "sqlserver", "sqlserver://u:p@db:1433?database=w", true},
		// Case is the driver's business, not ours: go-mssqldb matches its
		// parameter names case-insensitively.
		{"sqlserver mixed case key", "sqlserver", "sqlserver://u:p@db:1433?Encrypt=TRUE", false},
		// An engine with no rule stated is not blocked by a rule we did not write.
		{"unknown driver passes", "clickhouse", "clickhouse://u:p@db:9000/w", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := rawDSNEncrypts(tc.dbType, tc.raw)
			if tc.wantErr != (err != nil) {
				t.Errorf("rawDSNEncrypts(%s, %q) = %v, wantErr %v", tc.dbType, tc.raw, err, tc.wantErr)
			}
		})
	}
}

// A raw DSN reaching buildDSN in production goes through the check; outside
// production it is still handed back untouched.
func TestBuildDSNAppliesTheFloorToTheRawPath(t *testing.T) {
	raw := "postgres://u:p@db.example.com:5432/warehouse?sslmode=disable"
	if _, err := buildDSN("postgres", raw, "", "", "", "", "", "", true); err == nil {
		t.Error("buildDSN accepted a plaintext raw DSN with requireTLS")
	}
	got, err := buildDSN("postgres", raw, "", "", "", "", "", "", false)
	if err != nil || got != raw {
		t.Errorf("buildDSN(raw, requireTLS=false) = %q, %v — want the string unchanged", got, err)
	}
}

func TestResolveSSLModeDefaultsToRequire(t *testing.T) {
	for _, dbType := range []string{"postgres", "mysql"} {
		got, err := resolveSSLMode(dbType, "")
		if err != nil {
			t.Fatalf("resolveSSLMode(%q, \"\"): %v", dbType, err)
		}
		want, err := resolveSSLMode(dbType, "require")
		if err != nil {
			t.Fatalf("resolveSSLMode(%q, require): %v", dbType, err)
		}
		if got != want {
			t.Errorf("%s: empty = %q, require = %q — an unset mode must be the safe one", dbType, got, want)
		}
	}
}
