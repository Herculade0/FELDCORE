package citizenid

import (
	"sync"
	"time"
)

// limiter is a fixed-window count of failed sign-ins, matching the
// administrator login gate: 8 failures, then a few minutes of lockout.
type limiter struct {
	mu     sync.Mutex
	max    int
	window time.Duration
	hits   map[string]*bucket
}

type bucket struct {
	n       int
	pending int
	start   time.Time
}

func newLimiter(max int, window time.Duration) *limiter {
	return &limiter{max: max, window: window, hits: map[string]*bucket{}}
}

func (l *limiter) blocked(key string, now time.Time) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	b, ok := l.hits[key]
	if !ok {
		return false
	}
	if now.Sub(b.start) > l.window {
		delete(l.hits, key)
		return false
	}
	return b.n >= l.max
}

func (l *limiter) fail(key string, now time.Time) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if len(l.hits) > 4096 {
		for k, b := range l.hits {
			if now.Sub(b.start) > l.window {
				delete(l.hits, k)
			}
		}
	}
	b, ok := l.hits[key]
	if !ok || now.Sub(b.start) > l.window {
		l.hits[key] = &bucket{n: 1, start: now}
		return
	}
	b.n++
}

func (l *limiter) reset(key string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	delete(l.hits, key)
}

// begin reserves one attempt against every key. The reservation counts
// toward the lockout, so two requests cannot both pass the check before
// either of them is recorded. done(true) records a failure. done(false)
// releases the reservation and leaves the counters as they were.
func (l *limiter) begin(now time.Time, keys ...string) (func(failed bool), bool) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if len(l.hits) > 4096 {
		for k, b := range l.hits {
			if b.pending == 0 && now.Sub(b.start) > l.window {
				delete(l.hits, k)
			}
		}
	}
	for _, k := range keys {
		b := l.hits[k]
		if b == nil || now.Sub(b.start) > l.window {
			continue
		}
		if b.n+b.pending >= l.max {
			return nil, false
		}
	}
	for _, k := range keys {
		b := l.hits[k]
		if b == nil || now.Sub(b.start) > l.window {
			l.hits[k] = &bucket{pending: 1, start: now}
			continue
		}
		b.pending++
	}
	return func(failed bool) {
		l.mu.Lock()
		defer l.mu.Unlock()
		for _, k := range keys {
			b := l.hits[k]
			if b == nil {
				continue
			}
			if b.pending > 0 {
				b.pending--
			}
			if !failed {
				continue
			}
			if now.Sub(b.start) > l.window {
				b.n = 1
				b.start = now
				continue
			}
			b.n++
		}
	}, true
}
