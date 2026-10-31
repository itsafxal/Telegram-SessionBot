package security

import (
	"testing"
	"time"
)

func TestRateLimiter(t *testing.T) {
	rl := NewRateLimiter(3, 100*time.Millisecond)
	userID := int64(1001)

	// First 3 requests should be allowed
	if !rl.Allow(userID) {
		t.Fatalf("first request should be allowed")
	}
	if !rl.Allow(userID) {
		t.Fatalf("second request should be allowed")
	}
	if !rl.Allow(userID) {
		t.Fatalf("third request should be allowed")
	}

	// Fourth request should be rejected
	if rl.Allow(userID) {
		t.Fatalf("fourth request should have been rate limited")
	}

	// Wait for interval replenishment
	time.Sleep(120 * time.Millisecond)

	if !rl.Allow(userID) {
		t.Fatalf("request after replenishment should be allowed")
	}
}

func TestRateLimiterReset(t *testing.T) {
	rl := NewRateLimiter(1, time.Minute)
	userID := int64(2002)

	if !rl.Allow(userID) {
		t.Fatalf("first request should be allowed")
	}
	if rl.Allow(userID) {
		t.Fatalf("second request should be blocked")
	}

	rl.Reset(userID)

	if !rl.Allow(userID) {
		t.Fatalf("request after Reset should be allowed")
	}
}
