// Package processor holds sinks that wrap another ingest.Sink. Each one
// changes or inspects a decoded payload, then passes it on.
package processor

import (
	"context"
	"fmt"
	"log/slog"
	"strings"

	otherlodepb "buf.build/gen/go/otherlode/otherlode/protocolbuffers/go/otherlode/v1"

	"github.com/otherlodehq/otherlode-collector/ingest"
	"github.com/otherlodehq/otherlode-collector/metrics"
)

// Action says what Environment or Namespace does when a payload already
// holds a value for its field.
type Action int

const (
	// Insert keeps an agent-set value and fills in an absent one. It is the
	// default.
	Insert Action = iota
	// Upsert also writes the collector's configured value over a different
	// value the agent set.
	Upsert
)

// ParseAction parses raw as an Action, ignoring case. "" and "insert" mean
// Insert. "upsert" means Upsert. Any other value is an error.
func ParseAction(raw string) (Action, error) {
	switch strings.ToLower(raw) {
	case "", "insert":
		return Insert, nil
	case "upsert":
		return Upsert, nil
	default:
		return 0, fmt.Errorf("invalid action %q: must be %q or %q", raw, Insert, Upsert)
	}
}

// String returns "insert" or "upsert".
func (a Action) String() string {
	if a == Upsert {
		return "upsert"
	}
	return "insert"
}

// EnvironmentConfig configures an Environment processor. Value is the
// environment name to stamp. An empty Value means the processor is off.
type EnvironmentConfig struct {
	Value  string
	Action Action
}

// Environment is an ingest.Sink that writes a configured environment name
// onto the resource of each delta batch, manifest, static baseline and
// class listing, then passes the payload to next. It changes the decoded
// message in place.
type Environment struct {
	next       ingest.Sink
	cfg        EnvironmentConfig
	logger     *slog.Logger
	mismatches mismatchLog
}

var _ ingest.Sink = (*Environment)(nil)

// NewEnvironment returns an Environment that applies cfg and passes
// payloads to next. A nil logger means slog.Default().
func NewEnvironment(next ingest.Sink, cfg EnvironmentConfig, logger *slog.Logger) *Environment {
	if logger == nil {
		logger = slog.Default()
	}
	return &Environment{next: next, cfg: cfg, logger: logger}
}

// AcceptDeltaBatch stamps the batch's resource, then passes it to next.
func (e *Environment) AcceptDeltaBatch(ctx context.Context, batch *otherlodepb.DeltaBatch) ([]ingest.Notice, error) {
	e.stamp(batch.GetResource(), "deltas")
	return e.next.AcceptDeltaBatch(ctx, batch)
}

// AcceptStaticBaseline stamps the baseline's resource, then passes it to next.
func (e *Environment) AcceptStaticBaseline(ctx context.Context, baseline *otherlodepb.StaticBaseline) ([]ingest.Notice, error) {
	e.stamp(baseline.GetResource(), "static_baseline")
	return e.next.AcceptStaticBaseline(ctx, baseline)
}

// AcceptClassListing stamps the listing's resource, then passes it to next.
func (e *Environment) AcceptClassListing(ctx context.Context, listing *otherlodepb.ClassListing) ([]ingest.Notice, error) {
	e.stamp(listing.GetResource(), "class_listing")
	return e.next.AcceptClassListing(ctx, listing)
}

// AcceptManifest stamps the manifest's resource, then passes it to next.
func (e *Environment) AcceptManifest(ctx context.Context, manifest *otherlodepb.ProbeManifest) ([]ingest.Notice, error) {
	e.stamp(manifest.GetResource(), "manifest")
	return e.next.AcceptManifest(ctx, manifest)
}

func (e *Environment) stamp(res *otherlodepb.ResourceAttributes, payload string) {
	if res == nil {
		return
	}
	agentEnv := res.GetEnvironment()
	if strings.TrimSpace(agentEnv) == "" {
		res.SetEnvironment(e.cfg.Value)
		return
	}
	if sameEnvironment(agentEnv, e.cfg.Value) {
		if e.cfg.Action == Upsert {
			res.SetEnvironment(e.cfg.Value)
		}
		return
	}

	metrics.EnvironmentMismatch.Inc(payload)
	key := mismatchKey{
		namespace: res.GetServiceNamespace(),
		service:   res.GetServiceName(),
		value:     normaliseEnvironment(agentEnv),
	}
	e.logger.Log(context.Background(), e.mismatches.level(key), "agent environment does not match the collector's configured environment",
		"namespace", res.GetServiceNamespace(),
		"service", res.GetServiceName(),
		"instance", res.GetServiceInstanceId(),
		"run", res.GetRunId(),
		"payload", payload,
		"agent_environment", agentEnv,
		"collector_environment", e.cfg.Value,
		"action", e.cfg.Action.String(),
	)
	if e.cfg.Action == Upsert {
		res.SetEnvironment(e.cfg.Value)
	}
}

func sameEnvironment(a, b string) bool {
	return normaliseEnvironment(a) == normaliseEnvironment(b)
}

// This must match how the server stores an environment, trimmed and lowercased.
func normaliseEnvironment(name string) string {
	return strings.ToLower(strings.TrimSpace(name))
}
