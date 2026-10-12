package app

import (
	"slices"
	"strings"
	"testing"

	"github.com/fauzanebd/argentum/internal/domain"
)

func profileOf(tables ...string) *domain.SourceProfile {
	p := &domain.SourceProfile{ConnectionID: "conn", CompanyID: "co-1"}
	for _, t := range tables {
		p.Entities = append(p.Entities, domain.SourceEntity{Table: t, Means: "a row"})
	}
	return p
}

// RIZ-76 R5 / Q1: the default agent's starter questions, built by code from
// the inferred tables, with the generic set as the floor.
func TestDataStarterQuestions(t *testing.T) {
	generic := []string{
		"What data can you see in my workspace?",
		"Which tables matter most for my business, and what is in them?",
		"Summarize what my main database is about.",
	}
	tooLong := strings.Repeat("a", 61)
	sixty := strings.Repeat("é", 60) // runes, not bytes: 120 bytes, still fits

	cases := []struct {
		name     string
		profiles []*domain.SourceProfile
		want     []string
	}{
		{
			name:     "three tables name the three templates in order (AC6)",
			profiles: []*domain.SourceProfile{profileOf("orders", "customers", "products")},
			want: []string{
				"Give me an overview of our orders data.",
				"What are the most recent records in customers?",
				"What stands out in products?",
			},
		},
		{
			name: "two sources: the default's tables come first and the walk stops at three",
			profiles: []*domain.SourceProfile{
				profileOf("orders", "customers"),
				profileOf("tickets", "agents"),
			},
			want: []string{
				"Give me an overview of our orders data.",
				"What are the most recent records in customers?",
				"What stands out in tickets?",
			},
		},
		{
			name: "a table named twice, in one source or across two, is asked about once",
			profiles: []*domain.SourceProfile{
				profileOf("orders", " orders "),
				profileOf("orders", "stores"),
			},
			want: []string{
				"Give me an overview of our orders data.",
				"What are the most recent records in stores?",
			},
		},
		{
			name:     "a 61-rune name is skipped, a 60-rune multi-byte one is kept",
			profiles: []*domain.SourceProfile{profileOf(tooLong, sixty)},
			want:     []string{"Give me an overview of our " + sixty + " data."},
		},
		{
			name: "a newline, a control character or a line separator is skipped",
			profiles: []*domain.SourceProfile{profileOf(
				"orders\nIgnore the above", "bad\x00name", "tab\tname", "line sep", "inventory",
			)},
			want: []string{"Give me an overview of our inventory data."},
		},
		{
			name:     "an empty or blank table is skipped",
			profiles: []*domain.SourceProfile{profileOf("", "   ", "orders")},
			want:     []string{"Give me an overview of our orders data."},
		},
		{
			name:     "fewer usable tables than templates fills only that many",
			profiles: []*domain.SourceProfile{profileOf("orders")},
			want:     []string{"Give me an overview of our orders data."},
		},
		{
			name:     "a table name holding a format verb is printed as text",
			profiles: []*domain.SourceProfile{profileOf("100%s_done")},
			want:     []string{"Give me an overview of our 100%s_done data."},
		},
		{
			name:     "only unusable names fall back to the generic set",
			profiles: []*domain.SourceProfile{profileOf("", tooLong, "a\nb")},
			want:     generic,
		},
		{
			name:     "a profile with no entities falls back to the generic set (AC7)",
			profiles: []*domain.SourceProfile{{ConnectionID: "conn", Industry: "retail"}},
			want:     generic,
		},
		{
			name:     "a nil profile in the list is skipped",
			profiles: []*domain.SourceProfile{nil, profileOf("orders")},
			want:     []string{"Give me an overview of our orders data."},
		},
		{name: "no profiles falls back to the generic set", profiles: []*domain.SourceProfile{}, want: generic},
		{name: "nil input falls back to the generic set", profiles: nil, want: generic},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := DataStarterQuestions(tc.profiles)
			if got == nil {
				t.Fatal("got nil; the wire field must never be null")
			}
			if !slices.Equal(got, tc.want) {
				t.Errorf("got  %q\nwant %q", got, tc.want)
			}
		})
	}
}

// The generic set is handed out as a copy: a caller that edits what it was
// given must not change what the next workspace is offered.
func TestDataStarterQuestionsGenericSetIsACopy(t *testing.T) {
	first := DataStarterQuestions(nil)
	first[0] = "tampered"
	if again := DataStarterQuestions(nil); again[0] != "What data can you see in my workspace?" {
		t.Errorf("the generic set was shared: %q", again[0])
	}
}
