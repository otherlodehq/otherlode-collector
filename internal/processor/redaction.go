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
// CODE or PLACEHOLDER. Secret keys the HMAC that replaces branch and site
// keys. A zero RedactionConfig means the processor is off.
type RedactionConfig struct {
	BlockedValues []*regexp.Regexp
	AllLiterals   bool
	Secret        []byte
}

// Enabled reports whether the config redacts anything.
func (c RedactionConfig) Enabled() bool {
	return c.AllLiterals || len(c.BlockedValues) > 0
}

// Validate returns an error when the config is enabled and its Secret
// fails CheckSecret, or when it is off and still holds a Secret. See ADR
// 0007.
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
// It replaces each branch key and site key with HMAC-SHA256 of the key
// under the config's Secret, cut to 16 bytes and written as 32 lowercase
// hex characters. The agent digests a site's string constants into its
// keys, so a plain key lets anyone who holds it test guesses at a
// redacted literal. Equal keys stay equal, which is all the server needs.
// An empty key stays empty. A switch on a string's hash code that the
// agent could not read back sends those hash codes as case keys. So it
// clears every case key of a site the agent marks string_hash_code_switch,
// and a case key that equals Java's String.hashCode() of a literal it
// redacted in the same method. See ADR 0007.
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
// nil. It panics when cfg fails Validate, since a weak secret would leave
// every redacted literal open to guesses.
func NewRedaction(next ingest.Sink, cfg RedactionConfig, logger *slog.Logger) *Redaction {
	if err := cfg.Validate(); err != nil {
		panic("processor.NewRedaction: " + err.Error())
	}
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
// re-keys every branch key and site key, clears the case keys that give
// away a redacted literal, drops unknown fields, marks the manifest if it
// lost any, then passes it to next. Each probe's branch sites are one
// method's sites.
func (r *Redaction) AcceptManifest(ctx context.Context, manifest *otherlodepb.ProbeManifest) error {
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

// AcceptStaticBaseline redacts the literals in the branch sites of every
// declared method, re-keys every site key, clears the case keys that give
// away a redacted literal, drops unknown fields, marks the baseline if it
// lost any, then passes it to next.
func (r *Redaction) AcceptStaticBaseline(ctx context.Context, baseline *otherlodepb.StaticBaseline) error {
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

// counts is what the processor changed in one payload.
type counts struct {
	literals int
	caseKeys int
}

func (c *counts) add(o counts) {
	c.literals += o.literals
	c.caseKeys += o.caseKeys
}

// redactMethodSites redacts each site's condition and each of its
// outcomes' case labels, and re-keys each site key. sites are the branch
// sites of one method. It clears two kinds of case key, since the agent
// sends the hash codes of a string switch it could not read back as plain
// case keys. It clears every case key of a site marked
// string_hash_code_switch. It also clears a case key that equals
// String.hashCode() of a literal redacted in that method, for an agent
// that does not send the mark: the literals sit in the conditions of the
// equals checks beside the switch.
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

// redactParts replaces the text of each literal part that the config
// blocks, and adds the Java hash code of each replaced text to *hashes.
// It returns the number of parts it replaced.
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

// record counts the replaced parts and cleared case keys in c and logs
// them once for the payload. It never logs a literal's text or a key.
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
