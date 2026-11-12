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

// RedactionConfig configures a Redaction processor. The processor redacts a
// literal part when AllLiterals is true or any pattern in BlockedValues
// matches some of its text. Secret keys the HMAC that replaces branch and site keys.
// A zero RedactionConfig means the processor is off.
type RedactionConfig struct {
	BlockedValues []*regexp.Regexp
	AllLiterals   bool
	Secret        []byte
}

// Enabled reports whether the config redacts anything.
func (c RedactionConfig) Enabled() bool {
	return c.AllLiterals || len(c.BlockedValues) > 0
}

// Validate returns an error when the config is enabled and its Secret fails
// CheckSecret, or when it is off and still holds a Secret.
func (c RedactionConfig) Validate() error {
	if !c.Enabled() {
		if len(c.Secret) > 0 {
			return errSecretWithoutRedaction
		}
		return nil
	}
	return CheckSecret(c.Secret)
}

// Redaction is an ingest.Sink that hides string literals before a payload
// leaves the collector, then passes the payload to next. It changes the
// decoded message in place.
//
// It replaces the text of each blocked literal ConditionPart with
// RedactedText. A part counts as a literal unless its kind is CODE or
// PLACEHOLDER, so a part of an unknown kind counts as a literal. It replaces
// each non-empty branch key and site key with a keyed HMAC-SHA256 digest. It
// clears every case key of a site marked string_hash_code_switch, and each
// case key that equals the Java hash code of a literal it redacted in the
// same method. It drops unknown fields. When a payload loses a field, it
// sets fields_stripped on the payload's resource and never clears it.
type Redaction struct {
	next   ingest.Sink
	cfg    RedactionConfig
	logger *slog.Logger

	mu         sync.Mutex
	warnedRuns map[string]struct{}
}

const maxWarnedRuns = 10000

var _ ingest.Sink = (*Redaction)(nil)

// NewRedaction returns a Redaction that applies cfg and passes payloads to
// next. A nil logger means slog.Default(). It panics when cfg fails Validate.
func NewRedaction(next ingest.Sink, cfg RedactionConfig, logger *slog.Logger) *Redaction {
	if err := cfg.Validate(); err != nil {
		panic("processor.NewRedaction: " + err.Error())
	}
	if logger == nil {
		logger = slog.Default()
	}
	return &Redaction{next: next, cfg: cfg, logger: logger, warnedRuns: make(map[string]struct{})}
}

// AcceptDeltaBatch redacts the payload as described on Redaction, then passes it
// to next.
func (r *Redaction) AcceptDeltaBatch(ctx context.Context, batch *otherlodepb.DeltaBatch) ([]ingest.Notice, error) {
	r.markStripped(batch.GetResource(), "deltas", dropUnknown(batch.ProtoReflect()))
	return r.next.AcceptDeltaBatch(ctx, batch)
}

// AcceptManifest redacts the payload as described on Redaction, then passes it
// to next.
func (r *Redaction) AcceptManifest(ctx context.Context, manifest *otherlodepb.ProbeManifest) ([]ingest.Notice, error) {
	keys := newRekeyer(r.cfg.Secret)
	var c counts
	for _, probe := range manifest.GetProbes() {
		if probe.HasBranchKey() && probe.GetBranchKey() != "" {
			probe.SetBranchKey(keys.rekey(probe.GetBranchKey()))
		}
		c.add(r.redactMethodSites(probe.GetBranchSites(), keys))
	}
	r.record(manifest.GetResource(), "manifest", c)
	r.markStripped(manifest.GetResource(), "manifest", dropUnknown(manifest.ProtoReflect()))
	return r.next.AcceptManifest(ctx, manifest)
}

// AcceptStaticBaseline redacts the payload as described on Redaction, then passes it
// to next.
func (r *Redaction) AcceptStaticBaseline(ctx context.Context, baseline *otherlodepb.StaticBaseline) ([]ingest.Notice, error) {
	keys := newRekeyer(r.cfg.Secret)
	var c counts
	for _, class := range baseline.GetDeclaredClasses() {
		for _, method := range class.GetMethods() {
			c.add(r.redactMethodSites(method.GetBranchSites(), keys))
		}
	}
	r.record(baseline.GetResource(), "static_baseline", c)
	r.markStripped(baseline.GetResource(), "static_baseline", dropUnknown(baseline.ProtoReflect()))
	return r.next.AcceptStaticBaseline(ctx, baseline)
}

// AcceptClassListing redacts the payload as described on Redaction, then passes it
// to next.
func (r *Redaction) AcceptClassListing(ctx context.Context, listing *otherlodepb.ClassListing) ([]ingest.Notice, error) {
	r.markStripped(listing.GetResource(), "class_listing", dropUnknown(listing.ProtoReflect()))
	return r.next.AcceptClassListing(ctx, listing)
}

type counts struct {
	literals int
	caseKeys int
}

func (c *counts) add(o counts) {
	c.literals += o.literals
	c.caseKeys += o.caseKeys
}

func (r *Redaction) redactMethodSites(sites []*otherlodepb.BranchSite, keys *rekeyer) counts {
	var c counts
	var hashes map[int32]struct{}
	marked := false
	for _, site := range sites {
		if site.HasSiteKey() && site.GetSiteKey() != "" {
			site.SetSiteKey(keys.rekey(site.GetSiteKey()))
		}
		c.literals += r.redactParts(site.GetCondition(), &hashes)
		for _, outcome := range site.GetOutcomes() {
			c.literals += r.redactParts(outcome.GetCaseLabel(), &hashes)
		}
		marked = marked || site.GetStringHashCodeSwitch()
	}
	if len(hashes) == 0 && !marked {
		return c
	}
	for _, site := range sites {
		hashSwitch := site.GetStringHashCodeSwitch()
		for _, outcome := range site.GetOutcomes() {
			if !outcome.HasCaseKey() {
				continue
			}
			if _, ok := hashes[outcome.GetCaseKey()]; ok || hashSwitch {
				outcome.ClearCaseKey()
				c.caseKeys++
			}
		}
	}
	return c
}

func (r *Redaction) redactParts(parts []*otherlodepb.ConditionPart, hashes *map[int32]struct{}) int {
	n := 0
	for _, p := range parts {
		if k := p.GetKind(); k == otherlodepb.ConditionPartKind_CODE || k == otherlodepb.ConditionPartKind_PLACEHOLDER {
			continue
		}
		if r.blocks(p.GetText()) {
			if *hashes == nil {
				*hashes = make(map[int32]struct{})
			}
			(*hashes)[javaStringHash(p.GetText())] = struct{}{}
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

func (r *Redaction) record(res *otherlodepb.ResourceAttributes, payload string, c counts) {
	if c.literals == 0 && c.caseKeys == 0 {
		return
	}
	metrics.RedactedLiterals.Add(int64(c.literals), payload)
	metrics.RedactedCaseKeys.Add(int64(c.caseKeys), payload)
	r.logger.Debug("redacted string literals",
		"namespace", res.GetServiceNamespace(),
		"service", res.GetServiceName(),
		"instance", res.GetServiceInstanceId(),
		"run", res.GetRunId(),
		"payload", payload,
		"redacted", c.literals,
		"case_keys_cleared", c.caseKeys,
	)
}

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
