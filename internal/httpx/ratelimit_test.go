package httpx

import (
	"testing"
	"time"
)

func newTestLimiter(every time.Duration, burst int) (*RateLimiter, *time.Time) {
	now := time.Date(2026, 11, 5, 12, 0, 0, 0, time.UTC)
	l := NewRateLimiter(every, burst)
	l.now = func() time.Time { return now }
	return l, &now
}

func TestRateLimiterAllowsTheBurstThenRefuses(t *testing.T) {
	l, _ := newTestLimiter(time.Minute, 3)
	for i := range 3 {
		if !l.Allow("a") {
			t.Fatalf("attempt %d was refused", i+1)
		}
	}
	if l.Allow("a") {
		t.Fatal("attempt past the burst was allowed")
	}
}

func TestRateLimiterRefillsOverTime(t *testing.T) {
	l, now := newTestLimiter(time.Minute, 1)
	l.Allow("a")
	*now = now.Add(time.Minute)
	if !l.Allow("a") {
		t.Fatal("attempt after the refill was refused")
	}
}

func TestRateLimiterKeepsKeysApart(t *testing.T) {
	l, _ := newTestLimiter(time.Minute, 1)
	l.Allow("a")
	if !l.Allow("b") {
		t.Fatal("another key was refused")
	}
}

func TestRateLimiterForgetsIdleKeys(t *testing.T) {
	l, now := newTestLimiter(time.Minute, 2)
	l.Allow("a")
	l.Allow("b")
	*now = now.Add(time.Hour)
	l.Allow("c")
	if got := len(l.entries); got != 1 {
		t.Errorf("entries = %d, want only the fresh key", got)
	}
}

func TestRateKeyGroupsIPv6ByNetwork(t *testing.T) {
	tests := []struct{ ip, want string }{
		{"203.0.113.7", "203.0.113.7"},
		{"2001:db8:1:2:aaaa::1", "2001:db8:1:2::/64"},
		{"2001:db8:1:2:bbbb::2", "2001:db8:1:2::/64"},
		{"unparseable", "unparseable"},
	}
	for _, tt := range tests {
		if got := RateKey(tt.ip); got != tt.want {
			t.Errorf("RateKey(%q) = %q, want %q", tt.ip, got, tt.want)
		}
	}
}
