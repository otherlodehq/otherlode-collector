package forward

import (
	"strings"
	"sync"
	"time"

	"github.com/otherlodehq/otherlode-collector/ingest"
	"github.com/otherlodehq/otherlode-collector/metrics"
)

const (
	// noticeLogInterval is the least time between two log lines for one
	// notice of one service.
	noticeLogInterval = time.Hour

	// maxNoticeKeys bounds how many (notice, service) pairs a noticeLog
	// remembers.
	maxNoticeKeys = 10000

	// maxNoticeValueBytes is the longest backend notice value a log line
	// shows.
	maxNoticeValueBytes = 64
)

// noticeKey names one notice of one service. notice is the value the log
// line shows: a known notice, the backend's value when it is well formed,
// or "unknown".
type noticeKey struct {
	notice    string
	namespace string
	service   string
}

// noticeLog decides when a notice gets a log line. It remembers when each
// key last logged and allows one line per key in each noticeLogInterval.
type noticeLog struct {
	now     func() time.Time
	maxKeys int

	mu   sync.Mutex
	last map[noticeKey]time.Time
}

func newNoticeLog(now func() time.Time, maxKeys int) *noticeLog {
	return &noticeLog{now: now, maxKeys: maxKeys, last: make(map[noticeKey]time.Time)}
}

// allow reports whether key may log now, and starts its interval when it
// may. When the set is full, allow first drops the keys whose interval
// has passed. If it is still full, allow does not remember key and
// reports true, so a flood of distinct services logs more, never less.
func (l *noticeLog) allow(key noticeKey) bool {
	now := l.now()
	l.mu.Lock()
	defer l.mu.Unlock()
	last, seen := l.last[key]
	if seen && now.Sub(last) < noticeLogInterval {
		return false
	}
	if !seen && len(l.last) >= l.maxKeys {
		for k, t := range l.last {
			if now.Sub(t) >= noticeLogInterval {
				delete(l.last, k)
			}
		}
	}
	if seen || len(l.last) < l.maxKeys {
		l.last[key] = now
	}
	return true
}

// size returns how many keys the log remembers.
func (l *noticeLog) size() int {
	l.mu.Lock()
	defer l.mu.Unlock()
	return len(l.last)
}

// noticeMessages maps a known notice to the sentence logged for it.
var noticeMessages = map[ingest.Notice]string{
	ingest.NoticeServiceOverCap:        "backend stores the service's payloads but does not track the service, because the tenant's plan tracks no more services",
	ingest.NoticeEnvironmentNotTracked: "backend stores the environment's payloads but does not track the environment, because the service already watches as many environments as its cap allows",
}

const unknownNoticeMessage = "backend sent a notice this collector does not know"

// unknownNotice is the label and log value for a notice outside the
// closed set.
const unknownNotice = "unknown"

// noticeValues splits the NoticeHeader lines of a response into values.
// A proxy may join the lines of a list header into one line separated by
// commas, so each line is split on commas and each part trimmed. An empty
// part is dropped.
func noticeValues(lines []string) []string {
	var values []string
	for _, line := range lines {
		for part := range strings.SplitSeq(line, ",") {
			if part = strings.TrimSpace(part); part != "" {
				values = append(values, part)
			}
		}
	}
	return values
}

// wellFormedNotice reports whether value is 1 to maxNoticeValueBytes
// bytes of lowercase letters and hyphens.
func wellFormedNotice(value string) bool {
	if value == "" || len(value) > maxNoticeValueBytes {
		return false
	}
	for i := 0; i < len(value); i++ {
		if c := value[i]; (c < 'a' || c > 'z') && c != '-' {
			return false
		}
	}
	return true
}

// noticed counts each notice value the backend sent with a 2xx response
// for item, and logs a Warn for it unless the same notice of the same
// service logged within the last hour. A value outside the closed set
// counts as "unknown", and the log shows it only when it is well formed.
func (s *ForwardingSink) noticed(item queuedItem, lines []string) {
	for _, value := range noticeValues(lines) {
		notice := ingest.Notice(value)
		known := notice.Known()
		if known {
			metrics.ForwardNotices.Inc(value)
		} else {
			metrics.ForwardNotices.Inc(unknownNotice)
		}

		shown := unknownNotice
		if known || wellFormedNotice(value) {
			shown = value
		}
		key := noticeKey{notice: shown, namespace: item.owner.namespace, service: item.owner.service}
		if !s.noticeLog.allow(key) {
			continue
		}
		msg := unknownNoticeMessage
		if known {
			msg = noticeMessages[notice]
		}
		s.cfg.Logger.Warn(msg,
			"notice", shown,
			"namespace", item.owner.namespace,
			"service", item.owner.service,
		)
	}
}
