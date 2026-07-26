package metabase

import (
	"testing"
)

func TestPostgresMetabaseDetails_postgresURL(t *testing.T) {
	d, err := PostgresMetabaseDetails("postgres://user:secret@db.example:55432/app?sslmode=disable")
	if err != nil {
		t.Fatal(err)
	}
	if got, _ := d["host"].(string); got != "db.example" {
		t.Fatalf("host: %q", got)
	}
	if got, _ := d["port"].(uint16); got != 55432 {
		t.Fatalf("port: %v", got)
	}
	if got, _ := d["dbname"].(string); got != "app" {
		t.Fatalf("dbname: %q", got)
	}
	if got, _ := d["user"].(string); got != "user" {
		t.Fatalf("user: %q", got)
	}
	if got, _ := d["password"].(string); got != "secret" {
		t.Fatalf("password: redacted check failed")
	}
}
