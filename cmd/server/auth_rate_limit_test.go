package main

import (
	"testing"
	"time"
)

func TestLoginLimiterBlocksAndResets(t *testing.T) {
	limiter := newLoginLimiter(3, time.Minute, 2*time.Minute)
	now := time.Now()
	key := "127.0.0.1"

	if remaining := limiter.failure(key, now); remaining != 0 {
		t.Fatalf("first failure must not block, got %s", remaining)
	}
	if remaining := limiter.failure(key, now.Add(time.Second)); remaining != 0 {
		t.Fatalf("second failure must not block, got %s", remaining)
	}
	remaining := limiter.failure(key, now.Add(2*time.Second))
	if remaining <= 0 {
		t.Fatal("third failure must block")
	}
	if got := limiter.remaining(key, now.Add(3*time.Second)); got <= 0 {
		t.Fatal("blocked client must have retry duration")
	}

	limiter.success(key)
	if got := limiter.remaining(key, now.Add(4*time.Second)); got != 0 {
		t.Fatalf("successful login must clear limiter, got %s", got)
	}
}

func TestLoginLimiterExpiresFailureWindow(t *testing.T) {
	limiter := newLoginLimiter(2, time.Minute, 5*time.Minute)
	now := time.Now()
	key := "127.0.0.2"

	if got := limiter.failure(key, now); got != 0 {
		t.Fatalf("unexpected initial block: %s", got)
	}
	if got := limiter.failure(key, now.Add(2*time.Minute)); got != 0 {
		t.Fatalf("expired window must restart the counter, got %s", got)
	}
}
