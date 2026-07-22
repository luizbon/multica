package taskfailure

import (
	"fmt"
	"testing"
	"time"
)

// timeWithin asserts got is within delta of want, tolerating the small
// scheduling jitter between computing an expectation and calling the parser.
func timeWithin(t *testing.T, got, want time.Time, delta time.Duration) {
	t.Helper()
	diff := got.Sub(want)
	if diff < 0 {
		diff = -diff
	}
	if diff > delta {
		t.Errorf("got %v, want ~%v (diff %v > %v)", got, want, diff, delta)
	}
}

// TestParseRateLimitResetRFC3339 covers the absolute-timestamp shape,
// including when it's embedded in a larger JSON-ish error body.
func TestParseRateLimitResetRFC3339(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		in   string
		want time.Time
	}{
		{
			"bare RFC3339 with Z",
			"rate limited: reset_at=2026-07-22T12:00:00Z",
			time.Date(2026, 7, 22, 12, 0, 0, 0, time.UTC),
		},
		{
			"embedded in JSON body",
			`{"error":{"code":"rate_limited","reset_at":"2026-08-01T00:30:00Z"}}`,
			time.Date(2026, 8, 1, 0, 30, 0, 0, time.UTC),
		},
		{
			"offset timezone",
			"Too many requests, try again after 2026-07-22T09:00:00-03:00",
			time.Date(2026, 7, 22, 9, 0, 0, 0, time.FixedZone("", -3*60*60)),
		},
		{
			"fractional seconds",
			"reset at 2026-07-22T12:00:00.500Z",
			time.Date(2026, 7, 22, 12, 0, 0, 500000000, time.UTC),
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, ok := ParseRateLimitReset(c.in)
			if !ok {
				t.Fatalf("ParseRateLimitReset(%q) ok = false, want true", c.in)
			}
			if !got.Equal(c.want) {
				t.Errorf("ParseRateLimitReset(%q) = %v, want %v", c.in, got, c.want)
			}
		})
	}
}

// TestParseRateLimitResetLabeledEpoch covers the labeled Unix-epoch shape
// (seconds and milliseconds), several provider label spellings, and the
// "epoch already elapsed" rejection that keeps a stale/past reset hint from
// being taken as valid.
func TestParseRateLimitResetLabeledEpoch(t *testing.T) {
	t.Parallel()

	future := time.Now().Add(2 * time.Hour).Truncate(time.Second)

	cases := []struct {
		name string
		in   string
		want time.Time
		ok   bool
	}{
		{
			"X-RateLimit-Reset header seconds",
			fmt.Sprintf("X-RateLimit-Reset: %d", future.Unix()),
			future.UTC(), true,
		},
		{
			"reset_at json field",
			fmt.Sprintf(`{"reset_at": %d}`, future.Unix()),
			future.UTC(), true,
		},
		{
			"resets_at spelling",
			fmt.Sprintf("resets_at=%d", future.Unix()),
			future.UTC(), true,
		},
		{
			"reset_time spelling",
			fmt.Sprintf("reset_time=%d", future.Unix()),
			future.UTC(), true,
		},
		{
			"rate limit reset with spaces",
			fmt.Sprintf("rate limit reset: %d", future.Unix()),
			future.UTC(), true,
		},
		{
			"milliseconds epoch",
			fmt.Sprintf("reset_at=%d", future.UnixMilli()),
			future.UTC(), true,
		},
		{
			"epoch already in the past is rejected",
			fmt.Sprintf("reset_at=%d", time.Now().Add(-1*time.Hour).Unix()),
			time.Time{}, false,
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, ok := ParseRateLimitReset(c.in)
			if ok != c.ok {
				t.Fatalf("ParseRateLimitReset(%q) ok = %v, want %v", c.in, ok, c.ok)
			}
			if !c.ok {
				return
			}
			timeWithin(t, got, c.want, 2*time.Second)
		})
	}
}

// TestParseRateLimitResetRelativeDuration covers the "try again in Ns" /
// "retry after N minutes" / "wait Nm" phrasing and its supported units.
func TestParseRateLimitResetRelativeDuration(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		in   string
		want time.Duration
	}{
		{"try again in seconds", "Rate limited. Try again in 20s.", 20 * time.Second},
		{"retry after seconds word", "429: retry after 45 seconds", 45 * time.Second},
		{"retry in minutes", "please retry in 2 minutes", 2 * time.Minute},
		{"wait minutes abbreviation", "wait 5m before retrying", 5 * time.Minute},
		{"wait hours", "wait for 1 hour before retrying", 1 * time.Hour},
		{"milliseconds", "try again in 500ms", 500 * time.Millisecond},
		{"fractional hours", "retry after 1.5 hours", 90 * time.Minute},
		{"hrs abbreviation", "retry after 2 hrs", 2 * time.Hour},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			before := time.Now()
			got, ok := ParseRateLimitReset(c.in)
			if !ok {
				t.Fatalf("ParseRateLimitReset(%q) ok = false, want true", c.in)
			}
			want := before.Add(c.want)
			timeWithin(t, got, want, 2*time.Second)
		})
	}
}

// TestParseRateLimitReset24hCap pins maxParsedRateLimitCooldown: a parsed
// reset (from either the epoch or relative-duration path) that would land
// more than 24h out is treated as an unparseable misparse (ok=false) rather
// than trusted outright, while a reset landing exactly at the cap is still
// accepted.
func TestParseRateLimitReset24hCap(t *testing.T) {
	t.Parallel()

	t.Run("relative duration under cap is accepted", func(t *testing.T) {
		got, ok := ParseRateLimitReset("retry after 23 hours")
		if !ok {
			t.Fatal("ok = false, want true for 23h (under the 24h cap)")
		}
		timeWithin(t, got, time.Now().Add(23*time.Hour), 2*time.Second)
	})

	t.Run("relative duration exactly at cap is accepted", func(t *testing.T) {
		got, ok := ParseRateLimitReset("retry after 24 hours")
		if !ok {
			t.Fatal("ok = false, want true for exactly 24h (at the cap)")
		}
		timeWithin(t, got, time.Now().Add(24*time.Hour), 2*time.Second)
	})

	t.Run("relative duration over cap is rejected", func(t *testing.T) {
		_, ok := ParseRateLimitReset("retry after 25 hours")
		if ok {
			t.Fatal("ok = true, want false for 25h (over the 24h cap)")
		}
	})

	t.Run("epoch under cap is accepted", func(t *testing.T) {
		future := time.Now().Add(23 * time.Hour).Truncate(time.Second)
		got, ok := ParseRateLimitReset(fmt.Sprintf("reset_at=%d", future.Unix()))
		if !ok {
			t.Fatal("ok = false, want true for epoch 23h out")
		}
		timeWithin(t, got, future, 2*time.Second)
	})

	t.Run("epoch over cap is rejected", func(t *testing.T) {
		future := time.Now().Add(25 * time.Hour)
		_, ok := ParseRateLimitReset(fmt.Sprintf("reset_at=%d", future.Unix()))
		if ok {
			t.Fatal("ok = true, want false for epoch 25h out (over the 24h cap)")
		}
	})
}

// TestParseRateLimitResetNoMatchOrEmpty pins the failed-parse contract:
// empty/whitespace-only text and text with no recognized pattern both
// return ok=false so the caller falls back to the fixed cooldown.
func TestParseRateLimitResetNoMatchOrEmpty(t *testing.T) {
	t.Parallel()

	cases := []string{
		"",
		"   ",
		"\n\t \n",
		"429 Too Many Requests",
		"the server is overloaded, please try again later",
	}
	for _, in := range cases {
		if _, ok := ParseRateLimitReset(in); ok {
			t.Errorf("ParseRateLimitReset(%q) ok = true, want false", in)
		}
	}
}

// TestParseRateLimitResetPrecedence pins the documented most-specific-first
// ordering: RFC3339 beats labeled epoch and relative duration, and an epoch
// that fails its own validity check (here: already elapsed) still falls
// through to a relative-duration match found later in the same text.
func TestParseRateLimitResetPrecedence(t *testing.T) {
	t.Parallel()

	t.Run("RFC3339 wins over relative duration", func(t *testing.T) {
		in := "rate limited, reset_at=2026-07-22T12:00:00Z, try again in 20s"
		got, ok := ParseRateLimitReset(in)
		if !ok {
			t.Fatal("ok = false, want true")
		}
		want := time.Date(2026, 7, 22, 12, 0, 0, 0, time.UTC)
		if !got.Equal(want) {
			t.Errorf("got %v, want %v (RFC3339 should win)", got, want)
		}
	})

	t.Run("RFC3339 wins over labeled epoch", func(t *testing.T) {
		future := time.Now().Add(2 * time.Hour)
		in := fmt.Sprintf("reset_at=2026-07-22T12:00:00Z, X-RateLimit-Reset: %d", future.Unix())
		got, ok := ParseRateLimitReset(in)
		if !ok {
			t.Fatal("ok = false, want true")
		}
		want := time.Date(2026, 7, 22, 12, 0, 0, 0, time.UTC)
		if !got.Equal(want) {
			t.Errorf("got %v, want %v (RFC3339 should win)", got, want)
		}
	})

	t.Run("expired epoch falls through to relative duration", func(t *testing.T) {
		expiredEpoch := time.Now().Add(-1 * time.Hour).Unix()
		in := fmt.Sprintf("reset_at=%d but try again in 30s", expiredEpoch)
		got, ok := ParseRateLimitReset(in)
		if !ok {
			t.Fatal("ok = false, want true (should fall through to the relative match)")
		}
		timeWithin(t, got, time.Now().Add(30*time.Second), 2*time.Second)
	})
}
