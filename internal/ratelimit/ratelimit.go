package ratelimit

import (
	"net"
	"net/http"
	"sync"
	"time"
)

// bucket tracks request counts for a single IP.
type bucket struct {
	count     int
	resetAt   time.Time
}

// Limiter is an in-memory per-IP rate limiter.
type Limiter struct {
	limit   int
	window  time.Duration
	buckets map[string]*bucket
	mu      sync.Mutex
}

// New creates a rate limiter that allows `limit` requests per `windowSec` seconds.
func New(limit, windowSec int) *Limiter {
	l := &Limiter{
		limit:   limit,
		window:  time.Duration(windowSec) * time.Second,
		buckets: make(map[string]*bucket),
	}
	// Background cleanup of expired entries
	go l.cleanup()
	return l
}

// Allow returns true if the request from `ip` is within the rate limit.
func (l *Limiter) Allow(ip string) bool {
	l.mu.Lock()
	defer l.mu.Unlock()

	now := time.Now()
	b, ok := l.buckets[ip]
	if !ok || now.After(b.resetAt) {
		l.buckets[ip] = &bucket{count: 1, resetAt: now.Add(l.window)}
		return true
	}

	b.count++
	return b.count <= l.limit
}

// Middleware returns a chi-compatible middleware that rate limits by client IP.
func (l *Limiter) Middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ip, _, err := net.SplitHostPort(r.RemoteAddr)
		if err != nil {
			ip = r.RemoteAddr // Fallback if no port
		}
		if !l.Allow(ip) {
			http.Error(w, "Too many requests", http.StatusTooManyRequests)
			return
		}
		next.ServeHTTP(w, r)
	})
}

func (l *Limiter) cleanup() {
	ticker := time.NewTicker(60 * time.Second)
	defer ticker.Stop()
	for range ticker.C {
		l.mu.Lock()
		now := time.Now()
		for ip, b := range l.buckets {
			if now.After(b.resetAt) {
				delete(l.buckets, ip)
			}
		}
		l.mu.Unlock()
	}
}
