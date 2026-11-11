package processor

import (
	"log/slog"
	"sync"
)

const maxMismatchKeys = 10000

type mismatchKey struct {
	namespace string
	service   string
	value     string
}

type mismatchLog struct {
	mu   sync.Mutex
	seen map[mismatchKey]struct{}
}

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
