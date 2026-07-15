package gql

import (
	"context"
	"sync"
	"time"
)

// RateLimiter paces requests with a sliding window: at most capacity requests
// are allowed within any window. A nil limiter performs no pacing, and a
// single limiter can be shared by every client copy in the process so the
// account-wide request rate stays bounded.
type RateLimiter struct {
	capacity int
	window   time.Duration

	mu     sync.Mutex
	stamps []time.Time

	now func() time.Time
}

func NewRateLimiter(capacity int, window time.Duration) *RateLimiter {
	if capacity <= 0 || window <= 0 {
		return nil
	}
	return &RateLimiter{
		capacity: capacity,
		window:   window,
		now:      time.Now,
	}
}

// Wait blocks until the caller may issue a request or the context is done.
func (l *RateLimiter) Wait(ctx context.Context) error {
	if l == nil {
		return nil
	}

	for {
		now := l.now()

		l.mu.Lock()
		l.trimExpired(now)
		if len(l.stamps) < l.capacity {
			l.stamps = append(l.stamps, now)
			l.mu.Unlock()
			return nil
		}
		waitFor := l.stamps[0].Add(l.window).Sub(now)
		l.mu.Unlock()

		if err := sleepContext(ctx, waitFor); err != nil {
			return err
		}
	}
}

func (l *RateLimiter) trimExpired(now time.Time) {
	cutoff := now.Add(-l.window)
	kept := 0
	for kept < len(l.stamps) && !l.stamps[kept].After(cutoff) {
		kept++
	}
	if kept > 0 {
		l.stamps = append(l.stamps[:0], l.stamps[kept:]...)
	}
}

func sleepContext(ctx context.Context, delay time.Duration) error {
	if delay <= 0 {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
			return nil
		}
	}
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}
