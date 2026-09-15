package speech

import (
	"errors"
	"strings"
	"testing"
)

func TestSpeakableRefusesStructure(t *testing.T) {
	cases := map[string]string{
		"a table":        "| Gudang | Stok |\n|---|---|\n| Barat | 1.480 |",
		"a table's rule": "Gudang\n---|---\nBarat",
		"one stray pipe": "Gudang | barat.",
		"code":           "Jalankan `SELECT 1`.",
		"SQL":            "SELECT sum(total) FROM sales",
		"a link":         "Lihat https://example.com/report.",
		"nothing left":   "  \n** **\n",
		"too long":       strings.Repeat("kata ", 300),
	}
	for name, in := range cases {
		var ns *NotSpeakableError
		if _, err := Speakable(in); !errors.As(err, &ns) {
			t.Errorf("%s: error = %v, want a NotSpeakableError", name, err)
		}
	}
}

// Decoration goes and the words stay; each line becomes a sentence; a hyphen
// inside a word and a dash between clauses are prose, not a table.
func TestSpeakableRemovesDecorationAndKeepsTheWords(t *testing.T) {
	in := "## Ringkasan\n- **Penjualan** Maret sekitar 1,2 juta [1]\n- Gudang [barat](/dashboards/9) kira-kira 1.500 unit — naik.\n1. *Stok* aman"
	want := "Ringkasan. Penjualan Maret sekitar 1,2 juta. Gudang barat kira-kira 1.500 unit — naik. Stok aman."
	got, err := Speakable(in)
	if err != nil {
		t.Fatalf("Speakable: %v", err)
	}
	if got != want {
		t.Errorf("got  %q\nwant %q", got, want)
	}
}
