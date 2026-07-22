package taskfailure

import (
	"regexp"
	"strconv"
	"strings"
	"time"
)

// maxParsedRateLimitCooldown caps any reset time we derive from provider
// text. Providers occasionally echo a stray large number (e.g. a token
// count) next to wording that superficially matches a retry pattern; capping
// keeps a misparse from parking a runtime/model pair out of rotation for an
// unreasonable stretch. This is independent of, and smaller than, the fixed
// 4h fallback FORK-4 uses when no pattern matches at all.
const maxParsedRateLimitCooldown = 24 * time.Hour

// rfc3339ish matches an RFC3339 timestamp embedded anywhere in the error
// text (e.g. a JSON body echoed verbatim: `"reset_at":"2026-07-22T12:00:00Z"`).
var rfc3339ish = regexp.MustCompile(`\d{4}-\d{2}-\d{2}T\d{2}:\d{2}:\d{2}(?:\.\d+)?(?:Z|[+-]\d{2}:\d{2})`)

// epochSecondsRe matches a labeled Unix epoch (seconds or milliseconds),
// as several providers surface machine-readable reset hints this way, e.g.
// `"reset_at": 1737532800` or `X-RateLimit-Reset: 1737532800`.
var epochSecondsRe = regexp.MustCompile(`(?i)(?:rate.?limit.?reset|reset(?:_at|s_at|_time)?)"?\s*[:=]\s*"?(\d{10,13})"?`)

// relativeRetryRe matches the common "try again in Ns" / "retry after N
// seconds" / "wait Nm" phrasing used by OpenAI, Anthropic, and similar
// provider error strings.
var relativeRetryRe = regexp.MustCompile(`(?i)(?:try again in|retry(?:\s+after|\s+in)?|wait(?:\s+for)?)\s*[:\s]{0,3}(\d+(?:\.\d+)?)\s*(milliseconds?|ms|seconds?|secs?|s|minutes?|mins?|m|hours?|hrs?|h)\b`)

// ParseRateLimitReset best-effort parses a rate-limit reset/retry time out
// of a raw provider error string (FORK-4). It never fails loudly: ok is
// false whenever no recognized pattern matches, and the caller is expected
// to fall back to a fixed cooldown in that case.
//
// Recognized shapes, checked most-specific first:
//  1. An absolute RFC3339 timestamp embedded in the text.
//  2. A labeled Unix epoch (seconds or milliseconds).
//  3. A relative duration ("try again in 20s", "retry after 2 minutes").
func ParseRateLimitReset(rawError string) (time.Time, bool) {
	trimmed := strings.TrimSpace(rawError)
	if trimmed == "" {
		return time.Time{}, false
	}

	if m := rfc3339ish.FindString(trimmed); m != "" {
		if t, err := time.Parse(time.RFC3339Nano, m); err == nil {
			return t, true
		}
	}

	if m := epochSecondsRe.FindStringSubmatch(trimmed); m != nil {
		if n, err := strconv.ParseInt(m[1], 10, 64); err == nil {
			var t time.Time
			if len(m[1]) >= 13 {
				t = time.UnixMilli(n)
			} else {
				t = time.Unix(n, 0)
			}
			if until := time.Until(t); until > 0 && until <= maxParsedRateLimitCooldown {
				return t.UTC(), true
			}
		}
	}

	if m := relativeRetryRe.FindStringSubmatch(trimmed); m != nil {
		n, err := strconv.ParseFloat(m[1], 64)
		if err == nil && n > 0 {
			var d time.Duration
			switch strings.ToLower(m[2]) {
			case "ms", "millisecond", "milliseconds":
				d = time.Duration(n * float64(time.Millisecond))
			case "s", "sec", "secs", "second", "seconds":
				d = time.Duration(n * float64(time.Second))
			case "m", "min", "mins", "minute", "minutes":
				d = time.Duration(n * float64(time.Minute))
			case "h", "hr", "hrs", "hour", "hours":
				d = time.Duration(n * float64(time.Hour))
			}
			if d > 0 && d <= maxParsedRateLimitCooldown {
				return time.Now().Add(d).UTC(), true
			}
		}
	}

	return time.Time{}, false
}
