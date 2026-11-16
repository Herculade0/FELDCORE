package system

import (
	"context"
	"sync"
	"time"
)

// Sampler takes one Stats sample per interval in a single background loop,
// keeps a rolling window of recent samples, and fans each new sample out to
// subscribers.
//
// Sampling centrally (rather than once per HTTP request or WebSocket client)
// matters for correctness: CPU and network figures are deltas since the
// previous reading, so several independent readers would corrupt each other's
// numbers. It also lets a freshly opened dashboard draw full history at once.
type Sampler struct {
	c        *Collector
	interval time.Duration
	size     int

	mu   sync.RWMutex
	ring []*Stats
	subs map[chan *Stats]struct{}
}

// NewSampler returns a Sampler that keeps the last `size` samples.
func NewSampler(c *Collector, interval time.Duration, size int) *Sampler {
	return &Sampler{
		c:        c,
		interval: interval,
		size:     size,
		subs:     map[chan *Stats]struct{}{},
	}
}

// Run samples until ctx is cancelled. Call it in its own goroutine.
func (s *Sampler) Run(ctx context.Context) {
	// Prime the delta-based counters so the first published sample is real.
	_, _ = s.c.Sample(ctx)

	t := time.NewTicker(s.interval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			st, err := s.c.Sample(ctx)
			if err != nil {
				continue
			}
			s.publish(st)
		}
	}
}

func (s *Sampler) publish(st *Stats) {
	s.mu.Lock()
	s.ring = append(s.ring, st)
	if len(s.ring) > s.size {
		s.ring = s.ring[len(s.ring)-s.size:]
	}
	for ch := range s.subs {
		// Never block the sampler on a slow client: drop the stale sample
		// and replace it with the newest.
		select {
		case ch <- st:
		default:
			select {
			case <-ch:
			default:
			}
			select {
			case ch <- st:
			default:
			}
		}
	}
	s.mu.Unlock()
}

// Latest returns the most recent sample, or nil before the first one.
func (s *Sampler) Latest() *Stats {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if len(s.ring) == 0 {
		return nil
	}
	return s.ring[len(s.ring)-1]
}

// History returns a copy of the rolling window, oldest first.
func (s *Sampler) History() []*Stats {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]*Stats, len(s.ring))
	copy(out, s.ring)
	return out
}

// Subscribe returns the current history plus a channel of subsequent samples.
// Taking both under one lock guarantees no sample is missed or duplicated.
// Call the returned function to unsubscribe.
func (s *Sampler) Subscribe() (history []*Stats, ch <-chan *Stats, cancel func()) {
	c := make(chan *Stats, 1)
	s.mu.Lock()
	history = make([]*Stats, len(s.ring))
	copy(history, s.ring)
	s.subs[c] = struct{}{}
	s.mu.Unlock()
	return history, c, func() {
		s.mu.Lock()
		delete(s.subs, c)
		s.mu.Unlock()
	}
}
