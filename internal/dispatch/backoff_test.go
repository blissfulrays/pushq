package dispatch

import (
	"testing"
	"time"

	"github.com/blissfulrays/pushq/internal/store"
)

// The GCP-documented example: min=10s, max=300s, maxDoublings=3 →
// 10, 20, 40, 80, 160, 240, 300, 300, …
func TestBackoffGoldenVector(t *testing.T) {
	rc := store.RetryConfig{
		MinBackoff:   10 * time.Second,
		MaxBackoff:   300 * time.Second,
		MaxDoublings: 3,
		MaxAttempts:  -1,
	}
	want := []time.Duration{
		10 * time.Second,  // after attempt 1
		20 * time.Second,  // 2
		40 * time.Second,  // 3
		80 * time.Second,  // 4  (doubled maxDoublings times)
		160 * time.Second, // 5  (linear +80)
		240 * time.Second, // 6  (linear +80)
		300 * time.Second, // 7  (320 capped)
		300 * time.Second, // 8
	}
	for i, w := range want {
		attempts := i + 1
		if got := Backoff(rc, attempts); got != w {
			t.Errorf("Backoff(attempts=%d) = %v, want %v", attempts, got, w)
		}
	}
}

func TestBackoffDefaults(t *testing.T) {
	rc := store.DefaultRetryConfig() // min=100ms max=1h doublings=16
	if got := Backoff(rc, 1); got != 100*time.Millisecond {
		t.Errorf("first backoff = %v, want 100ms", got)
	}
	// 100ms * 2^16 = 6553.6s > 1h → capped
	if got := Backoff(rc, 17); got != time.Hour {
		t.Errorf("17th backoff = %v, want 1h cap", got)
	}
	// Far-out attempts stay capped, no overflow.
	if got := Backoff(rc, 10_000); got != time.Hour {
		t.Errorf("10000th backoff = %v, want 1h cap", got)
	}
}

func TestBackoffZeroDoublings(t *testing.T) {
	rc := store.RetryConfig{MinBackoff: 5 * time.Second, MaxBackoff: 30 * time.Second, MaxDoublings: 0}
	// Doubles zero times then grows linearly by min each retry: 5, 10, 15, 20, 25, 30, 30…
	want := []time.Duration{5, 10, 15, 20, 25, 30, 30}
	for i, w := range want {
		if got := Backoff(rc, i+1); got != w*time.Second {
			t.Errorf("Backoff(attempts=%d) = %v, want %vs", i+1, got, w)
		}
	}
}

func TestExhausted(t *testing.T) {
	at := func(d time.Duration) *time.Time {
		tm := time.Unix(1000, 0).Add(d)
		return &tm
	}
	base := time.Unix(1000, 0)
	now := base.Add(time.Hour)

	cases := []struct {
		name     string
		rc       store.RetryConfig
		attempts int
		first    *time.Time
		want     bool
	}{
		{"both unlimited never exhausts", store.RetryConfig{MaxAttempts: -1, MaxRetryDuration: 0}, 1_000_000, at(0), false},
		{"attempts only, not reached", store.RetryConfig{MaxAttempts: 5}, 4, at(0), false},
		{"attempts only, reached", store.RetryConfig{MaxAttempts: 5}, 5, at(0), true},
		{"duration only, not reached", store.RetryConfig{MaxAttempts: -1, MaxRetryDuration: 2 * time.Hour}, 100, at(0), false},
		{"duration only, reached", store.RetryConfig{MaxAttempts: -1, MaxRetryDuration: 30 * time.Minute}, 1, at(0), true},
		// The GCP quirk: BOTH set → retry until BOTH reached.
		{"both set, only attempts hit", store.RetryConfig{MaxAttempts: 3, MaxRetryDuration: 2 * time.Hour}, 10, at(0), false},
		{"both set, only duration hit", store.RetryConfig{MaxAttempts: 100, MaxRetryDuration: 30 * time.Minute}, 3, at(0), false},
		{"both set, both hit", store.RetryConfig{MaxAttempts: 3, MaxRetryDuration: 30 * time.Minute}, 3, at(0), true},
		{"no first attempt yet", store.RetryConfig{MaxAttempts: -1, MaxRetryDuration: time.Minute}, 1, nil, false},
	}
	for _, c := range cases {
		if got := Exhausted(c.rc, c.attempts, c.first, now); got != c.want {
			t.Errorf("%s: Exhausted = %v, want %v", c.name, got, c.want)
		}
	}
}
