package processor

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"regexp"
	"strconv"
	"strings"
	"testing"

	otherlodepb "buf.build/gen/go/otherlode/otherlode/protocolbuffers/go/otherlode/v1"
	"google.golang.org/protobuf/encoding/protowire"

	"github.com/otherlodehq/otherlode-collector/metrics"
)

func part(kind otherlodepb.ConditionPartKind, text string) *otherlodepb.ConditionPart {
	return &otherlodepb.ConditionPart{Kind: kind, Text: text}
}

func code(text string) *otherlodepb.ConditionPart {
	return part(otherlodepb.ConditionPartKind_CODE, text)
}

func literal(text string) *otherlodepb.ConditionPart {
	return part(otherlodepb.ConditionPartKind_STRING_LITERAL, text)
}

func placeholder(text string) *otherlodepb.ConditionPart {
	return part(otherlodepb.ConditionPartKind_PLACEHOLDER, text)
}

// site builds a branch site with the given condition and one CASE
// outcome carrying caseLabel.
func site(condition []*otherlodepb.ConditionPart, caseLabel []*otherlodepb.ConditionPart) *otherlodepb.BranchSite {
	return &otherlodepb.BranchSite{
		Condition: condition,
		Outcomes: []*otherlodepb.BranchOutcome{
			{Role: otherlodepb.BranchRole_CASE, CaseLabel: caseLabel},
			{Role: otherlodepb.BranchRole_DEFAULT},
		},
	}
}

func resourceFor(run string) *otherlodepb.ResourceAttributes {
	return &otherlodepb.ResourceAttributes{ServiceName: "svc", ServiceInstanceId: "i1", RunId: run}
}

func manifestWith(sites ...*otherlodepb.BranchSite) *otherlodepb.ProbeManifest {
	return &otherlodepb.ProbeManifest{
		Resource: resourceFor("run-1"),
		Probes:   []*otherlodepb.ProbeLocation{{ClassName: "com.example.Pricing", MethodName: "price", BranchSites: sites}},
	}
}

func baselineWith(sites ...*otherlodepb.BranchSite) *otherlodepb.StaticBaseline {
	return &otherlodepb.StaticBaseline{
		Resource:   resourceFor("run-1"),
		ScannedAt:  1700000000,
		ChunkCount: 1,
		DeclaredClasses: []*otherlodepb.DeclaredClass{{
			ClassName: "com.example.Pricing",
			Methods:   []*otherlodepb.DeclaredMethod{{MethodName: "price", BranchSites: sites}},
		}},
	}
}

// testSecret is a redaction secret of exactly MinSecretBytes.
var testSecret = []byte("0123456789abcdef0123456789abcdef")

// allLiterals is a config that redacts every literal.
func allLiterals() RedactionConfig {
	return RedactionConfig{AllLiterals: true, Secret: testSecret}
}

func blocked(t *testing.T, patterns ...string) RedactionConfig {
	t.Helper()
	cfg := RedactionConfig{Secret: testSecret}
	for _, p := range patterns {
		cfg.BlockedValues = append(cfg.BlockedValues, regexp.MustCompile(p))
	}
	return cfg
}

func texts(parts []*otherlodepb.ConditionPart) []string {
	out := make([]string, len(parts))
	for i, p := range parts {
		out[i] = p.GetText()
	}
	return out
}

func assertTexts(t *testing.T, what string, parts []*otherlodepb.ConditionPart, want ...string) {
	t.Helper()
	got := texts(parts)
	if len(got) != len(want) {
		t.Fatalf("%s = %q, want %q", what, got, want)
	}
	for i := range got {
		if got[i] != want[i] {
			t.Fatalf("%s = %q, want %q", what, got, want)
		}
	}
}

func TestRedaction_BlockedPatternMatchesLiteral_Replaced(t *testing.T) {
	next := &recordingSink{}
	r := NewRedaction(next, blocked(t, "LEGACY"), nil)

	manifest := manifestWith(site(
		[]*otherlodepb.ConditionPart{code("System.getenv("), literal("ENABLE_LEGACY_DISCOUNT"), code(") == "), literal("true")},
		nil,
	))
	if err := r.AcceptManifest(context.Background(), manifest); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	got := next.manifests[0].GetProbes()[0].GetBranchSites()[0].GetCondition()
	assertTexts(t, "condition", got, "System.getenv(", "…", ") == ", "true")
	if kind := got[1].GetKind(); kind != otherlodepb.ConditionPartKind_STRING_LITERAL {
		t.Fatalf("redacted part kind = %v, want STRING_LITERAL", kind)
	}
}

func TestRedaction_NoPatternMatchesLiteral_Kept(t *testing.T) {
	next := &recordingSink{}
	r := NewRedaction(next, blocked(t, "^secret$", "password"), nil)

	manifest := manifestWith(site(
		[]*otherlodepb.ConditionPart{code("mode == "), literal("fast")},
		[]*otherlodepb.ConditionPart{literal("slow")},
	))
	if err := r.AcceptManifest(context.Background(), manifest); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	s := next.manifests[0].GetProbes()[0].GetBranchSites()[0]
	assertTexts(t, "condition", s.GetCondition(), "mode == ", "fast")
	assertTexts(t, "case label", s.GetOutcomes()[0].GetCaseLabel(), "slow")
}

func TestRedaction_CodeAndPlaceholderParts_NeverTouched(t *testing.T) {
	next := &recordingSink{}
	r := NewRedaction(next, blocked(t, "token"), nil)

	manifest := manifestWith(site(
		[]*otherlodepb.ConditionPart{code("token.isEmpty() && "), placeholder("token"), code(" == "), literal("token-1")},
		[]*otherlodepb.ConditionPart{code("Token.NONE")},
	))
	if err := r.AcceptManifest(context.Background(), manifest); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	s := next.manifests[0].GetProbes()[0].GetBranchSites()[0]
	assertTexts(t, "condition", s.GetCondition(), "token.isEmpty() && ", "token", " == ", "…")
	assertTexts(t, "case label", s.GetOutcomes()[0].GetCaseLabel(), "Token.NONE")
}

func TestRedaction_NonCodeKinds_TreatedAsLiterals(t *testing.T) {
	const unknownKind otherlodepb.ConditionPartKind = 7
	configs := map[string]RedactionConfig{
		"AllLiterals":   allLiterals(),
		"BlockedValues": blocked(t, "secret"),
	}
	for name, cfg := range configs {
		t.Run(name, func(t *testing.T) {
			next := &recordingSink{}
			r := NewRedaction(next, cfg, nil)

			manifest := manifestWith(site(
				[]*otherlodepb.ConditionPart{
					part(unknownKind, "secret-a"),
					part(otherlodepb.ConditionPartKind_CONDITION_PART_KIND_UNSPECIFIED, "secret-b"),
					code("secret-c"),
					placeholder("secret-d"),
				},
				[]*otherlodepb.ConditionPart{part(unknownKind, "secret-e")},
			))
			if err := r.AcceptManifest(context.Background(), manifest); err != nil {
				t.Fatalf("unexpected error: %v", err)
			}

			s := next.manifests[0].GetProbes()[0].GetBranchSites()[0]
			cond := s.GetCondition()
			assertTexts(t, "condition", cond, "…", "…", "secret-c", "secret-d")
			if k := cond[0].GetKind(); k != unknownKind {
				t.Errorf("redacted unknown part kind = %v, want 7", k)
			}
			if k := cond[1].GetKind(); k != otherlodepb.ConditionPartKind_CONDITION_PART_KIND_UNSPECIFIED {
				t.Errorf("redacted unspecified part kind = %v, want UNSPECIFIED", k)
			}
			assertTexts(t, "case label", s.GetOutcomes()[0].GetCaseLabel(), "…")
		})
	}
}

func TestRedaction_BlockedPatternMatchesUnanchored(t *testing.T) {
	next := &recordingSink{}
	r := NewRedaction(next, blocked(t, "[0-9]{4}"), nil)

	baseline := baselineWith(site([]*otherlodepb.ConditionPart{literal("card 4111 1111")}, nil))
	if err := r.AcceptStaticBaseline(context.Background(), baseline); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	got := next.baselines[0].GetDeclaredClasses()[0].GetMethods()[0].GetBranchSites()[0].GetCondition()
	assertTexts(t, "condition", got, "…")
}

func TestRedaction_AllLiterals_ManifestConditionsAndCaseLabelsReplaced(t *testing.T) {
	next := &recordingSink{}
	r := NewRedaction(next, allLiterals(), nil)

	manifest := manifestWith(
		site([]*otherlodepb.ConditionPart{code("name == "), literal("alice")}, []*otherlodepb.ConditionPart{literal("bob")}),
		site([]*otherlodepb.ConditionPart{code("x > 0")}, []*otherlodepb.ConditionPart{code("Color.RED")}),
	)
	if err := r.AcceptManifest(context.Background(), manifest); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	sites := next.manifests[0].GetProbes()[0].GetBranchSites()
	assertTexts(t, "first condition", sites[0].GetCondition(), "name == ", "…")
	assertTexts(t, "first case label", sites[0].GetOutcomes()[0].GetCaseLabel(), "…")
	assertTexts(t, "second condition", sites[1].GetCondition(), "x > 0")
	assertTexts(t, "second case label", sites[1].GetOutcomes()[0].GetCaseLabel(), "Color.RED")
}

func TestRedaction_AllLiterals_StaticBaselineConditionsAndCaseLabelsReplaced(t *testing.T) {
	next := &recordingSink{}
	r := NewRedaction(next, allLiterals(), nil)

	baseline := baselineWith(site(
		[]*otherlodepb.ConditionPart{code("System.getenv("), literal("MODE"), code(")")},
		[]*otherlodepb.ConditionPart{literal("legacy")},
	))
	if err := r.AcceptStaticBaseline(context.Background(), baseline); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	s := next.baselines[0].GetDeclaredClasses()[0].GetMethods()[0].GetBranchSites()[0]
	assertTexts(t, "condition", s.GetCondition(), "System.getenv(", "…", ")")
	assertTexts(t, "case label", s.GetOutcomes()[0].GetCaseLabel(), "…")
}

func TestRedaction_Counter_CountsReplacedPartsPerPayload(t *testing.T) {
	beforeManifest := metrics.RedactedLiterals.Value("manifest")
	beforeBaseline := metrics.RedactedLiterals.Value("static_baseline")

	next := &recordingSink{}
	r := NewRedaction(next, blocked(t, "secret"), nil)

	manifest := manifestWith(site(
		[]*otherlodepb.ConditionPart{literal("secret-a"), code(" + "), literal("secret-b"), literal("public")},
		[]*otherlodepb.ConditionPart{literal("top-secret")},
	))
	if err := r.AcceptManifest(context.Background(), manifest); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	baseline := baselineWith(site([]*otherlodepb.ConditionPart{literal("secret-c"), code("secret")}, nil))
	if err := r.AcceptStaticBaseline(context.Background(), baseline); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if got := metrics.RedactedLiterals.Value("manifest") - beforeManifest; got != 3 {
		t.Fatalf("manifest counter increased by %d, want 3", got)
	}
	if got := metrics.RedactedLiterals.Value("static_baseline") - beforeBaseline; got != 1 {
		t.Fatalf("static_baseline counter increased by %d, want 1", got)
	}
}

// unknownBytes is one field that no message in the schema declares.
func unknownBytes() []byte {
	b := protowire.AppendTag(nil, 9999, protowire.BytesType)
	return protowire.AppendString(b, "a newer literal")
}

func TestRedaction_On_UnknownFieldsDroppedFromManifest(t *testing.T) {
	next := &recordingSink{}
	r := NewRedaction(next, blocked(t, "never-matches"), nil)

	s := site([]*otherlodepb.ConditionPart{code("ok")}, []*otherlodepb.ConditionPart{literal("x")})
	s.ProtoReflect().SetUnknown(unknownBytes())
	s.GetOutcomes()[0].GetCaseLabel()[0].ProtoReflect().SetUnknown(unknownBytes())
	manifest := manifestWith(s)
	manifest.ProtoReflect().SetUnknown(unknownBytes())

	if err := r.AcceptManifest(context.Background(), manifest); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	got := next.manifests[0]
	if n := len(got.ProtoReflect().GetUnknown()); n != 0 {
		t.Fatalf("manifest still has %d unknown bytes", n)
	}
	gotSite := got.GetProbes()[0].GetBranchSites()[0]
	if n := len(gotSite.ProtoReflect().GetUnknown()); n != 0 {
		t.Fatalf("branch site still has %d unknown bytes", n)
	}
	if n := len(gotSite.GetOutcomes()[0].GetCaseLabel()[0].ProtoReflect().GetUnknown()); n != 0 {
		t.Fatalf("case label part still has %d unknown bytes", n)
	}
}

func TestRedaction_On_UnknownFieldsDroppedFromStaticBaseline(t *testing.T) {
	next := &recordingSink{}
	r := NewRedaction(next, allLiterals(), nil)

	s := site([]*otherlodepb.ConditionPart{code("ok")}, nil)
	s.ProtoReflect().SetUnknown(unknownBytes())
	baseline := baselineWith(s)
	baseline.ProtoReflect().SetUnknown(unknownBytes())

	if err := r.AcceptStaticBaseline(context.Background(), baseline); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	got := next.baselines[0]
	if n := len(got.ProtoReflect().GetUnknown()); n != 0 {
		t.Fatalf("baseline still has %d unknown bytes", n)
	}
	gotSite := got.GetDeclaredClasses()[0].GetMethods()[0].GetBranchSites()[0]
	if n := len(gotSite.ProtoReflect().GetUnknown()); n != 0 {
		t.Fatalf("branch site still has %d unknown bytes", n)
	}
}

func TestRedaction_On_UnknownFieldsDroppedFromDeltaBatch(t *testing.T) {
	next := &recordingSink{}
	r := NewRedaction(next, allLiterals(), nil)

	delta := &otherlodepb.ProbeDelta{ClassId: 7, HitsTotal: 3}
	delta.ProtoReflect().SetUnknown(unknownBytes())
	batch := &otherlodepb.DeltaBatch{Resource: resourceFor("run-1"), Deltas: []*otherlodepb.ProbeDelta{delta}}
	batch.GetResource().ProtoReflect().SetUnknown(unknownBytes())
	batch.ProtoReflect().SetUnknown(unknownBytes())

	if err := r.AcceptDeltaBatch(context.Background(), batch); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	got := next.deltaBatches[0]
	if n := len(got.ProtoReflect().GetUnknown()); n != 0 {
		t.Fatalf("batch still has %d unknown bytes", n)
	}
	if n := len(got.GetResource().ProtoReflect().GetUnknown()); n != 0 {
		t.Fatalf("resource still has %d unknown bytes", n)
	}
	if n := len(got.GetDeltas()[0].ProtoReflect().GetUnknown()); n != 0 {
		t.Fatalf("delta still has %d unknown bytes", n)
	}
	if got.GetDeltas()[0].GetHitsTotal() != 3 {
		t.Fatalf("known field lost: hits_total = %d, want 3", got.GetDeltas()[0].GetHitsTotal())
	}
}

func TestRedaction_NextError_Returned(t *testing.T) {
	wantErr := errors.New("sink unavailable")
	next := &recordingSink{err: wantErr}
	r := NewRedaction(next, allLiterals(), nil)

	if err := r.AcceptManifest(context.Background(), manifestWith()); !errors.Is(err, wantErr) {
		t.Fatalf("error = %v, want %v", err, wantErr)
	}
}

func TestRedactionConfig_Enabled(t *testing.T) {
	if (RedactionConfig{}).Enabled() {
		t.Fatal("zero config reports enabled, want off")
	}
	if !(RedactionConfig{AllLiterals: true}).Enabled() {
		t.Fatal("AllLiterals config reports off, want enabled")
	}
	if !blocked(t, "x").Enabled() {
		t.Fatal("config with a blocked pattern reports off, want enabled")
	}
}

// strippedCase builds one payload type, with or without an unknown field
// on a nested message, and reads back the resource that reached next.
type strippedCase struct {
	name    string
	payload string
	send    func(t *testing.T, r *Redaction, run string, withUnknown, flagged bool) *otherlodepb.ResourceAttributes
}

func strippedCases() []strippedCase {
	prep := func(run string, flagged bool) *otherlodepb.ResourceAttributes {
		res := resourceFor(run)
		res.SetAgentVersion("1.2.3")
		res.SetFieldsStripped(flagged)
		return res
	}
	return []strippedCase{
		{"delta batch", "deltas", func(t *testing.T, r *Redaction, run string, withUnknown, flagged bool) *otherlodepb.ResourceAttributes {
			delta := &otherlodepb.ProbeDelta{ClassId: 7, HitsTotal: 3}
			if withUnknown {
				delta.ProtoReflect().SetUnknown(unknownBytes())
			}
			next := r.next.(*recordingSink)
			batch := &otherlodepb.DeltaBatch{Resource: prep(run, flagged), Deltas: []*otherlodepb.ProbeDelta{delta}}
			if err := r.AcceptDeltaBatch(context.Background(), batch); err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			return next.deltaBatches[len(next.deltaBatches)-1].GetResource()
		}},
		{"manifest", "manifest", func(t *testing.T, r *Redaction, run string, withUnknown, flagged bool) *otherlodepb.ResourceAttributes {
			m := manifestWith()
			m.Resource = prep(run, flagged)
			if withUnknown {
				m.GetProbes()[0].ProtoReflect().SetUnknown(unknownBytes())
			}
			next := r.next.(*recordingSink)
			if err := r.AcceptManifest(context.Background(), m); err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			return next.manifests[len(next.manifests)-1].GetResource()
		}},
		{"static baseline", "static_baseline", func(t *testing.T, r *Redaction, run string, withUnknown, flagged bool) *otherlodepb.ResourceAttributes {
			b := baselineWith()
			b.Resource = prep(run, flagged)
			if withUnknown {
				b.GetDeclaredClasses()[0].GetMethods()[0].ProtoReflect().SetUnknown(unknownBytes())
			}
			next := r.next.(*recordingSink)
			if err := r.AcceptStaticBaseline(context.Background(), b); err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			return next.baselines[len(next.baselines)-1].GetResource()
		}},
	}
}

func TestRedaction_UnknownFieldStripped_SetsFieldsStripped(t *testing.T) {
	for _, c := range strippedCases() {
		t.Run(c.name, func(t *testing.T) {
			r := NewRedaction(&recordingSink{}, allLiterals(), nil)
			res := c.send(t, r, "run-1", true, false)
			if !res.GetFieldsStripped() {
				t.Fatal("fields_stripped not set on a payload that lost a field")
			}
		})
	}
}

func TestRedaction_NoUnknownField_LeavesFieldsStrippedUnset(t *testing.T) {
	for _, c := range strippedCases() {
		t.Run(c.name, func(t *testing.T) {
			r := NewRedaction(&recordingSink{}, allLiterals(), nil)
			res := c.send(t, r, "run-1", false, false)
			if res.GetFieldsStripped() {
				t.Fatal("fields_stripped set on a payload that lost nothing")
			}
		})
	}
}

func TestRedaction_AlreadyFlaggedPayload_StaysFlagged(t *testing.T) {
	for _, c := range strippedCases() {
		t.Run(c.name, func(t *testing.T) {
			r := NewRedaction(&recordingSink{}, allLiterals(), nil)
			res := c.send(t, r, "run-1", false, true)
			if !res.GetFieldsStripped() {
				t.Fatal("fields_stripped cleared on an already flagged payload")
			}
		})
	}
}

func TestRedaction_UnknownFieldAndNilResource_PassesWithoutPanic(t *testing.T) {
	next := &recordingSink{}
	r := NewRedaction(next, allLiterals(), nil)
	batch := &otherlodepb.DeltaBatch{}
	batch.ProtoReflect().SetUnknown(unknownBytes())
	if err := r.AcceptDeltaBatch(context.Background(), batch); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if batch.GetResource() != nil {
		t.Fatal("a resource was created for a payload that had none")
	}
}

func TestRedaction_StrippedPayloads_WarnOncePerRun(t *testing.T) {
	var buf bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelWarn}))
	r := NewRedaction(&recordingSink{}, allLiterals(), logger)
	cases := strippedCases()

	cases[0].send(t, r, "run-a", true, false)
	cases[1].send(t, r, "run-a", true, false)
	if got := strings.Count(buf.String(), "level=WARN"); got != 1 {
		t.Fatalf("%d warnings for two stripped payloads of one run, want 1:\n%s", got, buf.String())
	}
	out := buf.String()
	for _, want := range []string{"run-a", "svc", "i1", "agent_version=1.2.3"} {
		if !strings.Contains(out, want) {
			t.Errorf("warning lacks %q:\n%s", want, out)
		}
	}

	cases[2].send(t, r, "run-b", true, false)
	if got := strings.Count(buf.String(), "level=WARN"); got != 2 {
		t.Fatalf("%d warnings after a second run, want 2:\n%s", got, buf.String())
	}

	cases[0].send(t, r, "run-c", false, false)
	if got := strings.Count(buf.String(), "level=WARN"); got != 2 {
		t.Fatalf("an unstripped payload logged a warning:\n%s", buf.String())
	}
}

func TestRedaction_WarnedRuns_BoundedAndCleared(t *testing.T) {
	var buf bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelWarn}))
	r := NewRedaction(&recordingSink{}, allLiterals(), logger)
	c := strippedCases()[0]
	for i := 0; i < maxWarnedRuns+1; i++ {
		c.send(t, r, "run-"+strconv.Itoa(i), true, false)
	}
	r.mu.Lock()
	n := len(r.warnedRuns)
	r.mu.Unlock()
	if n > maxWarnedRuns {
		t.Fatalf("warned set holds %d entries, bound is %d", n, maxWarnedRuns)
	}
}

func TestRedaction_StrippedPayloads_CounterIncrementsPerPayload(t *testing.T) {
	before := map[string]int64{}
	for _, c := range strippedCases() {
		before[c.payload] = metrics.FieldsStripped.Value(c.payload)
	}
	r := NewRedaction(&recordingSink{}, allLiterals(), nil)
	for _, c := range strippedCases() {
		c.send(t, r, "run-1", true, false)
		c.send(t, r, "run-1", true, false)
		c.send(t, r, "run-1", false, false)
	}
	for _, c := range strippedCases() {
		if got := metrics.FieldsStripped.Value(c.payload) - before[c.payload]; got != 2 {
			t.Errorf("%s counter increased by %d, want 2", c.payload, got)
		}
	}
}

// The bindings know a probe's outside caller and a manifest's failed
// classes (agent ADRs 0064 and 0065), so redaction keeps both and does
// not mark the payload stripped.
func TestRedaction_On_OutsideCallerAndFailedClassesKept(t *testing.T) {
	next := &recordingSink{}
	r := NewRedaction(next, allLiterals(), nil)

	manifest := manifestWith()
	manifest.GetProbes()[0].OutsideCaller = &otherlodepb.OutsideCaller{
		Kind:     otherlodepb.OutsideCallerKind_CALLBACK_ANNOTATION,
		TypeName: "org.springframework.context.event.EventListener",
	}
	manifest.FailedClasses = []*otherlodepb.FailedClass{{ClassName: "com.example.Broken", WithheldAt: 1700000000000}}

	if err := r.AcceptManifest(context.Background(), manifest); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	got := next.manifests[0]
	if got.GetResource().GetFieldsStripped() {
		t.Fatal("fields_stripped set on a payload whose fields the bindings know")
	}
	caller := got.GetProbes()[0].GetOutsideCaller()
	if caller.GetKind() != otherlodepb.OutsideCallerKind_CALLBACK_ANNOTATION ||
		caller.GetTypeName() != "org.springframework.context.event.EventListener" {
		t.Fatalf("outside caller = %v, want the callback annotation", caller)
	}
	if failed := got.GetFailedClasses(); len(failed) != 1 || failed[0].GetClassName() != "com.example.Broken" {
		t.Fatalf("failed classes = %v, want com.example.Broken", failed)
	}
}

// The bindings know a disabled module's kind (agent ADR 0017) and the
// payload sequence and pending-since stamp on a delta batch and a manifest
// (agent ADR 0068), so redaction keeps them and does not mark either
// payload stripped.
func TestRedaction_On_ModuleKindAndPendingCountsFieldsKept(t *testing.T) {
	next := &recordingSink{}
	r := NewRedaction(next, allLiterals(), nil)

	manifest := manifestWith()
	manifest.DisabledEndpointModules = []*otherlodepb.DisabledEndpointModule{
		{Module: "spring-webmvc", Reason: "advice failed", Kind: otherlodepb.DisabledEndpointModuleKind_ADVICE_FAILED},
	}
	manifest.PayloadSequence = 4
	manifest.CountsPendingSince = 1700000000000
	batch := &otherlodepb.DeltaBatch{
		Resource:           resourceFor("run-1"),
		Deltas:             []*otherlodepb.ProbeDelta{{ClassId: 7, HitsTotal: 3}},
		PayloadSequence:    5,
		CountsPendingSince: 1700000000000,
	}

	if err := r.AcceptManifest(context.Background(), manifest); err != nil {
		t.Fatalf("accept manifest: %v", err)
	}
	if err := r.AcceptDeltaBatch(context.Background(), batch); err != nil {
		t.Fatalf("accept delta batch: %v", err)
	}

	gotManifest := next.manifests[0]
	if gotManifest.GetResource().GetFieldsStripped() {
		t.Error("fields_stripped set on a manifest whose fields the bindings know")
	}
	if modules := gotManifest.GetDisabledEndpointModules(); len(modules) != 1 ||
		modules[0].GetKind() != otherlodepb.DisabledEndpointModuleKind_ADVICE_FAILED {
		t.Errorf("disabled modules = %v, want one of kind ADVICE_FAILED", modules)
	}
	if gotManifest.GetPayloadSequence() != 4 || gotManifest.GetCountsPendingSince() != 1700000000000 {
		t.Errorf("manifest payload_sequence %d, counts_pending_since %d, want 4 and 1700000000000",
			gotManifest.GetPayloadSequence(), gotManifest.GetCountsPendingSince())
	}

	gotBatch := next.deltaBatches[0]
	if gotBatch.GetResource().GetFieldsStripped() {
		t.Error("fields_stripped set on a delta batch whose fields the bindings know")
	}
	if gotBatch.GetPayloadSequence() != 5 || gotBatch.GetCountsPendingSince() != 1700000000000 {
		t.Errorf("batch payload_sequence %d, counts_pending_since %d, want 5 and 1700000000000",
			gotBatch.GetPayloadSequence(), gotBatch.GetCountsPendingSince())
	}
}
