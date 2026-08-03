package dispatch

import (
	"time"

	"github.com/blissfulrays/pushq/internal/store"
)

// Backoff returns the delay before the next attempt, given that `attempts`
// attempts have completed. GCP Cloud Tasks semantics: the interval starts at
// MinBackoff, doubles MaxDoublings times, then grows linearly by
// 2^MaxDoublings × MinBackoff per retry, capped at MaxBackoff.
//
// Example (GCP docs): min=10s max=300s doublings=3 →
// 10, 20, 40, 80, 160, 240, 300, 300, …
func Backoff(rc store.RetryConfig, attempts int) time.Duration {
	n := attempts - 1 // 0-based retry index
	if n < 0 {
		n = 0
	}
	d := rc.MaxDoublings
	if d < 0 {
		d = 0
	}
	min := rc.MinBackoff
	if min < 0 {
		min = 0
	}

	var delay time.Duration
	if n <= d {
		delay = shiftCapped(min, n, rc.MaxBackoff)
	} else {
		base := shiftCapped(min, d, rc.MaxBackoff)
		if base >= rc.MaxBackoff {
			delay = rc.MaxBackoff
		} else {
			steps := n - d
			// base * (1 + steps), guarding overflow.
			if base > 0 && time.Duration(steps) > (1<<62)/base {
				delay = rc.MaxBackoff
			} else {
				delay = base + time.Duration(steps)*base
			}
		}
	}
	if delay > rc.MaxBackoff {
		delay = rc.MaxBackoff
	}
	if delay < min {
		delay = min
	}
	return delay
}

// shiftCapped computes v << n without overflowing past cap.
func shiftCapped(v time.Duration, n int, cap time.Duration) time.Duration {
	if v <= 0 {
		return 0
	}
	for i := 0; i < n; i++ {
		v <<= 1
		if v >= cap || v < 0 {
			return cap
		}
	}
	return v
}

// Exhausted decides whether retries stop after `attempts` completed attempts.
// GCP quirk adopted for parity: when BOTH limits are set, the task keeps
// retrying until BOTH are reached. An unlimited value (-1 attempts / 0
// duration) leaves the other limit in sole control.
func Exhausted(rc store.RetryConfig, attempts int, firstAttempt *time.Time, now time.Time) bool {
	unlimitedAttempts := rc.MaxAttempts == -1
	unlimitedDuration := rc.MaxRetryDuration == 0

	attemptsHit := !unlimitedAttempts && attempts >= rc.MaxAttempts
	durationHit := !unlimitedDuration && firstAttempt != nil && now.Sub(*firstAttempt) >= rc.MaxRetryDuration

	switch {
	case unlimitedAttempts && unlimitedDuration:
		return false
	case unlimitedAttempts:
		return durationHit
	case unlimitedDuration:
		return attemptsHit
	default:
		return attemptsHit && durationHit
	}
}
