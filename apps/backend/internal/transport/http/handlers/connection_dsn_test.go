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
			dsn, err := buildDSN(tt.dbType, "", "db.example.com", "5432", "u", "p", "warehouse", tt.mode)
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
	dsn, err := buildDSN("postgres", raw, "", "", "", "", "", "disable")
	if err != nil {
		t.Fatalf("buildDSN: %v", err)
	}
	if dsn != raw {
		t.Errorf("dsn = %q, want the raw string unchanged", dsn)
	}
}

// A driver with no choice on offer — SQL Server sets its own encryption
// parameters — is unaffected by the field rather than refusing it.
func TestBuildDSNIgnoresSSLModeWhereTheDriverHasNoChoice(t *testing.T) {
	dsn, err := buildDSN("sqlserver", "", "db.example.com", "1433", "u", "p", "warehouse", "disable")
	if err != nil {
		t.Fatalf("buildDSN: %v", err)
	}
	if !strings.Contains(dsn, "encrypt=true") {
		t.Errorf("dsn = %q, want sqlserver's own encryption parameters", dsn)
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
