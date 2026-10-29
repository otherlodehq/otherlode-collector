package forward

import (
	"context"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	otherlodepb "buf.build/gen/go/otherlode/otherlode/protocolbuffers/go/otherlode/v1"

	"github.com/otherlodehq/otherlode-collector/ingest"
	"github.com/otherlodehq/otherlode-collector/metrics"
)

type testClock struct {
	mu  sync.Mutex
	now time.Time
}

func (c *testClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

func (c *testClock) Advance(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.now = c.now.Add(d)
}

// noticeHarness is a sink that posts to a backend answering 202 with the
// notice header values and status it holds.
type noticeHarness struct {
	t      *testing.T
	sink   *ForwardingSink
	logs   *syncBuffer
	clock  *testClock
	mu     sync.Mutex
	values []string
	status int
}

func newNoticeHarness(t *testing.T, maxKeys int) *noticeHarness {
	t.Helper()
	h := &noticeHarness{
		t:      t,
		logs:   &syncBuffer{},
		clock:  &testClock{now: time.Date(2026, 10, 29, 9, 0, 0, 0, time.UTC)},
		status: http.StatusAccepted,
	}
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h.mu.Lock()
		values, status := h.values, h.status
		h.mu.Unlock()
		for _, v := range values {
			w.Header().Add(ingest.NoticeHeader, v)
		}
		w.WriteHeader(status)
	}))
	t.Cleanup(backend.Close)

	cfg := testConfig(backend.URL)
	cfg.Logger = slog.New(slog.NewJSONHandler(h.logs, nil))
	h.sink = mustNewSink(t, cfg)
	h.sink.noticeLog = newNoticeLog(h.clock.Now, maxKeys)
	t.Cleanup(func() { h.sink.Shutdown(context.Background()) })
	return h
}

func (h *noticeHarness) respondWith(status int, values ...string) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.status, h.values = status, values
}

// send posts a manifest for the service and waits until the worker is
// done with it.
func (h *noticeHarness) send(namespace, service string) {
	h.t.Helper()
	res := &otherlodepb.ResourceAttributes{ServiceName: service, ServiceInstanceId: "instance-1", RunId: "run-1"}
	res.SetServiceNamespace(namespace)
	before := metrics.ForwardDelivered.Value("manifest") + droppedTotal("manifest")
	if _, err := h.sink.AcceptManifest(context.Background(), &otherlodepb.ProbeManifest{Resource: res}); err != nil {
		h.t.Fatalf("AcceptManifest: %v", err)
	}
	waitFor(h.t, 2*time.Second, func() bool {
		return metrics.ForwardDelivered.Value("manifest")+droppedTotal("manifest") > before
	})
}

func (h *noticeHarness) noticeLines() []string {
	var lines []string
	for _, line := range strings.Split(h.logs.String(), "\n") {
		if strings.Contains(line, `"notice"`) {
			lines = append(lines, line)
		}
	}
	return lines
}

func noticeCount(notice string) int64 {
	return metrics.ForwardNotices.Value(notice)
}

const (
	serviceOverCapMsg = "backend stores the service's payloads but does not track the service, because the tenant's plan tracks no more services"
	envNotTrackedMsg  = "backend stores the environment's payloads but does not track the environment, because the service already watches as many environments as its cap allows"
)

func TestForwardingSink_Notice_LogsWarnNamingServiceAndCounts(t *testing.T) {
	h := newNoticeHarness(t, 100)
	h.respondWith(http.StatusAccepted, "service-over-cap")
	before := noticeCount("service-over-cap")

	h.send("team-a", "billing")

	lines := h.noticeLines()
	if len(lines) != 1 {
		t.Fatalf("got %d notice log lines, want 1:\n%s", len(lines), h.logs.String())
	}
	for _, want := range []string{
		`"level":"WARN"`,
		`"msg":"` + serviceOverCapMsg + `"`,
		`"notice":"service-over-cap"`,
		`"namespace":"team-a"`,
		`"service":"billing"`,
	} {
		if !strings.Contains(lines[0], want) {
			t.Errorf("log line does not contain %s:\n%s", want, lines[0])
		}
	}
	if got := noticeCount("service-over-cap") - before; got != 1 {
		t.Errorf("service-over-cap count rose by %d, want 1", got)
	}
}

func TestForwardingSink_Notice_EnvironmentNotTracked_LogsItsOwnSentence(t *testing.T) {
	h := newNoticeHarness(t, 100)
	h.respondWith(http.StatusAccepted, "environment-not-tracked")
	before := noticeCount("environment-not-tracked")

	h.send("", "billing")

	lines := h.noticeLines()
	if len(lines) != 1 {
		t.Fatalf("got %d notice log lines, want 1:\n%s", len(lines), h.logs.String())
	}
	for _, want := range []string{`"msg":"` + envNotTrackedMsg + `"`, `"notice":"environment-not-tracked"`, `"namespace":""`, `"service":"billing"`} {
		if !strings.Contains(lines[0], want) {
			t.Errorf("log line does not contain %s:\n%s", want, lines[0])
		}
	}
	if got := noticeCount("environment-not-tracked") - before; got != 1 {
		t.Errorf("environment-not-tracked count rose by %d, want 1", got)
	}
}

func TestForwardingSink_Notice_LogsOncePerHourPerServiceButCountsEvery(t *testing.T) {
	h := newNoticeHarness(t, 100)
	h.respondWith(http.StatusAccepted, "service-over-cap")
	before := noticeCount("service-over-cap")

	h.send("team-a", "billing")
	h.send("team-a", "billing")
	h.clock.Advance(59 * time.Minute)
	h.send("team-a", "billing")
	if got := len(h.noticeLines()); got != 1 {
		t.Fatalf("got %d log lines inside the hour, want 1:\n%s", got, h.logs.String())
	}
	if got := noticeCount("service-over-cap") - before; got != 3 {
		t.Errorf("count rose by %d, want 3", got)
	}

	h.clock.Advance(time.Minute)
	h.send("team-a", "billing")
	if got := len(h.noticeLines()); got != 2 {
		t.Fatalf("got %d log lines after the hour, want 2:\n%s", got, h.logs.String())
	}

	h.clock.Advance(59 * time.Minute)
	h.send("team-a", "billing")
	if got := len(h.noticeLines()); got != 2 {
		t.Fatalf("got %d log lines inside the second hour, want 2:\n%s", got, h.logs.String())
	}
}

func TestForwardingSink_Notice_CommaJoinedHeaderCountsEachNotice(t *testing.T) {
	h := newNoticeHarness(t, 100)
	h.respondWith(http.StatusAccepted, "service-over-cap, environment-not-tracked,")
	overCap, notTracked, unknown := noticeCount("service-over-cap"), noticeCount("environment-not-tracked"), noticeCount("unknown")

	h.send("", "billing")

	if got := noticeCount("service-over-cap") - overCap; got != 1 {
		t.Errorf("service-over-cap rose by %d, want 1", got)
	}
	if got := noticeCount("environment-not-tracked") - notTracked; got != 1 {
		t.Errorf("environment-not-tracked rose by %d, want 1", got)
	}
	if got := noticeCount("unknown") - unknown; got != 0 {
		t.Errorf("unknown rose by %d, want 0", got)
	}
	if got := len(h.noticeLines()); got != 2 {
		t.Fatalf("got %d log lines, want 2:\n%s", got, h.logs.String())
	}
}

func TestForwardingSink_Notice_EachServiceAndNamespaceLogsItsOwnLine(t *testing.T) {
	h := newNoticeHarness(t, 100)
	h.respondWith(http.StatusAccepted, "service-over-cap")

	h.send("team-a", "billing")
	h.send("team-a", "search")
	h.send("team-b", "billing")
	h.send("team-a", "billing")

	if got := len(h.noticeLines()); got != 3 {
		t.Fatalf("got %d log lines, want 3:\n%s", got, h.logs.String())
	}
}

func TestForwardingSink_Notice_EachNoticeValueLogsItsOwnLine(t *testing.T) {
	h := newNoticeHarness(t, 100)
	h.respondWith(http.StatusAccepted, "service-over-cap", "environment-not-tracked")

	h.send("", "billing")

	if got := len(h.noticeLines()); got != 2 {
		t.Fatalf("got %d log lines, want 2:\n%s", got, h.logs.String())
	}
}

func TestForwardingSink_Notice_GarbageValue_CountsUnknownAndLogsNoValue(t *testing.T) {
	h := newNoticeHarness(t, 100)
	h.respondWith(http.StatusAccepted, "Bad Value!")
	before := noticeCount("unknown")

	h.send("", "billing")

	if got := noticeCount("unknown") - before; got != 1 {
		t.Errorf("unknown count rose by %d, want 1", got)
	}
	if got := metrics.ForwardNotices.Value("Bad Value!"); got != 0 {
		t.Errorf("a series exists for the raw value, count %d", got)
	}
	lines := h.noticeLines()
	if len(lines) != 1 {
		t.Fatalf("got %d notice log lines, want 1:\n%s", len(lines), h.logs.String())
	}
	if strings.Contains(lines[0], "Bad Value") {
		t.Errorf("log line shows the raw value:\n%s", lines[0])
	}
	for _, want := range []string{`"level":"WARN"`, `"notice":"unknown"`, `"service":"billing"`} {
		if !strings.Contains(lines[0], want) {
			t.Errorf("log line does not contain %s:\n%s", want, lines[0])
		}
	}
}

func TestForwardingSink_Notice_WellFormedUnknownValue_CountsUnknownAndLogsValue(t *testing.T) {
	h := newNoticeHarness(t, 100)
	h.respondWith(http.StatusAccepted, "quota-soon")
	before := noticeCount("unknown")

	h.send("", "billing")

	if got := noticeCount("unknown") - before; got != 1 {
		t.Errorf("unknown count rose by %d, want 1", got)
	}
	if got := metrics.ForwardNotices.Value("quota-soon"); got != 0 {
		t.Errorf("a series exists for the raw value, count %d", got)
	}
	lines := h.noticeLines()
	if len(lines) != 1 || !strings.Contains(lines[0], `"notice":"quota-soon"`) {
		t.Fatalf("want one line showing quota-soon:\n%s", h.logs.String())
	}
}

func TestForwardingSink_Notice_ValueLengthLimit(t *testing.T) {
	long := strings.Repeat("a", 65)
	for name, tc := range map[string]struct {
		value     string
		wantShown bool
	}{
		"64 characters": {strings.Repeat("a", 64), true},
		"65 characters": {long, false},
		"upper case":    {"Quota-Soon", false},
		"digit":         {"quota-2", false},
		"underscore":    {"quota_soon", false},
	} {
		t.Run(name, func(t *testing.T) {
			h := newNoticeHarness(t, 100)
			h.respondWith(http.StatusAccepted, tc.value)
			h.send("", "billing")
			lines := h.noticeLines()
			if len(lines) != 1 {
				t.Fatalf("got %d notice log lines, want 1:\n%s", len(lines), h.logs.String())
			}
			if shown := strings.Contains(lines[0], `"notice":"`+tc.value+`"`); shown != tc.wantShown {
				t.Errorf("value shown = %v, want %v:\n%s", shown, tc.wantShown, lines[0])
			}
		})
	}
}

func TestForwardingSink_Notice_EmptyValue_IsNoNotice(t *testing.T) {
	h := newNoticeHarness(t, 100)
	h.respondWith(http.StatusAccepted, "", " , ")
	before := noticeCount("unknown")

	h.send("", "billing")

	if got := noticeCount("unknown") - before; got != 0 {
		t.Errorf("unknown count rose by %d, want 0", got)
	}
	if got := len(h.noticeLines()); got != 0 {
		t.Fatalf("got %d notice log lines, want 0:\n%s", got, h.logs.String())
	}
}

func TestForwardingSink_Notice_AbsentHeader_LogsAndCountsNothing(t *testing.T) {
	h := newNoticeHarness(t, 100)
	h.respondWith(http.StatusAccepted)
	known := []string{"service-over-cap", "environment-not-tracked", "unknown"}
	before := make([]int64, len(known))
	for i, n := range known {
		before[i] = noticeCount(n)
	}

	h.send("", "billing")

	if lines := h.noticeLines(); len(lines) != 0 {
		t.Errorf("got notice log lines:\n%s", h.logs.String())
	}
	for i, n := range known {
		if got := noticeCount(n); got != before[i] {
			t.Errorf("%s count changed by %d, want 0", n, got-before[i])
		}
	}
}

func TestForwardingSink_Notice_OnNon2xx_IsNotANotice(t *testing.T) {
	h := newNoticeHarness(t, 100)
	h.respondWith(http.StatusForbidden, "service-over-cap")
	before := noticeCount("service-over-cap")

	h.send("", "billing")

	if lines := h.noticeLines(); len(lines) != 0 {
		t.Errorf("got notice log lines:\n%s", h.logs.String())
	}
	if got := noticeCount("service-over-cap") - before; got != 0 {
		t.Errorf("count rose by %d, want 0", got)
	}
}

func TestForwardingSink_Notice_Plain200_IsANotice(t *testing.T) {
	h := newNoticeHarness(t, 100)
	h.respondWith(http.StatusOK, "service-over-cap")

	h.send("", "billing")

	if got := len(h.noticeLines()); got != 1 {
		t.Errorf("got %d notice log lines, want 1:\n%s", got, h.logs.String())
	}
}

func TestForwardingSink_Notice_ManyServices_StillLogsAndMemoryStaysBounded(t *testing.T) {
	const maxKeys = 3
	h := newNoticeHarness(t, maxKeys)
	h.respondWith(http.StatusAccepted, "service-over-cap")

	services := []string{"s1", "s2", "s3", "s4", "s5", "s6"}
	for _, s := range services {
		h.send("", s)
	}

	if got := len(h.noticeLines()); got != len(services) {
		t.Fatalf("got %d log lines for %d distinct services, want one each:\n%s", got, len(services), h.logs.String())
	}
	if got := h.sink.noticeLog.size(); got > maxKeys {
		t.Fatalf("remembered %d keys, cap is %d", got, maxKeys)
	}

	h.send("", "s6")
	if got := len(h.noticeLines()); got != len(services)+1 {
		t.Fatalf("a service the log could not remember must log again, got %d lines", got)
	}
}

func TestForwardingSink_Notice_FullSet_DropsExpiredKeysBeforeRefusingANewOne(t *testing.T) {
	h := newNoticeHarness(t, 2)
	h.respondWith(http.StatusAccepted, "service-over-cap")

	h.send("", "s1")
	h.send("", "s2")
	h.clock.Advance(2 * time.Hour)
	h.send("", "s3")
	lines := len(h.noticeLines())
	h.send("", "s3")

	if got := len(h.noticeLines()); got != lines {
		t.Fatalf("s3 logged again inside the hour: %d lines, want %d", got, lines)
	}
	if got := h.sink.noticeLog.size(); got != 1 {
		t.Fatalf("remembered %d keys after expiry, want 1", got)
	}
}

func TestForwardingSink_Notice_HugeRawValues_DoNotGrowTheRememberedSet(t *testing.T) {
	h := newNoticeHarness(t, 100)
	for i := 0; i < 5; i++ {
		h.respondWith(http.StatusAccepted, strings.Repeat("Z", 200+i))
		h.send("", "billing")
	}
	if got := h.sink.noticeLog.size(); got != 1 {
		t.Fatalf("remembered %d keys for one service and one unknown notice, want 1", got)
	}
}

func TestForwardingSink_Accept_ReturnsNoNotices(t *testing.T) {
	h := newNoticeHarness(t, 100)
	h.respondWith(http.StatusAccepted, "service-over-cap")
	res := &otherlodepb.ResourceAttributes{ServiceName: "billing", ServiceInstanceId: "instance-1", RunId: "run-1"}

	notices, err := h.sink.AcceptDeltaBatch(context.Background(), &otherlodepb.DeltaBatch{Resource: res})
	if err != nil {
		t.Fatalf("AcceptDeltaBatch: %v", err)
	}
	if notices != nil {
		t.Errorf("notices = %q, want none", notices)
	}
}
