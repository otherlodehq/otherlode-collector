package ingest

import (
	"context"
	"log/slog"

	otherlodepb "buf.build/gen/go/otherlode/otherlode/protocolbuffers/go/otherlode/v1"
)

// LogSink logs each payload's identity and size and stores nothing. The
// collector uses it when no forward URL is set.
type LogSink struct {
	logger *slog.Logger
}

// NewLogSink returns a LogSink writing to logger, or slog.Default() when
// logger is nil.
func NewLogSink(logger *slog.Logger) *LogSink {
	if logger == nil {
		logger = slog.Default()
	}
	return &LogSink{logger: logger}
}

// AcceptDeltaBatch logs the batch's identity and the size of each list
// it carries, and the time its run's counts have been pending since when
// there are pending counts. It never fails.
func (s *LogSink) AcceptDeltaBatch(_ context.Context, batch *otherlodepb.DeltaBatch) error {
	attrs := []any{
		"namespace", batch.GetResource().GetServiceNamespace(),
		"service", batch.GetResource().GetServiceName(),
		"instance", batch.GetResource().GetServiceInstanceId(),
		"run", batch.GetResource().GetRunId(),
		"environment", batch.GetResource().GetEnvironment(),
		"test_run", batch.GetResource().GetTestRun(),
		"agent_version", batch.GetResource().GetAgentVersion(),
		"deltas", len(batch.GetDeltas()),
		"endpoint_deltas", len(batch.GetEndpointDeltas()),
		"dependency_deltas", len(batch.GetDependencyDeltas()),
	}
	if since := batch.GetCountsPendingSince(); since != 0 {
		attrs = append(attrs, "counts_pending_since", since)
	}
	s.logger.Info("received delta batch", attrs...)
	return nil
}

// AcceptManifest logs the manifest's identity and the size of each list
// it carries, and the time its run's counts have been pending since when
// there are pending counts. It never fails.
func (s *LogSink) AcceptManifest(_ context.Context, manifest *otherlodepb.ProbeManifest) error {
	attrs := []any{
		"namespace", manifest.GetResource().GetServiceNamespace(),
		"service", manifest.GetResource().GetServiceName(),
		"instance", manifest.GetResource().GetServiceInstanceId(),
		"run", manifest.GetResource().GetRunId(),
		"environment", manifest.GetResource().GetEnvironment(),
		"test_run", manifest.GetResource().GetTestRun(),
		"agent_version", manifest.GetResource().GetAgentVersion(),
		"probes", len(manifest.GetProbes()),
		"call_edges", callEdgeCount(manifest.GetProbes()),
		"class_locations", len(manifest.GetClassLocations()),
		"skipped_classes", len(manifest.GetSkippedClasses()),
		"failed_classes", len(manifest.GetFailedClasses()),
		"endpoints", len(manifest.GetEndpoints()),
		"disabled_endpoint_modules", len(manifest.GetDisabledEndpointModules()),
		"dependencies", len(manifest.GetDependencies()),
		"referenced_classes", referencedClassCount(manifest.GetProbes()),
		"class_references", len(manifest.GetClassReferences()),
		"external_classes", len(manifest.GetExternalClasses()),
		"references_recorded", manifest.GetReferencesRecorded(),
		"dependencies_listed", manifest.GetDependenciesListed(),
	}
	if since := manifest.GetCountsPendingSince(); since != 0 {
		attrs = append(attrs, "counts_pending_since", since)
	}
	s.logger.Info("received probe manifest", attrs...)
	return nil
}

// AcceptStaticBaseline logs the baseline's identity, its chunk position
// and the size of each list it carries. It never fails.
func (s *LogSink) AcceptStaticBaseline(_ context.Context, baseline *otherlodepb.StaticBaseline) error {
	s.logger.Info("received static baseline",
		"namespace", baseline.GetResource().GetServiceNamespace(),
		"service", baseline.GetResource().GetServiceName(),
		"instance", baseline.GetResource().GetServiceInstanceId(),
		"run", baseline.GetResource().GetRunId(),
		"environment", baseline.GetResource().GetEnvironment(),
		"test_run", baseline.GetResource().GetTestRun(),
		"agent_version", baseline.GetResource().GetAgentVersion(),
		"scanned_at", baseline.GetScannedAt(),
		"chunk", baseline.GetChunkIndex(),
		"chunk_count", baseline.GetChunkCount(),
		"declared_classes", len(baseline.GetDeclaredClasses()),
		"declared_call_edges", declaredCallEdgeCount(baseline.GetDeclaredClasses()),
		"declared_referenced_classes", declaredReferencedClassCount(baseline.GetDeclaredClasses()),
		"unreadable_classes", len(baseline.GetUnreadableClasses()),
		"unprobed_classes", len(baseline.GetUnprobedClasses()),
	)
	return nil
}

func callEdgeCount(probes []*otherlodepb.ProbeLocation) int {
	n := 0
	for _, p := range probes {
		n += len(p.GetCalls())
	}
	return n
}

func declaredCallEdgeCount(classes []*otherlodepb.DeclaredClass) int {
	n := 0
	for _, c := range classes {
		for _, m := range c.GetMethods() {
			n += len(m.GetCalls())
		}
	}
	return n
}

func referencedClassCount(probes []*otherlodepb.ProbeLocation) int {
	n := 0
	for _, p := range probes {
		n += len(p.GetReferencedClasses())
	}
	return n
}

func declaredReferencedClassCount(classes []*otherlodepb.DeclaredClass) int {
	n := 0
	for _, c := range classes {
		n += len(c.GetReferencedClasses())
		for _, m := range c.GetMethods() {
			n += len(m.GetReferencedClasses())
		}
	}
	return n
}
