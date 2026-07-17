package api

import (
	"testing"
	"time"
)

func TestResolvePeriod(t *testing.T) {
	now := time.Date(2026, 7, 17, 12, 0, 0, 0, time.UTC)

	cases := []struct {
		period     string
		wantOK     bool
		wantBucket string
		wantSince  time.Time
	}{
		{"", true, "hour", now.Add(-24 * time.Hour)},
		{"24h", true, "hour", now.Add(-24 * time.Hour)},
		{"7d", true, "day", now.Add(-7 * 24 * time.Hour)},
		{"30d", true, "day", now.Add(-30 * 24 * time.Hour)},
		{"1h", false, "", time.Time{}},
		{"7D", false, "", time.Time{}},
		{"garbage", false, "", time.Time{}},
	}

	for _, c := range cases {
		since, bucket, ok := resolvePeriod(c.period, now)
		if ok != c.wantOK {
			t.Errorf("resolvePeriod(%q) ok = %v, want %v", c.period, ok, c.wantOK)
			continue
		}
		if !c.wantOK {
			continue
		}
		if bucket != c.wantBucket {
			t.Errorf("resolvePeriod(%q) bucket = %q, want %q", c.period, bucket, c.wantBucket)
		}
		if !since.Equal(c.wantSince) {
			t.Errorf("resolvePeriod(%q) since = %v, want %v", c.period, since, c.wantSince)
		}
	}
}

func TestIsErrorStatus(t *testing.T) {
	cases := map[string]bool{
		"ok":           false,
		"error":        true,
		"denied":       true,
		"rate_limited": true,
		"timeout":      true,
		"":             true,
	}
	for status, want := range cases {
		if got := isErrorStatus(status); got != want {
			t.Errorf("isErrorStatus(%q) = %v, want %v", status, got, want)
		}
	}
}

func TestRate(t *testing.T) {
	cases := []struct {
		part, total int
		want        float64
	}{
		{0, 0, 0},
		{5, 0, 0},
		{1, 4, 0.25},
		{3, 3, 1},
	}
	for _, c := range cases {
		if got := rate(c.part, c.total); got != c.want {
			t.Errorf("rate(%d,%d) = %v, want %v", c.part, c.total, got, c.want)
		}
	}
}
