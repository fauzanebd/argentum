package domain

import (
	"strings"
	"testing"
)

// TestFilenameSearchTerms pins the two halves T-P14 needs, and they are needed
// by two different people.
//
// The raw name is what the person who just uploaded the file types, because it
// is the only string about the document they are certain of — and Postgres
// tokenizes `09-scan-invoice.pdf` as one lexeme, so nothing but the whole name
// matches it. The split stem is what everybody else types: "invoice", "scan
// invoice", the month in a report's name. Losing either half loses one of those
// two queries, silently, in the shape the ticket was written from — a document
// that is present, parsed and answerable, reported as not existing.
func TestFilenameSearchTerms(t *testing.T) {
	cases := []struct {
		name     string
		filename string
		want     []string
		absent   []string
	}{
		{
			name:     "the ticket's own filename",
			filename: "09-scan-invoice.pdf",
			want:     []string{"09-scan-invoice.pdf", "09", "scan", "invoice"},
			// The extension survives only inside the whole name. Indexed on its
			// own it would be a term every document holds, which discriminates
			// nothing and outranks nothing.
			absent: []string{" pdf"},
		},
		{
			name:     "underscores are separators too",
			filename: "Laporan_Penjualan_Q4_2024.pdf",
			want:     []string{"Laporan_Penjualan_Q4_2024.pdf", "Laporan", "Penjualan", "Q4", "2024"},
		},
		{
			name:     "a name with no separators still indexes itself",
			filename: "kontrak.pdf",
			want:     []string{"kontrak.pdf", "kontrak"},
		},
		{
			name:     "dots inside the stem split as well",
			filename: "2024.11.invoice.pdf",
			want:     []string{"2024.11.invoice.pdf", "2024", "11", "invoice"},
		},
		{
			name:     "no extension is not a special case",
			filename: "surat-kuasa",
			want:     []string{"surat-kuasa", "surat", "kuasa"},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := FilenameSearchTerms(tc.filename)
			for _, want := range tc.want {
				if !strings.Contains(got, want) {
					t.Errorf("FilenameSearchTerms(%q) = %q, missing %q", tc.filename, got, want)
				}
			}
			for _, absent := range tc.absent {
				if strings.Contains(got, absent) {
					t.Errorf("FilenameSearchTerms(%q) = %q, should not carry %q", tc.filename, got, absent)
				}
			}
			if strings.Contains(got, "  ") {
				t.Errorf("FilenameSearchTerms(%q) = %q, holds a double space", tc.filename, got)
			}
		})
	}
}

// TestFilenameSearchTermsOfNothing: a document row with no filename indexes
// nothing rather than a space, so an empty query term cannot match every chunk
// this tenant owns.
func TestFilenameSearchTermsOfNothing(t *testing.T) {
	for _, in := range []string{"", "   "} {
		if got := FilenameSearchTerms(in); got != "" {
			t.Errorf("FilenameSearchTerms(%q) = %q, want empty", in, got)
		}
	}
}
