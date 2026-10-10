package processor

import (
	"log/slog"
	"sync"
)

// maxMismatchKeys bounds the set of mismatches a mismatchLog remembers.
const maxMismatchKeys = 10000

// mismatchKey names one kind of mismatch: one service sending one value
// that differs from the collector's.
type mismatchKey struct {
	namespace string
	service   string
	value     string
}

// mismatchLog picks the level for a mismatch log line. The first time a
// service sends a value, the line is a warning, so an operator sees it at
// the default level. Each later payload of that service with that value
// logs at debug, since an agent repeats its value on every payload.
type mismatchLog struct {
	mu   sync.Mutex
	seen map[mismatchKey]struct{}
}

// level returns slog.LevelWarn the first time it sees key and
// slog.LevelDebug after that. It forgets every key once it holds
// maxMismatchKeys, so memory stays bounded and a key may warn again.
func (m *mismatchLog) level(key mismatchKey) slog.Level {
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, ok := m.seen[key]; ok {
		return slog.LevelDebug
	}
	if m.seen == nil || len(m.seen) >= maxMismatchKeys {
		m.seen = make(map[mismatchKey]struct{})
	}
	m.seen[key] = struct{}{}
	return slog.LevelWarn
}
