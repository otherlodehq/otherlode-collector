package processor

import (
	"context"
	"log/slog"
	"strings"

	otherlodepb "buf.build/gen/go/otherlode/otherlode/protocolbuffers/go/otherlode/v1"

	"github.com/otherlodehq/otherlode-collector/ingest"
	"github.com/otherlodehq/otherlode-collector/metrics"
)

// NamespaceConfig configures a Namespace processor. Value is the service
// namespace to stamp. An empty Value means the processor is off.
type NamespaceConfig struct {
	Value  string
	Action Action
}

// Namespace is an ingest.Sink that writes a configured service namespace
// onto the resource of each delta batch, manifest, static baseline and
// class listing, then passes the payload to next. It compares values after
// it trims the spaces around them, and keeps case. It changes the decoded
// message in place.
type Namespace struct {
	next       ingest.Sink
	cfg        NamespaceConfig
	logger     *slog.Logger
	mismatches mismatchLog
}

var _ ingest.Sink = (*Namespace)(nil)

// NewNamespace returns a Namespace that applies cfg and passes payloads to
// next. A nil logger means slog.Default().
func NewNamespace(next ingest.Sink, cfg NamespaceConfig, logger *slog.Logger) *Namespace {
	if logger == nil {
		logger = slog.Default()
	}
	return &Namespace{next: next, cfg: cfg, logger: logger}
}

// AcceptDeltaBatch stamps the batch's resource, then passes it to next.
func (n *Namespace) AcceptDeltaBatch(ctx context.Context, batch *otherlodepb.DeltaBatch) ([]ingest.Notice, error) {
	n.stamp(batch.GetResource(), "deltas")
	return n.next.AcceptDeltaBatch(ctx, batch)
}

// AcceptManifest stamps the manifest's resource, then passes it to next.
func (n *Namespace) AcceptManifest(ctx context.Context, manifest *otherlodepb.ProbeManifest) ([]ingest.Notice, error) {
	n.stamp(manifest.GetResource(), "manifest")
	return n.next.AcceptManifest(ctx, manifest)
}

// AcceptStaticBaseline stamps the baseline's resource, then passes it to next.
func (n *Namespace) AcceptStaticBaseline(ctx context.Context, baseline *otherlodepb.StaticBaseline) ([]ingest.Notice, error) {
	n.stamp(baseline.GetResource(), "static_baseline")
	return n.next.AcceptStaticBaseline(ctx, baseline)
}

// AcceptClassListing stamps the listing's resource, then passes it to next.
func (n *Namespace) AcceptClassListing(ctx context.Context, listing *otherlodepb.ClassListing) ([]ingest.Notice, error) {
	n.stamp(listing.GetResource(), "class_listing")
	return n.next.AcceptClassListing(ctx, listing)
}

func (n *Namespace) stamp(res *otherlodepb.ResourceAttributes, payload string) {
	if res == nil {
		return
	}
	agentNamespace := res.GetServiceNamespace()
	agent := strings.TrimSpace(agentNamespace)
	collector := strings.TrimSpace(n.cfg.Value)
	if agent == "" {
		res.SetServiceNamespace(collector)
		return
	}
	if agent == collector {
		return
	}

	metrics.NamespaceMismatch.Inc(payload)
	key := mismatchKey{service: res.GetServiceName(), value: agent}
	n.logger.Log(context.Background(), n.mismatches.level(key), "agent namespace does not match the collector's configured namespace",
		"service", res.GetServiceName(),
		"instance", res.GetServiceInstanceId(),
		"run", res.GetRunId(),
		"payload", payload,
		"agent_namespace", agentNamespace,
		"collector_namespace", collector,
		"action", n.cfg.Action.String(),
	)
	if n.cfg.Action == Upsert {
		res.SetServiceNamespace(collector)
	}
}
