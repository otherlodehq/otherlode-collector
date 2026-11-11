package forward

import (
	"strings"
	"sync"
	"time"

	"github.com/otherlodehq/otherlode-collector/ingest"
	"github.com/otherlodehq/otherlode-collector/metrics"
)

const (
	noticeLogInterval = time.Hour

	maxNoticeKeys = 10000

	maxNoticeValueBytes = 64
)

type noticeKey struct {
	notice    string
	namespace string
	service   string
}

type noticeLog struct {
	now     func() time.Time
	maxKeys int

	mu   sync.Mutex
	last map[noticeKey]time.Time
}

func newNoticeLog(now func() time.Time, maxKeys int) *noticeLog {
	return &noticeLog{now: now, maxKeys: maxKeys, last: make(map[noticeKey]time.Time)}
}

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

func (l *noticeLog) size() int {
	l.mu.Lock()
	defer l.mu.Unlock()
	return len(l.last)
}

var noticeMessages = map[ingest.Notice]string{
	ingest.NoticeServiceOverCap:        "backend stores the service's payloads but does not track the service, because the tenant's plan tracks no more services",
	ingest.NoticeEnvironmentNotTracked: "backend stores the environment's payloads but does not track the environment, because the service already watches as many environments as its cap allows",
}

const unknownNoticeMessage = "backend sent a notice this collector does not know"

const unknownNotice = "unknown"

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

func (s *ForwardingSink) noticed(item outbound, lines []string) {
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
