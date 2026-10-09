package main

import (
	"fmt"
	"strconv"
	"strings"
	"time"
	"unicode"
)

// ageUnits are the units the age field accepts.  A week is spelled w and a minute m, so
// `1d3h10m` is one day, three hours, and ten minutes.
var ageUnits = map[rune]time.Duration{
	'w': 7 * 24 * time.Hour,
	'd': 24 * time.Hour,
	'h': time.Hour,
	'm': time.Minute,
	's': time.Second,
}

// defaultPruneAge is the age the delete-old-sessions field opens on.  A week is the shortest
// span that is plainly stale rather than merely quiet, and the field is edited in place.
const defaultPruneAge = "7d"

// parseAge reads the picker's age field: one or more <number><unit> groups, where the unit
// is w, d, h, m, or s.  `1d`, `3h`, `10m`, `1d3h10m`, and `1d 3h` all name a duration.
//
// A number with no unit is refused rather than guessed at, so `7` is an error and not a
// week.  sh2pil-sessions parses the same field with the same grammar before it deletes anything; this
// parse is what tells the reader that a typo is a typo before a helper is asked to run.
func parseAge(raw string) (time.Duration, error) {
	text := strings.TrimSpace(raw)
	if text == "" {
		return 0, fmt.Errorf("an age is required, such as 1d, 3h, or 1d3h10m")
	}
	runes := []rune(text)
	var total time.Duration
	index, groups := 0, 0
	for index < len(runes) {
		if unicode.IsSpace(runes[index]) {
			index++
			continue
		}
		start := index
		for index < len(runes) && unicode.IsDigit(runes[index]) {
			index++
		}
		if start == index {
			return 0, fmt.Errorf("%q is not a number in %q", string(runes[index]), text)
		}
		number, err := strconv.Atoi(string(runes[start:index]))
		if err != nil {
			return 0, fmt.Errorf("%q is not a number in %q", string(runes[start:index]), text)
		}
		if index >= len(runes) {
			return 0, fmt.Errorf("%q names no unit; use w, d, h, m, or s",
				string(runes[start:]))
		}
		unit, known := ageUnits[unicode.ToLower(runes[index])]
		if !known {
			return 0, fmt.Errorf("%q is not a unit; use w, d, h, m, or s", string(runes[index]))
		}
		total += time.Duration(number) * unit
		index++
		groups++
	}
	if groups == 0 {
		return 0, fmt.Errorf("an age is required, such as 1d, 3h, or 1d3h10m")
	}
	if total <= 0 {
		return 0, fmt.Errorf("an age must be more than zero")
	}
	return total, nil
}

// ageSummary says how much of each unit an age holds, for a reader who typed the age in
// another unit: "1d3h10m" is read back as "1d 3h 10m", and 36h as "1d 12h".
func ageSummary(age time.Duration) string {
	if age <= 0 {
		return "0s"
	}
	units := []struct {
		name string
		size time.Duration
	}{{"w", 7 * 24 * time.Hour}, {"d", 24 * time.Hour}, {"h", time.Hour}, {"m", time.Minute},
		{"s", time.Second}}
	parts := make([]string, 0, len(units))
	for _, unit := range units {
		// A week is only worth saying when the age is exact weeks; 8d reads as 1w 1d.
		count := int64(age / unit.size)
		if count == 0 {
			continue
		}
		age -= time.Duration(count) * unit.size
		parts = append(parts, fmt.Sprintf("%d%s", count, unit.name))
	}
	return strings.Join(parts, " ")
}
