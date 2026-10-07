package ingest_test

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"testing"

	otherlodepb "buf.build/gen/go/otherlode/otherlode/protocolbuffers/go/otherlode/v1"

	"github.com/otherlodehq/otherlode-collector/ingest"
)

// TestLogSink_LogsTheTestRunFlagAndAgentVersion covers the test_run
// attribute (agent ADR 0050) and the agent_version attribute (agent ADR 0054)
// on each of the three payloads' log lines.
func TestLogSink_LogsTheTestRunFlagAndAgentVersion(t *testing.T) {
	var buf bytes.Buffer
	sink := ingest.NewLogSink(slog.New(slog.NewJSONHandler(&buf, nil)))
	resource := &otherlodepb.ResourceAttributes{ServiceName: "svc", ServiceInstanceId: "unit-tests", RunId: "run-1", TestRun: true, AgentVersion: "1.2.3"}
	ctx := context.Background()

	if err := sink.AcceptDeltaBatch(ctx, &otherlodepb.DeltaBatch{Resource: resource}); err != nil {
		t.Fatalf("accept delta batch: %v", err)
	}
	if err := sink.AcceptManifest(ctx, &otherlodepb.ProbeManifest{Resource: resource}); err != nil {
		t.Fatalf("accept manifest: %v", err)
	}
	if err := sink.AcceptStaticBaseline(ctx, &otherlodepb.StaticBaseline{Resource: resource}); err != nil {
		t.Fatalf("accept static baseline: %v", err)
	}

	lines := bytes.Split(bytes.TrimSpace(buf.Bytes()), []byte("\n"))
	if len(lines) != 3 {
		t.Fatalf("logged %d lines, want 3: %s", len(lines), buf.String())
	}
	for _, line := range lines {
		var record map[string]any
		if err := json.Unmarshal(line, &record); err != nil {
			t.Fatalf("decode log line %s: %v", line, err)
		}
		if record["test_run"] != true {
			t.Errorf("log line %s has test_run %v, want true", line, record["test_run"])
		}
		if record["agent_version"] != "1.2.3" {
			t.Errorf("log line %s has agent_version %v, want 1.2.3", line, record["agent_version"])
		}
	}
}

// TestLogSink_LogsTheFailedClassCount covers the failed_classes attribute
// on a manifest's log line (agent ADR 0065).
func TestLogSink_LogsTheFailedClassCount(t *testing.T) {
	var buf bytes.Buffer
	sink := ingest.NewLogSink(slog.New(slog.NewJSONHandler(&buf, nil)))
	manifest := &otherlodepb.ProbeManifest{
		Resource:      &otherlodepb.ResourceAttributes{ServiceName: "svc", ServiceInstanceId: "i1", RunId: "run-1"},
		FailedClasses: []*otherlodepb.FailedClass{{ClassName: "com.example.A"}, {ClassName: "com.example.B"}},
	}

	if err := sink.AcceptManifest(context.Background(), manifest); err != nil {
		t.Fatalf("accept manifest: %v", err)
	}

	var record map[string]any
	if err := json.Unmarshal(bytes.TrimSpace(buf.Bytes()), &record); err != nil {
		t.Fatalf("decode log line %s: %v", buf.String(), err)
	}
	if record["failed_classes"] != float64(2) {
		t.Errorf("log line %s has failed_classes %v, want 2", buf.String(), record["failed_classes"])
	}
}

// TestLogSink_LogsCountsPendingSinceOnlyWhenSet covers the
// counts_pending_since attribute on a delta batch's and a manifest's log
// line (agent ADR 0068). A zero value leaves the attribute out.
func TestLogSink_LogsCountsPendingSinceOnlyWhenSet(t *testing.T) {
	resource := &otherlodepb.ResourceAttributes{ServiceName: "svc", ServiceInstanceId: "i1", RunId: "run-1"}
	ctx := context.Background()
	const since = int64(1700000000000)

	record := func(t *testing.T, accept func(*ingest.LogSink) error) map[string]any {
		t.Helper()
		var buf bytes.Buffer
		sink := ingest.NewLogSink(slog.New(slog.NewJSONHandler(&buf, nil)))
		if err := accept(sink); err != nil {
			t.Fatalf("accept: %v", err)
		}
		var rec map[string]any
		if err := json.Unmarshal(bytes.TrimSpace(buf.Bytes()), &rec); err != nil {
			t.Fatalf("decode log line %s: %v", buf.String(), err)
		}
		return rec
	}

	cases := []struct {
		name   string
		accept func(pending int64) func(*ingest.LogSink) error
	}{
		{"delta batch", func(pending int64) func(*ingest.LogSink) error {
			return func(s *ingest.LogSink) error {
				return s.AcceptDeltaBatch(ctx, &otherlodepb.DeltaBatch{Resource: resource, CountsPendingSince: pending})
			}
		}},
		{"manifest", func(pending int64) func(*ingest.LogSink) error {
			return func(s *ingest.LogSink) error {
				return s.AcceptManifest(ctx, &otherlodepb.ProbeManifest{Resource: resource, CountsPendingSince: pending})
			}
		}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := record(t, c.accept(since))["counts_pending_since"]; got != float64(since) {
				t.Errorf("counts_pending_since = %v, want %d", got, since)
			}
			if got, ok := record(t, c.accept(0))["counts_pending_since"]; ok {
				t.Errorf("counts_pending_since = %v on a payload with no pending counts, want it absent", got)
			}
		})
	}
}
