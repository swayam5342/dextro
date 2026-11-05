package httpx

import (
	"net/netip"
	"sync"
	"time"

	"golang.org/x/time/rate"
)

// RateLimiter limits how often each key may act. It lives in memory because
// there is one API process; a restart resets it, which is acceptable.
type RateLimiter struct {
	every time.Duration
	burst int
	now   func() time.Time

	mu        sync.Mutex
	entries   map[string]*rateEntry
	nextSweep time.Time
}

type rateEntry struct {
	limiter  *rate.Limiter
	lastUsed time.Time
}

// NewRateLimiter allows burst actions at once per key, refilling one per every.
func NewRateLimiter(every time.Duration, burst int) *RateLimiter {
	return &RateLimiter{every: every, burst: burst, now: time.Now, entries: map[string]*rateEntry{}}
}

// Allow reports whether key may act now, and counts the attempt if so.
func (l *RateLimiter) Allow(key string) bool {
	l.mu.Lock()
	defer l.mu.Unlock()

	now := l.now()
	l.sweep(now)
	entry, ok := l.entries[key]
	if !ok {
		entry = &rateEntry{limiter: rate.NewLimiter(rate.Every(l.every), l.burst)}
		l.entries[key] = entry
	}
	entry.lastUsed = now
	return entry.limiter.AllowN(now, 1)
}

// sweep drops keys that have been idle long enough to be fully refilled, since
// forgetting those changes nothing. It keeps memory bounded.
func (l *RateLimiter) sweep(now time.Time) {
	if now.Before(l.nextSweep) {
		return
	}
	refill := l.every * time.Duration(l.burst)
	for key, entry := range l.entries {
		if now.Sub(entry.lastUsed) >= refill {
			delete(l.entries, key)
		}
	}
	l.nextSweep = now.Add(refill)
}

// RateKey turns an IP into a rate-limit key. An IPv6 client usually controls a
// whole /64, so limiting single addresses would be no limit at all.
func RateKey(ip string) string {
	addr, err := netip.ParseAddr(ip)
	if err != nil || !addr.Is6() {
		return ip
	}
	prefix, err := addr.Prefix(64)
	if err != nil {
		return ip
	}
	return prefix.String()
}
