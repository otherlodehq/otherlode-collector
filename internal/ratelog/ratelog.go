// Package ratelog limits how often a log line of one kind is written.
package ratelog

import (
	"sync"
	"time"
)

// Limiter lets one line per key through in each interval and counts the
// lines it held back. It is safe for concurrent use.
type Limiter struct {
	every time.Duration
	now   func() time.Time

	mu   sync.Mutex
	keys map[string]*state
}

type state struct {
	last     time.Time
	unlogged int
}

// New returns a Limiter that allows one line per key in each interval of
// every. now gives the current time.
func New(every time.Duration, now func() time.Time) *Limiter {
	return &Limiter{every: every, now: now, keys: make(map[string]*state)}
}

// Allow reports whether the caller should write a line for key. When it
// does, unlogged is the number of calls for key held back since the
// previous line. The first call for a key is always allowed.
func (l *Limiter) Allow(key string) (unlogged int, ok bool) {
	l.mu.Lock()
	defer l.mu.Unlock()
	now := l.now()
	s := l.keys[key]
	if s == nil {
		l.keys[key] = &state{last: now}
		return 0, true
	}
	if now.Sub(s.last) < l.every {
		s.unlogged++
		return 0, false
	}
	unlogged = s.unlogged
	s.last = now
	s.unlogged = 0
	return unlogged, true
}
