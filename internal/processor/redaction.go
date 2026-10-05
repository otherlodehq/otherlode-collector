package processor

import (
	"context"
	"log/slog"
	"regexp"
	"sync"

	otherlodepb "buf.build/gen/go/otherlode/otherlode/protocolbuffers/go/otherlode/v1"
	"google.golang.org/protobuf/reflect/protoreflect"

	"github.com/otherlodehq/otherlode-collector/ingest"
	"github.com/otherlodehq/otherlode-collector/metrics"
)

// RedactedText is the text a redacted literal part carries.
const RedactedText = "…"

// RedactionConfig configures a Redaction processor. A literal part is
// redacted when AllLiterals is true or any pattern in BlockedValues
// matches some of its text. A part counts as a literal unless its kind is
// CODE or PLACEHOLDER. A zero RedactionConfig means the processor is off.
type RedactionConfig struct {
	BlockedValues []*regexp.Regexp
	AllLiterals   bool
}

// Enabled reports whether the config redacts anything.
func (c RedactionConfig) Enabled() bool {
	return c.AllLiterals || len(c.BlockedValues) > 0
}

// Redaction is an ingest.Sink that hides string literals before a payload
// leaves the collector, then passes the payload to next. See ADR 0001.
//
// It looks at ConditionPart values. These sit in each branch site's
// condition and in each outcome's case label, on manifest probes and on
// static baseline methods. Only parts of kind CODE or PLACEHOLDER are
// exempt. Any other kind counts as a literal, including
// STRING_LITERAL, UNSPECIFIED and a kind this build does not know. The
// proto enum is open, so an unknown kind decodes as a plain number. A
// newer agent can add a literal kind, so a part of an unknown kind fails
// closed. A redacted part keeps its kind, and its text becomes
// RedactedText. Names and files pass through unchanged.
//
// It also drops unknown fields from every message of every payload. A
// field the collector's bindings do not know could carry a literal that
// this processor cannot see. A dropped field can also carry meaning the
// server needs, so when a payload loses at least one, the processor sets
// fields_stripped on the payload's resource. It never clears the flag.
// It counts each such payload in otherlode_collector_fields_stripped_total
// and logs one WARNING per run id. A payload with no resource has nowhere
// to carry the flag and passes unmarked. See ADR 0005.
//
// Like Environment, it changes the decoded message in place.
type Redaction struct {
	next   ingest.Sink
	cfg    RedactionConfig
	logger *slog.Logger

	mu         sync.Mutex
	warnedRuns map[string]struct{}
}

// maxWarnedRuns bounds the set of run ids the processor has warned about.
const maxWarnedRuns = 10000

var _ ingest.Sink = (*Redaction)(nil)

// NewRedaction returns a Redaction that applies cfg before passing
// payloads to next, logging to logger, or slog.Default() when logger is
// nil.
func NewRedaction(next ingest.Sink, cfg RedactionConfig, logger *slog.Logger) *Redaction {
	if logger == nil {
		logger = slog.Default()
	}
	return &Redaction{next: next, cfg: cfg, logger: logger, warnedRuns: make(map[string]struct{})}
}

// AcceptDeltaBatch drops the batch's unknown fields, marks the batch if
// it lost any, then passes it to next. A delta batch holds no condition
// parts.
func (r *Redaction) AcceptDeltaBatch(ctx context.Context, batch *otherlodepb.DeltaBatch) error {
	r.markStripped(batch.GetResource(), "deltas", dropUnknown(batch.ProtoReflect()))
	return r.next.AcceptDeltaBatch(ctx, batch)
}

// AcceptManifest redacts the literals in the branch sites of every probe,
// drops unknown fields, marks the manifest if it lost any, then passes it
// to next.
func (r *Redaction) AcceptManifest(ctx context.Context, manifest *otherlodepb.ProbeManifest) error {
	n := 0
	for _, probe := range manifest.GetProbes() {
		n += r.redactSites(probe.GetBranchSites())
	}
	r.record(manifest.GetResource(), "manifest", n)
	r.markStripped(manifest.GetResource(), "manifest", dropUnknown(manifest.ProtoReflect()))
	return r.next.AcceptManifest(ctx, manifest)
}

// AcceptStaticBaseline redacts the literals in the branch sites of every
// declared method, drops unknown fields, marks the baseline if it lost
// any, then passes it to next.
func (r *Redaction) AcceptStaticBaseline(ctx context.Context, baseline *otherlodepb.StaticBaseline) error {
	n := 0
	for _, class := range baseline.GetDeclaredClasses() {
		for _, method := range class.GetMethods() {
			n += r.redactSites(method.GetBranchSites())
		}
	}
	r.record(baseline.GetResource(), "static_baseline", n)
	r.markStripped(baseline.GetResource(), "static_baseline", dropUnknown(baseline.ProtoReflect()))
	return r.next.AcceptStaticBaseline(ctx, baseline)
}

// redactSites redacts each site's condition and each of its outcomes'
// case labels. It returns the number of parts it replaced.
func (r *Redaction) redactSites(sites []*otherlodepb.BranchSite) int {
	n := 0
	for _, site := range sites {
		n += r.redactParts(site.GetCondition())
		for _, outcome := range site.GetOutcomes() {
			n += r.redactParts(outcome.GetCaseLabel())
		}
	}
	return n
}

// redactParts replaces the text of each literal part that the config
// blocks. It returns the number of parts it replaced.
func (r *Redaction) redactParts(parts []*otherlodepb.ConditionPart) int {
	n := 0
	for _, p := range parts {
		if k := p.GetKind(); k == otherlodepb.ConditionPartKind_CODE || k == otherlodepb.ConditionPartKind_PLACEHOLDER {
			continue
		}
		if r.blocks(p.GetText()) {
			p.SetText(RedactedText)
			n++
		}
	}
	return n
}

func (r *Redaction) blocks(text string) bool {
	if r.cfg.AllLiterals {
		return true
	}
	for _, re := range r.cfg.BlockedValues {
		if re.MatchString(text) {
			return true
		}
	}
	return false
}

// record counts n replaced parts and logs them once for the payload. It
// never logs a literal's text.
func (r *Redaction) record(res *otherlodepb.ResourceAttributes, payload string, n int) {
	if n == 0 {
		return
	}
	metrics.RedactedLiterals.Add(int64(n), payload)
	r.logger.Debug("redacted string literals",
		"namespace", res.GetServiceNamespace(),
		"service", res.GetServiceName(),
		"instance", res.GetServiceInstanceId(),
		"run", res.GetRunId(),
		"payload", payload,
		"redacted", n,
	)
}

// markStripped sets fields_stripped on res when dropped is true, counts
// the payload and warns once per run id. It does nothing when dropped is
// false, and it leaves a nil res alone.
func (r *Redaction) markStripped(res *otherlodepb.ResourceAttributes, payload string, dropped bool) {
	if !dropped || res == nil {
		return
	}
	res.SetFieldsStripped(true)
	metrics.FieldsStripped.Inc(payload)
	if !r.firstStripForRun(res.GetRunId()) {
		return
	}
	r.logger.Warn("dropped fields this collector does not know; its bindings are older than the agent's, so the server withholds findings from this run; upgrade to the latest collector",
		"namespace", res.GetServiceNamespace(),
		"service", res.GetServiceName(),
		"instance", res.GetServiceInstanceId(),
		"run", res.GetRunId(),
		"agent_version", res.GetAgentVersion(),
		"payload", payload,
	)
}

// firstStripForRun reports whether run has not been warned about, and
// remembers it. The set is cleared when it reaches maxWarnedRuns.
func (r *Redaction) firstStripForRun(run string) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, ok := r.warnedRuns[run]; ok {
		return false
	}
	if len(r.warnedRuns) >= maxWarnedRuns {
		clear(r.warnedRuns)
	}
	r.warnedRuns[run] = struct{}{}
	return true
}

// dropUnknown clears the unknown fields of m and of every message
// nested in it, through singular, repeated and map fields. It reports
// whether it cleared any.
func dropUnknown(m protoreflect.Message) bool {
	dropped := false
	if len(m.GetUnknown()) > 0 {
		m.SetUnknown(nil)
		dropped = true
	}
	m.Range(func(fd protoreflect.FieldDescriptor, v protoreflect.Value) bool {
		switch {
		case fd.IsMap():
			if fd.MapValue().Message() != nil {
				v.Map().Range(func(_ protoreflect.MapKey, mv protoreflect.Value) bool {
					if dropUnknown(mv.Message()) {
						dropped = true
					}
					return true
				})
			}
		case fd.IsList():
			if fd.Message() != nil {
				list := v.List()
				for i := 0; i < list.Len(); i++ {
					if dropUnknown(list.Get(i).Message()) {
						dropped = true
					}
				}
			}
		case fd.Message() != nil:
			if dropUnknown(v.Message()) {
				dropped = true
			}
		}
		return true
	})
	return dropped
}
