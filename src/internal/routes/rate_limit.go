package routes

import (
	"net/http"
	"sync"
	"time"

	"workspace/src/internal/utils"

	"golang.org/x/time/rate"
)

// perUserLimiter tracks a rate.Limiter per authenticated user and evicts entries that have
// been idle past ttl, so memory stays bounded regardless of how many distinct users have ever
// connected. Must run after JWTMiddleware (relies on "user_id" already being in context).
type perUserLimiter struct {
	mu       sync.Mutex
	limiters map[string]*limiterEntry
	rate     rate.Limit
	burst    int
	ttl      time.Duration
}

type limiterEntry struct {
	limiter  *rate.Limiter
	lastSeen time.Time
}

func newPerUserLimiter(r rate.Limit, burst int, ttl time.Duration) *perUserLimiter {
	l := &perUserLimiter{
		limiters: make(map[string]*limiterEntry),
		rate:     r,
		burst:    burst,
		ttl:      ttl,
	}
	go l.evictLoop()
	return l
}

func (l *perUserLimiter) evictLoop() {
	ticker := time.NewTicker(l.ttl)
	defer ticker.Stop()
	for range ticker.C {
		cutoff := time.Now().Add(-l.ttl)
		l.mu.Lock()
		for key, entry := range l.limiters {
			if entry.lastSeen.Before(cutoff) {
				delete(l.limiters, key)
			}
		}
		l.mu.Unlock()
	}
}

func (l *perUserLimiter) allow(key string) bool {
	l.mu.Lock()
	entry, ok := l.limiters[key]
	if !ok {
		entry = &limiterEntry{limiter: rate.NewLimiter(l.rate, l.burst)}
		l.limiters[key] = entry
	}
	entry.lastSeen = time.Now()
	limiter := entry.limiter
	l.mu.Unlock()
	return limiter.Allow()
}

// messagingWriteLimiter throttles mutating messaging actions (send message, edit, delete,
// react, upload, forward) per user to blunt flood/spam abuse. 10 req/s sustained with a burst
// of 20 comfortably covers legitimate fast typing/reacting while capping automated flooding.
var messagingWriteLimiter = newPerUserLimiter(rate.Limit(10), 20, 10*time.Minute)

// RateLimitMiddleware rejects requests over the per-user rate with 429 Too Many Requests.
// Wire onto write-heavy route groups only; must sit after JWTMiddleware in the chain.
func RateLimitMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		uid, _ := r.Context().Value("user_id").(string)
		if uid == "" {
			next.ServeHTTP(w, r)
			return
		}
		if !messagingWriteLimiter.allow(uid) {
			utils.WriteError(w, http.StatusTooManyRequests, "Too many requests, slow down")
			return
		}
		next.ServeHTTP(w, r)
	})
}
