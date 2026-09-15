package guardrails

import (
	"errors"
	"strings"
	"testing"
)

// "1,234,567 may be spoken as about 1.2 million" — and the other roundings a
// spoken answer is for, in both languages.
func TestSpokenFiguresMayRoundWhatTheWrittenAnswerStates(t *testing.T) {
	cases := []struct{ name, written, spoken string }{
		{"a million, to one decimal", "Revenue in March was 1,234,567.", "Revenue in March was about 1.2 million."},
		{"a million, to the unit", "Revenue in March was 1,234,567.", "Revenue was about 1 million."},
		{"the figure in full", "Revenue in March was 1,234,567.", "Revenue was 1,234,567."},
		{"a magnitude, rounded further", "Pendapatan Desember Rp 3,86 Miliar.", "Pendapatan Desember sekitar 3,9 miliar rupiah."},
		{"grouped digits to a magnitude", "Total penjualan Rp 1.234.567.890.", "Total penjualan sekitar 1,2 miliar."},
		{"a percentage", "Margin naik menjadi 18,42%.", "Margin naik menjadi sekitar 18 persen."},
		{"to the nearest hundred", "Ada 312 toko aktif.", "Ada sekitar 300 toko aktif."},
		{"a year, as written", "Penjualan 2024 mencapai 12.500 unit.", "Penjualan 2024 sekitar 12.500 unit."},
		{"a figure from a table cell", "| Gudang | Stok |\n|---|---|\n| Barat | 1.480 |", "Gudang barat menyimpan sekitar 1.500 unit."},
		{"no figure at all", "Revenue in March was 1,234,567.", "Revenue grew in March."},
		{"words that look like numbers and are not", "Gudang barat 1.480 unit.", "Salah satu gudang, the one in the west, sold the most."},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if err := CheckSpokenFigures(c.written, c.spoken); err != nil {
				t.Errorf("refused: %v", err)
			}
		})
	}
}

// "2 million is refused and the check names both figures."
func TestSpokenFigureTheWrittenAnswerDoesNotStateIsRefusedNamingBoth(t *testing.T) {
	err := CheckSpokenFigures("Revenue in March was 1,234,567.", "Revenue in March was about 2 million.")
	var sf *SpokenFigureError
	if !errors.As(err, &sf) {
		t.Fatalf("error = %v, want a SpokenFigureError", err)
	}
	if sf.Spoken != "2 million" || sf.Written != "1,234,567" {
		t.Errorf("spoken %q, written %q", sf.Spoken, sf.Written)
	}
	if msg := err.Error(); !strings.Contains(msg, "2 million") || !strings.Contains(msg, "1,234,567") {
		t.Errorf("the message does not name both figures: %s", msg)
	}
}

// "A spoken text stating a figure absent from the written answer is refused" —
// and the neighbours of that case a looser rule would let through.
func TestSpokenFiguresRefuseWhatIsNotARounding(t *testing.T) {
	cases := []struct{ name, written, spoken, want string }{
		{"a figure the answer never states", "Revenue in March was 1,234,567.", "Revenue was about 1.2 million, up 15 percent.", "15 percent"},
		{"digits the answer does not have", "Pendapatan Rp 3,86 miliar.", "Pendapatan 3,8634 miliar.", "3,8634 miliar"},
		{"the wrong year", "Penjualan tahun 2024 naik.", "Penjualan tahun 2020 naik.", "2020"},
		{"a multiplier misread", "Stok senilai Rp 300 juta.", "Stok senilai sekitar 30 juta.", "30 juta"},
		{"a figure where the answer states none", "Penjualan naik.", "Penjualan naik 5 persen.", "5 persen"},
		{"a digit that was only in code", "```sql\nSELECT * FROM sales LIMIT 50\n```\nTotal 1.234 unit.", "Ada 50 baris.", "50"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			var sf *SpokenFigureError
			if err := CheckSpokenFigures(c.written, c.spoken); !errors.As(err, &sf) {
				t.Fatalf("error = %v, want a SpokenFigureError", err)
			}
			if sf.Spoken != c.want || sf.Spelled {
				t.Errorf("refused %q (spelled %v), want %q", sf.Spoken, sf.Spelled, c.want)
			}
		})
	}
}

// A figure spelled in words cannot be read, so it cannot be vouched for.
func TestSpokenFigureSpelledInWordsIsRefused(t *testing.T) {
	written := "Pendapatan Rp 2.000.000, stok 300 unit, margin 5%, and 100 stores."
	for _, spoken := range []string{
		"Pendapatan sekitar dua juta rupiah.",
		"Stok tiga ratus unit.",
		"Margin lima persen.",
		"Revenue was two million.",
		"About one hundred stores.",
	} {
		var sf *SpokenFigureError
		if err := CheckSpokenFigures(written, spoken); !errors.As(err, &sf) || !sf.Spelled {
			t.Errorf("%q: error = %v, want a spelled-figure refusal", spoken, err)
		}
	}
}
