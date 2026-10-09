package main

import (
	"testing"
	"time"
)

// TestParseAgeAcceptsTheWrittenForms pins the grammar the reader types and pib parses.
func TestParseAgeAcceptsTheWrittenForms(t *testing.T) {
	cases := []struct {
		field string
		want  time.Duration
	}{
		{"1d", 24 * time.Hour},
		{"3h", 3 * time.Hour},
		{"10m", 10 * time.Minute},
		{"1d3h10m", 24*time.Hour + 3*time.Hour + 10*time.Minute},
		{"1d 3h", 27 * time.Hour},
		{"  2w ", 14 * 24 * time.Hour},
		{"90s", 90 * time.Second},
		{"1W", 7 * 24 * time.Hour},
		{"36h", 36 * time.Hour},
	}
	for _, c := range cases {
		got, err := parseAge(c.field)
		if err != nil {
			t.Errorf("parseAge(%q) failed: %v", c.field, err)
			continue
		}
		if got != c.want {
			t.Errorf("parseAge(%q) = %v, want %v", c.field, got, c.want)
		}
	}
}

// TestParseAgeRefusesWhatItCannotRead pins the refusals: a unit is required, so a bare
// number is never guessed at.
func TestParseAgeRefusesWhatItCannotRead(t *testing.T) {
	for _, field := range []string{"", "   ", "7", "d", "1x", "1d3", "-1d", "0d", "1d1d2"} {
		if got, err := parseAge(field); err == nil {
			t.Errorf("parseAge(%q) = %v, want an error", field, got)
		}
	}
}

// TestAgeSummaryReadsAnAgeBack pins the words the confirmation box uses, so an age typed in
// hours is still readable when it is shown again.
func TestAgeSummaryReadsAnAgeBack(t *testing.T) {
	cases := []struct {
		age  time.Duration
		want string
	}{
		{36 * time.Hour, "1d 12h"},
		{24*time.Hour + 3*time.Hour + 10*time.Minute, "1d 3h 10m"},
		{10 * time.Minute, "10m"},
		{14 * 24 * time.Hour, "2w"},
	}
	for _, c := range cases {
		if got := ageSummary(c.age); got != c.want {
			t.Errorf("ageSummary(%v) = %q, want %q", c.age, got, c.want)
		}
	}
}
