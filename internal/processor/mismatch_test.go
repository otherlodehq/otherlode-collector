package processor

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"strconv"
	"strings"
	"testing"

	otherlodepb "buf.build/gen/go/otherlode/otherlode/protocolbuffers/go/otherlode/v1"
)

// logLevels decodes buf as JSON log lines and returns the level of each
// line whose message holds msg.
func logLevels(t *testing.T, buf *bytes.Buffer, msg string) []string {
	t.Helper()
	var levels []string
	for _, line := range strings.Split(strings.TrimSpace(buf.String()), "\n") {
		if line == "" {
			continue
		}
		var entry struct {
			Level string `json:"level"`
			Msg   string `json:"msg"`
		}
		if err := json.Unmarshal([]byte(line), &entry); err != nil {
			t.Fatalf("log line %q: %v", line, err)
		}
		if strings.Contains(entry.Msg, msg) {
			levels = append(levels, entry.Level)
		}
	}
	return levels
}

func debugLogger(buf *bytes.Buffer) *slog.Logger {
	return slog.New(slog.NewJSONHandler(buf, &slog.HandlerOptions{Level: slog.LevelDebug}))
}

func mismatchResource(service, environment, namespace string) *otherlodepb.ResourceAttributes {
	res := &otherlodepb.ResourceAttributes{ServiceName: service, ServiceInstanceId: "i1", RunId: "run-1"}
	res.SetEnvironment(environment)
	res.SetServiceNamespace(namespace)
	return res
}

// TestEnvironment_Mismatch_WarnsOncePerServiceAndValue pins that a
// mismatch reaches an operator at the default log level without a line
// at that level for every payload.
func TestEnvironment_Mismatch_WarnsOncePerServiceAndValue(t *testing.T) {
	var logs bytes.Buffer
	env := NewEnvironment(&recordingSink{}, EnvironmentConfig{Value: "prod"}, debugLogger(&logs))

	for _, res := range []*otherlodepb.ResourceAttributes{
		mismatchResource("billing", "uat", ""),
		mismatchResource("billing", " UAT", ""), // the same environment to the server
		mismatchResource("billing", "dev", ""),
		mismatchResource("search", "uat", ""),
	} {
		if err := env.AcceptDeltaBatch(context.Background(), &otherlodepb.DeltaBatch{Resource: res}); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
	}

	got := logLevels(t, &logs, "agent environment does not match")
	want := []string{"WARN", "DEBUG", "WARN", "WARN"}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("mismatch log levels = %v, want %v", got, want)
	}
}

func TestNamespace_Mismatch_WarnsOncePerServiceAndValue(t *testing.T) {
	var logs bytes.Buffer
	ns := NewNamespace(&recordingSink{}, NamespaceConfig{Value: "team-a"}, debugLogger(&logs))

	for _, res := range []*otherlodepb.ResourceAttributes{
		mismatchResource("billing", "", "team-b"),
		mismatchResource("billing", "", " team-b "),
		mismatchResource("billing", "", "Team-B"), // namespaces keep case
		mismatchResource("search", "", "team-b"),
	} {
		if err := ns.AcceptDeltaBatch(context.Background(), &otherlodepb.DeltaBatch{Resource: res}); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
	}

	got := logLevels(t, &logs, "agent namespace does not match")
	want := []string{"WARN", "DEBUG", "WARN", "WARN"}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("mismatch log levels = %v, want %v", got, want)
	}
}

func TestMismatchLog_ForgetsEveryKeyAtTheBound(t *testing.T) {
	var m mismatchLog
	first := mismatchKey{service: "svc", value: "v0"}
	m.level(first)
	for i := 1; i < maxMismatchKeys; i++ {
		m.level(mismatchKey{service: "svc", value: strconv.Itoa(i)})
	}
	if got := m.level(first); got != slog.LevelDebug {
		t.Fatalf("level for a key seen before the bound = %v, want DEBUG", got)
	}
	m.level(mismatchKey{service: "svc", value: "one past the bound"})
	if got := len(m.seen); got != 1 {
		t.Fatalf("keys held after passing the bound = %d, want 1", got)
	}
	if got := m.level(first); got != slog.LevelWarn {
		t.Fatalf("level for a forgotten key = %v, want WARN", got)
	}
}
