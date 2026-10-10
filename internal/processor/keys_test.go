package processor

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"regexp"
	"strings"
	"testing"

	otherlodepb "buf.build/gen/go/otherlode/otherlode/protocolbuffers/go/otherlode/v1"

	"github.com/otherlodehq/otherlode-collector/metrics"
)

// agentKey derives a key the way the agent's BranchKeys.digest does: hex
// of the first 16 bytes of SHA-256 over the tag, class name, method name,
// method descriptor, origin class, condition fingerprint and outcome
// token, joined by NUL. A site key has no outcome token.
func agentKey(tag, class, method, descriptor, origin, fingerprint string, outcome *string) string {
	parts := []string{tag, class, method, descriptor, origin, fingerprint}
	if outcome != nil {
		parts = append(parts, *outcome)
	}
	sum := sha256.Sum256([]byte(strings.Join(parts, "\x00")))
	return hex.EncodeToString(sum[:16])
}

// pricingKey is the branch key the agent sends for one outcome of a
// condition that compares an environment variable with literal.
func pricingKey(literal, outcome string) string {
	fingerprint := `INVOKESTATIC java/lang/System.getenv(Ljava/lang/String;)Ljava/lang/String; LDC "` + literal + `"`
	return agentKey("v1", "com.example.Pricing", "price", "()J", "", fingerprint, &outcome)
}

var hexKey = regexp.MustCompile(`^[0-9a-f]{32}$`)

func int32Ptr(v int32) *int32 { return &v }

func keyedSite(siteKey string, outcomes ...*otherlodepb.BranchOutcome) *otherlodepb.BranchSite {
	return &otherlodepb.BranchSite{SiteKey: ptr(siteKey), Outcomes: outcomes}
}

func TestRedaction_ReKeys_EqualStayEqualAndDifferentStayDifferent(t *testing.T) {
	next := &recordingSink{}
	r := NewRedaction(next, allLiterals(), nil)

	a, b := pricingKey("ENABLE_LEGACY_DISCOUNT", "taken"), pricingKey("ENABLE_LEGACY_DISCOUNT", "fallthrough")
	manifest := &otherlodepb.ProbeManifest{
		Resource: resourceFor("run-1"),
		Probes: []*otherlodepb.ProbeLocation{
			{ClassName: "com.example.Pricing", MethodName: "price", BranchSites: []*otherlodepb.BranchSite{keyedSite("site-a"), keyedSite("site-b")}},
			{ClassName: "com.example.Pricing", Kind: otherlodepb.ProbeKind_BRANCH, BranchKey: ptr(a)},
			{ClassName: "com.example.Pricing", Kind: otherlodepb.ProbeKind_BRANCH, BranchKey: ptr(a)},
			{ClassName: "com.example.Pricing", Kind: otherlodepb.ProbeKind_BRANCH, BranchKey: ptr(b)},
		},
	}
	if err := r.AcceptManifest(context.Background(), manifest); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	baseline := baselineWith(keyedSite("site-a"))
	if err := r.AcceptStaticBaseline(context.Background(), baseline); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	probes := next.manifests[0].GetProbes()
	first, second, third := probes[1].GetBranchKey(), probes[2].GetBranchKey(), probes[3].GetBranchKey()
	for _, k := range []string{first, second, third} {
		if !hexKey.MatchString(k) {
			t.Fatalf("re-keyed branch key %q is not 32 lowercase hex characters", k)
		}
	}
	if first == a || third == b {
		t.Fatal("a branch key reached next unchanged")
	}
	if first != second {
		t.Fatalf("equal keys re-keyed to %q and %q, want them equal", first, second)
	}
	if first == third {
		t.Fatal("different keys re-keyed to the same value")
	}

	sites := probes[0].GetBranchSites()
	siteA, siteB := sites[0].GetSiteKey(), sites[1].GetSiteKey()
	if !hexKey.MatchString(siteA) || siteA == "site-a" || siteA == siteB {
		t.Fatalf("site keys re-keyed to %q and %q, want two different 32-character hex keys", siteA, siteB)
	}
	declared := next.baselines[0].GetDeclaredClasses()[0].GetMethods()[0].GetBranchSites()[0].GetSiteKey()
	if declared != siteA {
		t.Fatalf("the baseline's site key re-keyed to %q, the manifest's to %q; want them equal so the server can join them", declared, siteA)
	}
}

func TestRedaction_ReKeys_GuessAtLiteralNoLongerConfirmed(t *testing.T) {
	next := &recordingSink{}
	r := NewRedaction(next, allLiterals(), nil)

	plain := pricingKey("ENABLE_LEGACY_DISCOUNT", "taken")
	manifest := &otherlodepb.ProbeManifest{
		Resource: resourceFor("run-1"),
		Probes:   []*otherlodepb.ProbeLocation{{ClassName: "com.example.Pricing", Kind: otherlodepb.ProbeKind_BRANCH, BranchKey: ptr(plain)}},
	}
	if err := r.AcceptManifest(context.Background(), manifest); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	sent := next.manifests[0].GetProbes()[0].GetBranchKey()
	if sent == plain {
		t.Fatal("the branch key reached next as the agent's plain digest")
	}
	if guess := pricingKey("ENABLE_LEGACY_DISCOUNT", "taken"); guess == sent {
		t.Fatal("a correct guess at the literal reproduces the forwarded key")
	}
	if want := newRekeyer(testSecret).rekey(plain); sent != want {
		t.Fatalf("forwarded key = %q, want HMAC of the plain key %q", sent, want)
	}
}

func TestRedaction_ReKeys_DependsOnSecret(t *testing.T) {
	key := pricingKey("ENABLE_LEGACY_DISCOUNT", "taken")
	other := []byte(strings.Repeat("z", MinSecretBytes))
	if newRekeyer(testSecret).rekey(key) == newRekeyer(other).rekey(key) {
		t.Fatal("two secrets re-keyed one key to the same value")
	}
}

func TestRedaction_EmptyAndUnsetKeys_StayAsSent(t *testing.T) {
	next := &recordingSink{}
	r := NewRedaction(next, allLiterals(), nil)

	manifest := &otherlodepb.ProbeManifest{
		Resource: resourceFor("run-1"),
		Probes: []*otherlodepb.ProbeLocation{
			{Kind: otherlodepb.ProbeKind_BRANCH, BranchKey: ptr("")},
			{Kind: otherlodepb.ProbeKind_BRANCH},
			{Kind: otherlodepb.ProbeKind_METHOD, BranchSites: []*otherlodepb.BranchSite{keyedSite(""), {}}},
		},
	}
	if err := r.AcceptManifest(context.Background(), manifest); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	probes := next.manifests[0].GetProbes()
	if !probes[0].HasBranchKey() || probes[0].GetBranchKey() != "" {
		t.Fatalf("empty branch key became %q, want it empty", probes[0].GetBranchKey())
	}
	if probes[1].HasBranchKey() {
		t.Fatal("an unset branch key was set")
	}
	sites := probes[2].GetBranchSites()
	if !sites[0].HasSiteKey() || sites[0].GetSiteKey() != "" {
		t.Fatalf("empty site key became %q, want it empty", sites[0].GetSiteKey())
	}
	if sites[1].HasSiteKey() {
		t.Fatal("an unset site key was set")
	}
}

// hashSwitchMethod is one method's sites from a string switch the agent
// could not read back: a switch on the subject's hash code whose case keys
// are the literals' hash codes, an equals check for each literal, and a
// plain switch on an int whose case key 7 matches no literal.
func hashSwitchMethod() []*otherlodepb.BranchSite {
	caseOutcome := func(key int32) *otherlodepb.BranchOutcome {
		return &otherlodepb.BranchOutcome{Role: otherlodepb.BranchRole_CASE, CaseKey: int32Ptr(key)}
	}
	equalsCheck := func(literal string) *otherlodepb.BranchSite {
		return &otherlodepb.BranchSite{
			Condition: []*otherlodepb.ConditionPart{code("mode != "), part(otherlodepb.ConditionPartKind_STRING_LITERAL, literal)},
			Outcomes:  []*otherlodepb.BranchOutcome{{Role: otherlodepb.BranchRole_TAKEN}, {Role: otherlodepb.BranchRole_FALL_THROUGH}},
		}
	}
	return []*otherlodepb.BranchSite{
		{
			Condition: []*otherlodepb.ConditionPart{code("mode.hashCode()")},
			Outcomes: []*otherlodepb.BranchOutcome{
				caseOutcome(javaStringHash("legacy")),
				caseOutcome(javaStringHash("ENABLE_LEGACY_DISCOUNT")),
				{Role: otherlodepb.BranchRole_DEFAULT},
			},
		},
		equalsCheck("legacy"),
		equalsCheck("ENABLE_LEGACY_DISCOUNT"),
		{
			Condition: []*otherlodepb.ConditionPart{code("count")},
			Outcomes:  []*otherlodepb.BranchOutcome{caseOutcome(7), {Role: otherlodepb.BranchRole_DEFAULT}},
		},
	}
}

func TestRedaction_HashCodeCaseKeys_ClearedInManifestAndBaseline(t *testing.T) {
	beforeManifest := metrics.RedactedCaseKeys.Value("manifest")
	beforeBaseline := metrics.RedactedCaseKeys.Value("static_baseline")

	next := &recordingSink{}
	r := NewRedaction(next, allLiterals(), nil)
	if err := r.AcceptManifest(context.Background(), manifestWith(hashSwitchMethod()...)); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if err := r.AcceptStaticBaseline(context.Background(), baselineWith(hashSwitchMethod()...)); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	for name, sites := range map[string][]*otherlodepb.BranchSite{
		"manifest":        next.manifests[0].GetProbes()[0].GetBranchSites(),
		"static baseline": next.baselines[0].GetDeclaredClasses()[0].GetMethods()[0].GetBranchSites(),
	} {
		for i, outcome := range sites[0].GetOutcomes()[:2] {
			if outcome.HasCaseKey() {
				t.Errorf("%s: hash switch case %d kept case key %d, want it cleared", name, i, outcome.GetCaseKey())
			}
			if outcome.GetRole() != otherlodepb.BranchRole_CASE {
				t.Errorf("%s: hash switch case %d role = %v, want CASE", name, i, outcome.GetRole())
			}
		}
		if got := sites[3].GetOutcomes()[0]; !got.HasCaseKey() || got.GetCaseKey() != 7 {
			t.Errorf("%s: int switch case key = %v, want 7 kept", name, got.CaseKey)
		}
	}
	if got := metrics.RedactedCaseKeys.Value("manifest") - beforeManifest; got != 2 {
		t.Errorf("manifest case key counter increased by %d, want 2", got)
	}
	if got := metrics.RedactedCaseKeys.Value("static_baseline") - beforeBaseline; got != 2 {
		t.Errorf("static_baseline case key counter increased by %d, want 2", got)
	}
}

func TestRedaction_HashCodeCaseKeys_KeptWhenLiteralNotRedacted(t *testing.T) {
	next := &recordingSink{}
	r := NewRedaction(next, blocked(t, "^ENABLE_"), nil)
	if err := r.AcceptManifest(context.Background(), manifestWith(hashSwitchMethod()...)); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	outcomes := next.manifests[0].GetProbes()[0].GetBranchSites()[0].GetOutcomes()
	if got := outcomes[0]; !got.HasCaseKey() || got.GetCaseKey() != javaStringHash("legacy") {
		t.Errorf("case key of a literal sent in clear = %v, want it kept", got.CaseKey)
	}
	if outcomes[1].HasCaseKey() {
		t.Errorf("case key of a redacted literal kept as %d, want it cleared", outcomes[1].GetCaseKey())
	}
}

func TestRedaction_HashCodeCaseKeys_OnlyClearedInTheLiteralsMethod(t *testing.T) {
	next := &recordingSink{}
	r := NewRedaction(next, allLiterals(), nil)

	sites := hashSwitchMethod()
	manifest := &otherlodepb.ProbeManifest{
		Resource: resourceFor("run-1"),
		Probes: []*otherlodepb.ProbeLocation{
			{MethodName: "switches", BranchSites: sites[:1]},
			{MethodName: "checks", BranchSites: sites[1:3]},
		},
	}
	if err := r.AcceptManifest(context.Background(), manifest); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	for i, outcome := range next.manifests[0].GetProbes()[0].GetBranchSites()[0].GetOutcomes()[:2] {
		if !outcome.HasCaseKey() {
			t.Errorf("case %d of a method with no literal lost its case key", i)
		}
	}
}

// markedHashSwitchMethod is one method's sites from a newer agent: a
// switch it marks string_hash_code_switch, whose literals are not in the
// payload because the agent could not write the equals checks'
// conditions, and an unmarked switch on an int.
func markedHashSwitchMethod() []*otherlodepb.BranchSite {
	return []*otherlodepb.BranchSite{
		{
			Condition: []*otherlodepb.ConditionPart{code("mode")},
			Outcomes: []*otherlodepb.BranchOutcome{
				{Role: otherlodepb.BranchRole_CASE, CaseKey: int32Ptr(javaStringHash("legacy"))},
				{Role: otherlodepb.BranchRole_CASE, CaseKey: int32Ptr(javaStringHash("ENABLE_LEGACY_DISCOUNT"))},
				{Role: otherlodepb.BranchRole_DEFAULT},
			},
			StringHashCodeSwitch: true,
		},
		{
			Condition: []*otherlodepb.ConditionPart{code("count")},
			Outcomes: []*otherlodepb.BranchOutcome{
				{Role: otherlodepb.BranchRole_CASE, CaseKey: int32Ptr(7)},
				{Role: otherlodepb.BranchRole_DEFAULT},
			},
		},
	}
}

func TestRedaction_MarkedHashCodeSwitch_CaseKeysClearedWithoutLiterals(t *testing.T) {
	beforeManifest := metrics.RedactedCaseKeys.Value("manifest")
	beforeBaseline := metrics.RedactedCaseKeys.Value("static_baseline")

	next := &recordingSink{}
	r := NewRedaction(next, allLiterals(), nil)
	if err := r.AcceptManifest(context.Background(), manifestWith(markedHashSwitchMethod()...)); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if err := r.AcceptStaticBaseline(context.Background(), baselineWith(markedHashSwitchMethod()...)); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	for name, sites := range map[string][]*otherlodepb.BranchSite{
		"manifest":        next.manifests[0].GetProbes()[0].GetBranchSites(),
		"static baseline": next.baselines[0].GetDeclaredClasses()[0].GetMethods()[0].GetBranchSites(),
	} {
		if !sites[0].GetStringHashCodeSwitch() {
			t.Errorf("%s: the mark was dropped, want it forwarded", name)
		}
		for i, outcome := range sites[0].GetOutcomes()[:2] {
			if outcome.HasCaseKey() {
				t.Errorf("%s: marked switch case %d kept case key %d, want it cleared", name, i, outcome.GetCaseKey())
			}
		}
		if got := sites[1].GetOutcomes()[0]; !got.HasCaseKey() || got.GetCaseKey() != 7 {
			t.Errorf("%s: int switch case key = %v, want 7 kept", name, got.CaseKey)
		}
	}
	if got := metrics.RedactedCaseKeys.Value("manifest") - beforeManifest; got != 2 {
		t.Errorf("manifest case key counter increased by %d, want 2", got)
	}
	if got := metrics.RedactedCaseKeys.Value("static_baseline") - beforeBaseline; got != 2 {
		t.Errorf("static_baseline case key counter increased by %d, want 2", got)
	}
	if next.manifests[0].GetResource().GetFieldsStripped() || next.baselines[0].GetResource().GetFieldsStripped() {
		t.Error("fields_stripped set, but the bindings know string_hash_code_switch")
	}
}

// A blocked pattern that matches no literal still clears a marked
// switch's case keys: the collector cannot tell which literal a key
// hashes, so it cannot tell whether the pattern would have blocked it.
func TestRedaction_MarkedHashCodeSwitch_ClearedWhenNoLiteralIsBlocked(t *testing.T) {
	next := &recordingSink{}
	r := NewRedaction(next, blocked(t, "^NO_SUCH_LITERAL$"), nil)
	if err := r.AcceptManifest(context.Background(), manifestWith(markedHashSwitchMethod()...)); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	sites := next.manifests[0].GetProbes()[0].GetBranchSites()
	for i, outcome := range sites[0].GetOutcomes()[:2] {
		if outcome.HasCaseKey() {
			t.Errorf("marked switch case %d kept case key %d, want it cleared", i, outcome.GetCaseKey())
		}
	}
	if got := sites[1].GetOutcomes()[0]; !got.HasCaseKey() || got.GetCaseKey() != 7 {
		t.Errorf("int switch case key = %v, want 7 kept", got.CaseKey)
	}
}

// An agent that does not send the mark still gets the hash-code rule, and
// a marked switch in one method leaves another method's int switch alone.
func TestRedaction_MarkedAndUnmarkedMethods_EachRuleApplies(t *testing.T) {
	next := &recordingSink{}
	r := NewRedaction(next, allLiterals(), nil)
	manifest := &otherlodepb.ProbeManifest{
		Resource: resourceFor("run-1"),
		Probes: []*otherlodepb.ProbeLocation{
			{MethodName: "older", BranchSites: hashSwitchMethod()},
			{MethodName: "newer", BranchSites: markedHashSwitchMethod()},
		},
	}
	if err := r.AcceptManifest(context.Background(), manifest); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	probes := next.manifests[0].GetProbes()
	for _, probe := range probes {
		sites := probe.GetBranchSites()
		for i, outcome := range sites[0].GetOutcomes()[:2] {
			if outcome.HasCaseKey() {
				t.Errorf("%s: hash switch case %d kept case key %d, want it cleared", probe.GetMethodName(), i, outcome.GetCaseKey())
			}
		}
		intSwitch := sites[len(sites)-1].GetOutcomes()[0]
		if !intSwitch.HasCaseKey() || intSwitch.GetCaseKey() != 7 {
			t.Errorf("%s: int switch case key = %v, want 7 kept", probe.GetMethodName(), intSwitch.CaseKey)
		}
	}
}

func TestJavaStringHash_MatchesJava(t *testing.T) {
	// Values printed by Java's String.hashCode().
	for s, want := range map[string]int32{
		"":                       0,
		"hello":                  99162322,
		"polygenelubricants":     -2147483648,
		"\U0001F600":             1772899,
		"ENABLE_LEGACY_DISCOUNT": -1896388037,
		"café":                   3045921,
		"Aa":                     2112,
		"BB":                     2112,
	} {
		if got := javaStringHash(s); got != want {
			t.Errorf("javaStringHash(%q) = %d, want %d", s, got, want)
		}
	}
}

func TestSecretFingerprint(t *testing.T) {
	got := SecretFingerprint(testSecret)
	if !regexp.MustCompile(`^[0-9a-f]{16}$`).MatchString(got) {
		t.Fatalf("fingerprint %q is not 16 lowercase hex characters", got)
	}
	if got != SecretFingerprint(append([]byte(nil), testSecret...)) {
		t.Fatal("one secret gave two fingerprints")
	}
	if got == SecretFingerprint([]byte(strings.Repeat("z", MinSecretBytes))) {
		t.Fatal("two secrets gave one fingerprint")
	}
	if strings.Contains(string(testSecret), got) || strings.Contains(hex.EncodeToString(testSecret), got) {
		t.Fatal("the fingerprint holds part of the secret")
	}
}

func TestCheckSecret(t *testing.T) {
	cases := map[string]struct {
		secret  string
		wantErr string
	}{
		"exactly the minimum": {secret: strings.Repeat("a", MinSecretBytes)},
		"one byte short":      {secret: strings.Repeat("a", MinSecretBytes-1), wantErr: "31 bytes, want at least 32"},
		"empty":               {secret: "", wantErr: "0 bytes"},
		"control character":   {secret: strings.Repeat("a", MinSecretBytes) + "\x00", wantErr: "control character at byte 32"},
		"delete character":    {secret: "\x7f" + strings.Repeat("a", MinSecretBytes), wantErr: "control character at byte 0"},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			err := CheckSecret([]byte(c.secret))
			if c.wantErr == "" {
				if err != nil {
					t.Fatalf("unexpected error: %v", err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), c.wantErr) {
				t.Fatalf("error = %v, want one containing %q", err, c.wantErr)
			}
			if c.secret != "" && strings.Contains(err.Error(), c.secret) {
				t.Fatalf("error %q quotes the secret", err)
			}
		})
	}
}

func TestRedactionConfig_Validate(t *testing.T) {
	if err := (RedactionConfig{}).Validate(); err != nil {
		t.Fatalf("zero config: unexpected error: %v", err)
	}
	if err := allLiterals().Validate(); err != nil {
		t.Fatalf("enabled config with a secret: unexpected error: %v", err)
	}
	if err := (RedactionConfig{AllLiterals: true}).Validate(); err == nil {
		t.Fatal("enabled config with no secret passed, want an error")
	}
	if err := (RedactionConfig{AllLiterals: true, Secret: []byte("short")}).Validate(); err == nil {
		t.Fatal("enabled config with a short secret passed, want an error")
	}
	if err := (RedactionConfig{Secret: testSecret}).Validate(); err == nil {
		t.Fatal("a secret with redaction off passed, want an error")
	}
}

func TestNewRedaction_NoSecret_Panics(t *testing.T) {
	defer func() {
		if recover() == nil {
			t.Fatal("NewRedaction with no secret did not panic")
		}
	}()
	NewRedaction(&recordingSink{}, RedactionConfig{AllLiterals: true}, nil)
}
