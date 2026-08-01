package metric

import (
	"testing"
	"time"
)

func TestPreviousPeriodAbutsAndMatchesLength(t *testing.T) {
	w := Window{
		From: time.Date(2024, 2, 1, 0, 0, 0, 0, time.UTC),
		To:   time.Date(2024, 3, 1, 0, 0, 0, 0, time.UTC), // 29 days (leap Feb)
	}
	prev, err := w.Shift(ComparePreviousPeriod)
	if err != nil {
		t.Fatalf("Shift: %v", err)
	}
	// It ends exactly where the primary begins…
	if !prev.To.Equal(w.From) {
		t.Errorf("prev.To = %v, want %v (adjacent)", prev.To, w.From)
	}
	// …and is the same length.
	if prev.To.Sub(prev.From) != w.To.Sub(w.From) {
		t.Errorf("prev length = %v, want %v", prev.To.Sub(prev.From), w.To.Sub(w.From))
	}
}

func TestSamePeriodLastYearKeepsMonthAndDay(t *testing.T) {
	w := Window{
		From: time.Date(2024, 3, 1, 0, 0, 0, 0, time.UTC),
		To:   time.Date(2024, 4, 1, 0, 0, 0, 0, time.UTC),
	}
	ly, err := w.Shift(CompareSamePeriodLastYear)
	if err != nil {
		t.Fatalf("Shift: %v", err)
	}
	if ly.From.Year() != 2023 || ly.From.Month() != 3 || ly.From.Day() != 1 {
		t.Errorf("ly.From = %v, want 2023-03-01", ly.From)
	}
	if ly.To.Year() != 2023 || ly.To.Month() != 4 {
		t.Errorf("ly.To = %v, want 2023-04", ly.To)
	}
}

func TestValidationWindowIsTrailingSevenDays(t *testing.T) {
	now := time.Date(2024, 6, 10, 12, 0, 0, 0, time.UTC)
	w := ValidationWindow(now)
	if !w.To.Equal(now) || !w.From.Equal(now.AddDate(0, 0, -7)) {
		t.Errorf("window = %+v, want [now-7d, now]", w)
	}
}
