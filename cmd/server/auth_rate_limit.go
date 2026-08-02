package main

import (
	"errors"
	"net"
	"net/http"
	"strconv"
	"sync"
	"time"
)

type loginAttempt struct {
	failures     int
	windowStart  time.Time
	blockedUntil time.Time
}

type loginLimiter struct {
	mu          sync.Mutex
	attempts    map[string]loginAttempt
	maxFailures int
	window      time.Duration
	blockFor    time.Duration
}

func newLoginLimiter(maxFailures int, window, blockFor time.Duration) *loginLimiter {
	return &loginLimiter{
		attempts:    make(map[string]loginAttempt),
		maxFailures: maxFailures,
		window:      window,
		blockFor:    blockFor,
	}
}

func (l *loginLimiter) remaining(key string, now time.Time) time.Duration {
	l.mu.Lock()
	defer l.mu.Unlock()
	attempt, ok := l.attempts[key]
	if !ok {
		return 0
	}
	if !attempt.blockedUntil.IsZero() {
		if now.Before(attempt.blockedUntil) {
			return attempt.blockedUntil.Sub(now)
		}
		delete(l.attempts, key)
		return 0
	}
	if now.Sub(attempt.windowStart) > l.window {
		delete(l.attempts, key)
	}
	return 0
}

func (l *loginLimiter) failure(key string, now time.Time) time.Duration {
	l.mu.Lock()
	defer l.mu.Unlock()
	attempt := l.attempts[key]
	if attempt.windowStart.IsZero() || now.Sub(attempt.windowStart) > l.window {
		attempt = loginAttempt{windowStart: now}
	}
	attempt.failures++
	if attempt.failures >= l.maxFailures {
		attempt.blockedUntil = now.Add(l.blockFor)
	}
	l.attempts[key] = attempt
	if attempt.blockedUntil.IsZero() {
		return 0
	}
	return attempt.blockedUntil.Sub(now)
}

func (l *loginLimiter) success(key string) {
	l.mu.Lock()
	delete(l.attempts, key)
	l.mu.Unlock()
}

var administratorLoginLimiter = newLoginLimiter(5, 10*time.Minute, 15*time.Minute)

func loginClientKey(r *http.Request) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err == nil && host != "" {
		return host
	}
	if r.RemoteAddr != "" {
		return r.RemoteAddr
	}
	return "unknown"
}

func writeLoginRateLimit(w http.ResponseWriter, remaining time.Duration) {
	seconds := int64(remaining.Seconds())
	if seconds < 1 {
		seconds = 1
	}
	w.Header().Set("Retry-After", strconv.FormatInt(seconds, 10))
	writeJSON(w, http.StatusTooManyRequests, map[string]any{
		"error":      "too many login attempts",
		"retryAfter": seconds,
	})
}

func (s *server) handleAuthLoginRateLimited(w http.ResponseWriter, r *http.Request) {
	key := loginClientKey(r)
	now := time.Now()
	if remaining := administratorLoginLimiter.remaining(key, now); remaining > 0 {
		writeLoginRateLimit(w, remaining)
		return
	}

	username, password, err := decodeAuthBody(r)
	if err != nil {
		if remaining := administratorLoginLimiter.failure(key, now); remaining > 0 {
			writeLoginRateLimit(w, remaining)
			return
		}
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}

	token, claims, err := s.auth.login(r.Context(), username, password)
	if err != nil {
		if errors.Is(err, errInvalidCredentials) {
			remaining := administratorLoginLimiter.failure(key, now)
			s.log.Warn("administrator login rejected", "remote", key)
			if remaining > 0 {
				writeLoginRateLimit(w, remaining)
				return
			}
			writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "invalid username or password"})
			return
		}
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "login failed"})
		return
	}

	administratorLoginLimiter.success(key)
	writeJSON(w, http.StatusOK, map[string]any{
		"token":     token,
		"expiresAt": claims.Expires,
		"user":      map[string]string{"username": claims.Username, "role": claims.Role},
	})
}
