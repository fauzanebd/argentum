package metabase

import (
	"testing"
)

func TestSQLServerMetabaseDetails_sslEnabled(t *testing.T) {
	d, err := SQLServerMetabaseDetails("sqlserver://user:secret@db.example:1433?database=app&encrypt=true&TrustServerCertificate=false")
	if err != nil {
		t.Fatal(err)
	}
	if got, _ := d["host"].(string); got != "db.example" {
		t.Fatalf("host: %q", got)
	}
	if got, _ := d["port"].(int); got != 1433 {
		t.Fatalf("port: %v", got)
	}
	if got, _ := d["db"].(string); got != "app" {
		t.Fatalf("db: %q", got)
	}
	if got, _ := d["user"].(string); got != "user" {
		t.Fatalf("user: %q", got)
	}
	if got, _ := d["password"].(string); got != "secret" {
		t.Fatalf("password: redacted check failed")
	}
	if got, _ := d["ssl"].(bool); !got {
		t.Fatalf("ssl: want true, got %v", got)
	}
}

func TestSQLServerMetabaseDetails_sslDisabled(t *testing.T) {
	d, err := SQLServerMetabaseDetails("sqlserver://user:secret@db.example:1433?database=app&encrypt=disable")
	if err != nil {
		t.Fatal(err)
	}
	if got, _ := d["ssl"].(bool); got {
		t.Fatalf("ssl: want false, got %v", got)
	}
}
