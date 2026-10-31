package security

import (
	"sync"
	"time"
)

// RateLimiter manages rate limits per user key.
type RateLimiter struct {
	mu          sync.Mutex
	rate        int           // allowed requests
	interval    time.Duration // per interval
	userBuckets map[int64]*bucket
}

type bucket struct {
	tokens    int
	lastCheck time.Time
}

// NewRateLimiter creates a new sliding window or token-bucket rate limiter.
func NewRateLimiter(rate int, interval time.Duration) *RateLimiter {
	limiter := &RateLimiter{
		rate:        rate,
		interval:    interval,
		userBuckets: make(map[int64]*bucket),
	}

	// Periodic cleanup of idle entries
	go limiter.cleanupRoutine()
	return limiter
}

// Allow checks if the user is allowed to perform an action.
func (rl *RateLimiter) Allow(userID int64) bool {
	rl.mu.Lock()
	defer rl.mu.Unlock()

	now := time.Now()
	b, exists := rl.userBuckets[userID]
	if !exists {
		rl.userBuckets[userID] = &bucket{
			tokens:    rl.rate - 1,
			lastCheck: now,
		}
		return true
	}

	// Replenish tokens based on elapsed time
	elapsed := now.Sub(b.lastCheck)
	if elapsed > rl.interval {
		b.tokens = rl.rate
		b.lastCheck = now
	}

	if b.tokens > 0 {
		b.tokens--
		return true
	}

	return false
}

// Reset resets rate limit for a user.
func (rl *RateLimiter) Reset(userID int64) {
	rl.mu.Lock()
	delete(rl.userBuckets, userID)
	rl.mu.Unlock()
}

// cleanupRoutine removes stale buckets periodically.
func (rl *RateLimiter) cleanupRoutine() {
	ticker := time.NewTicker(5 * time.Minute)
	defer ticker.Stop()

	for range ticker.C {
		rl.mu.Lock()
		now := time.Now()
		for id, b := range rl.userBuckets {
			if now.Sub(b.lastCheck) > 2*rl.interval {
				delete(rl.userBuckets, id)
			}
		}
		rl.mu.Unlock()
	}
}
