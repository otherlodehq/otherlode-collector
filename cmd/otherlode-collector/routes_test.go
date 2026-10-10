package main

import (
	"bytes"
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strings"
	"testing"
	"time"

	"golang.org/x/time/rate"
	"google.golang.org/protobuf/encoding/protowire"
	"google.golang.org/protobuf/proto"

	otherlodepb "buf.build/gen/go/otherlode/otherlode/protocolbuffers/go/otherlode/v1"

	"github.com/otherlodehq/otherlode-collector/internal/auth"
	"github.com/otherlodehq/otherlode-collector/internal/forward"
	"github.com/otherlodehq/otherlode-collector/internal/processor"
	"github.com/otherlodehq/otherlode-collector/internal/ratelimit"
	"github.com/otherlodehq/otherlode-collector/internal/tokenfile"
	"github.com/otherlodehq/otherlode-collector/metrics"
)

// deltaRequest builds a POST to the deltas route carrying the smallest
// batch the handler accepts: a resource with a service name, instance ID
// and run ID, and no deltas.
func deltaRequest(t *testing.T, serverURL string) *http.Request {
	t.Helper()
	body, err := proto.Marshal(&otherlodepb.DeltaBatch{
		Resource: &otherlodepb.ResourceAttributes{ServiceName: "demo-service", ServiceInstanceId: "instance-1", RunId: "run-1"},
	})
	if err != nil {
		t.Fatalf("marshal batch: %v", err)
	}
	req, err := http.NewRequest(http.MethodPost, serverURL+"/v1/otherlode/deltas", bytes.NewReader(body))
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	req.Header.Set("Content-Type", "application/x-protobuf")
	return req
}

// staticBaselineRequest builds a POST to the static-baseline route
// carrying the smallest baseline the handler accepts: identity, a
// scanned_at, and a single chunk.
func staticBaselineRequest(t *testing.T, serverURL string) *http.Request {
	t.Helper()
	body, err := proto.Marshal(&otherlodepb.StaticBaseline{
		Resource:   &otherlodepb.ResourceAttributes{ServiceName: "demo-service", ServiceInstanceId: "instance-1", RunId: "run-1"},
		ScannedAt:  1700000000,
		ChunkCount: 1,
	})
	if err != nil {
		t.Fatalf("marshal baseline: %v", err)
	}
	req, err := http.NewRequest(http.MethodPost, serverURL+"/v1/otherlode/static-baseline", bytes.NewReader(body))
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	req.Header.Set("Content-Type", "application/x-protobuf")
	return req
}

func mustRegisterRoutes(t *testing.T, mux *http.ServeMux, cfg routeConfig) *forward.ForwardingSink {
	t.Helper()
	fwd, err := registerRoutes(mux, cfg)
	if err != nil {
		t.Fatalf("registerRoutes: %v", err)
	}
	return fwd
}

func tokenSet(token string) *auth.TokenSet {
	return auth.NewTokenSet([]string{token})
}

func TestRegisterRoutes_AuthTokenSet_RequiresMatchingHeader(t *testing.T) {
	mux := http.NewServeMux()
	mustRegisterRoutes(t, mux, routeConfig{AuthTokens: tokenSet("s3cret")})
	server := httptest.NewServer(mux)
	defer server.Close()

	req := deltaRequest(t, server.URL)

	resp, err := server.Client().Do(req)
	if err != nil {
		t.Fatalf("post without auth: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("status without auth = %d, want %d", resp.StatusCode, http.StatusUnauthorized)
	}

	req2 := deltaRequest(t, server.URL)
	req2.Header.Set("Authorization", "Bearer s3cret")

	resp2, err := server.Client().Do(req2)
	if err != nil {
		t.Fatalf("post with auth: %v", err)
	}
	defer resp2.Body.Close()
	if resp2.StatusCode != http.StatusAccepted {
		t.Fatalf("status with auth = %d, want %d", resp2.StatusCode, http.StatusAccepted)
	}
}

func TestRegisterRoutes_AuthTokenSet_StaticBaselineRequiresMatchingHeader(t *testing.T) {
	mux := http.NewServeMux()
	mustRegisterRoutes(t, mux, routeConfig{AuthTokens: tokenSet("s3cret")})
	server := httptest.NewServer(mux)
	defer server.Close()

	resp, err := server.Client().Do(staticBaselineRequest(t, server.URL))
	if err != nil {
		t.Fatalf("post without auth: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("status without auth = %d, want %d", resp.StatusCode, http.StatusUnauthorized)
	}

	req2 := staticBaselineRequest(t, server.URL)
	req2.Header.Set("Authorization", "Bearer s3cret")

	resp2, err := server.Client().Do(req2)
	if err != nil {
		t.Fatalf("post with auth: %v", err)
	}
	defer resp2.Body.Close()
	if resp2.StatusCode != http.StatusAccepted {
		t.Fatalf("status with auth = %d, want %d", resp2.StatusCode, http.StatusAccepted)
	}
}

func TestRegisterRoutes_Healthz_NeverRequiresAuth(t *testing.T) {
	mux := http.NewServeMux()
	mustRegisterRoutes(t, mux, routeConfig{AuthTokens: tokenSet("s3cret")})
	server := httptest.NewServer(mux)
	defer server.Close()

	resp, err := http.Get(server.URL + "/healthz")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want %d", resp.StatusCode, http.StatusOK)
	}
}

func postDelta(t *testing.T, serverURL string) *http.Response {
	t.Helper()
	resp, err := http.DefaultClient.Do(deltaRequest(t, serverURL))
	if err != nil {
		t.Fatalf("post: %v", err)
	}
	return resp
}

func TestRegisterRoutes_LimiterSet_ThrottlesIngestRoutesOverBurst(t *testing.T) {
	limiter := ratelimit.New(rate.Limit(1), 1)
	defer limiter.Stop()

	mux := http.NewServeMux()
	mustRegisterRoutes(t, mux, routeConfig{Limiter: limiter})
	server := httptest.NewServer(mux)
	defer server.Close()

	resp1 := postDelta(t, server.URL)
	resp1.Body.Close()
	if resp1.StatusCode != http.StatusAccepted {
		t.Fatalf("first request status = %d, want %d", resp1.StatusCode, http.StatusAccepted)
	}

	resp2 := postDelta(t, server.URL)
	defer resp2.Body.Close()
	if resp2.StatusCode != http.StatusTooManyRequests {
		t.Fatalf("second request status = %d, want %d", resp2.StatusCode, http.StatusTooManyRequests)
	}
}

func TestRegisterRoutes_LimiterSet_HealthzNeverThrottled(t *testing.T) {
	limiter := ratelimit.New(rate.Limit(1), 1)
	defer limiter.Stop()

	mux := http.NewServeMux()
	mustRegisterRoutes(t, mux, routeConfig{Limiter: limiter})
	server := httptest.NewServer(mux)
	defer server.Close()

	for i := 0; i < 3; i++ {
		resp, err := http.Get(server.URL + "/healthz")
		if err != nil {
			t.Fatalf("get: %v", err)
		}
		resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("request %d: status = %d, want %d", i, resp.StatusCode, http.StatusOK)
		}
	}
}

func TestRegisterRoutes_NoLimiter_IngestUnthrottled(t *testing.T) {
	mux := http.NewServeMux()
	mustRegisterRoutes(t, mux, routeConfig{})
	server := httptest.NewServer(mux)
	defer server.Close()

	for i := 0; i < 5; i++ {
		resp := postDelta(t, server.URL)
		resp.Body.Close()
		if resp.StatusCode != http.StatusAccepted {
			t.Fatalf("request %d: status = %d, want %d", i, resp.StatusCode, http.StatusAccepted)
		}
	}
}

func TestRegisterRoutes_NoForwardURL_ReturnsNilForwardingSink(t *testing.T) {
	mux := http.NewServeMux()
	fwd := mustRegisterRoutes(t, mux, routeConfig{})
	if fwd != nil {
		t.Fatalf("forwarding sink = %v, want nil when OTHERLODE_COLLECTOR_FORWARD_URL is unset", fwd)
	}
}

func TestRegisterRoutes_ForwardURLSet_ReturnsForwardingSinkAndReachesAcceptedStatus(t *testing.T) {
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer backend.Close()

	mux := http.NewServeMux()
	fwd := mustRegisterRoutes(t, mux, routeConfig{Forward: forward.Config{URL: backend.URL, AuthToken: forward.StaticToken("backend-secret")}})
	if fwd == nil {
		t.Fatal("forwarding sink = nil, want non-nil when OTHERLODE_COLLECTOR_FORWARD_URL is set")
	}
	defer fwd.Shutdown(context.Background())

	server := httptest.NewServer(mux)
	defer server.Close()

	resp := postDelta(t, server.URL)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusAccepted {
		t.Fatalf("status = %d, want %d", resp.StatusCode, http.StatusAccepted)
	}
}

func TestRegisterRoutes_InvalidForwardURL_ReturnsError(t *testing.T) {
	mux := http.NewServeMux()
	if _, err := registerRoutes(mux, routeConfig{Forward: forward.Config{URL: "not a url"}}); err == nil {
		t.Fatal("expected an error for an unusable forward URL, got nil")
	}
}

func TestRegisterRoutes_Metrics_CountsAcceptedIngest(t *testing.T) {
	mux := http.NewServeMux()
	mustRegisterRoutes(t, mux, routeConfig{})
	server := httptest.NewServer(mux)
	defer server.Close()

	resp := postDelta(t, server.URL)
	resp.Body.Close()
	if resp.StatusCode != http.StatusAccepted {
		t.Fatalf("post status = %d, want %d", resp.StatusCode, http.StatusAccepted)
	}

	metricsResp, err := http.Get(server.URL + "/metrics")
	if err != nil {
		t.Fatalf("get metrics: %v", err)
	}
	defer metricsResp.Body.Close()
	body, _ := io.ReadAll(metricsResp.Body)
	if metricsResp.StatusCode != http.StatusOK {
		t.Fatalf("metrics status = %d, want %d", metricsResp.StatusCode, http.StatusOK)
	}
	if !strings.Contains(string(body), `otherlode_collector_ingest_accepted_total{payload="deltas"} `) {
		t.Fatalf("metrics output missing the accepted-deltas series:\n%s", body)
	}
}

// forwardDelta posts a delta batch carrying res to a collector wired
// with envCfg and nsCfg, and returns the batch its backend received.
func forwardDelta(t *testing.T, envCfg processor.EnvironmentConfig, nsCfg processor.NamespaceConfig, res *otherlodepb.ResourceAttributes) *otherlodepb.DeltaBatch {
	t.Helper()
	received := make(chan *otherlodepb.DeltaBatch, 1)
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(r.Body)
		if err != nil {
			t.Errorf("read forwarded body: %v", err)
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		var batch otherlodepb.DeltaBatch
		if err := proto.Unmarshal(body, &batch); err != nil {
			t.Errorf("unmarshal forwarded body: %v", err)
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		received <- &batch
		w.WriteHeader(http.StatusOK)
	}))
	defer backend.Close()

	mux := http.NewServeMux()
	fwd := mustRegisterRoutes(t, mux, routeConfig{Forward: forward.Config{URL: backend.URL}, Environment: envCfg, Namespace: nsCfg})
	defer fwd.Shutdown(context.Background())

	server := httptest.NewServer(mux)
	defer server.Close()

	body, err := proto.Marshal(&otherlodepb.DeltaBatch{Resource: res})
	if err != nil {
		t.Fatalf("marshal batch: %v", err)
	}
	resp, err := http.Post(server.URL+"/v1/otherlode/deltas", "application/x-protobuf", bytes.NewReader(body))
	if err != nil {
		t.Fatalf("post: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusAccepted {
		t.Fatalf("status = %d, want %d", resp.StatusCode, http.StatusAccepted)
	}

	select {
	case batch := <-received:
		return batch
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for the backend to receive the forwarded batch")
		return nil
	}
}

// demoResource is the smallest resource the handler accepts. It names a
// namespace only when ns is non-empty.
func demoResource(ns string) *otherlodepb.ResourceAttributes {
	res := &otherlodepb.ResourceAttributes{ServiceName: "demo-service", ServiceInstanceId: "instance-1", RunId: "run-1"}
	if ns != "" {
		res.SetServiceNamespace(ns)
	}
	return res
}

func TestRegisterRoutes_EnvironmentConfigSet_StampsForwardedDeltaBatch(t *testing.T) {
	batch := forwardDelta(t, processor.EnvironmentConfig{Value: "uat"}, processor.NamespaceConfig{}, demoResource(""))
	if got := batch.GetResource().GetEnvironment(); got != "uat" {
		t.Fatalf("forwarded batch environment = %q, want %q", got, "uat")
	}
}

func TestRegisterRoutes_NamespaceConfigSet_StampsForwardedDeltaBatch(t *testing.T) {
	batch := forwardDelta(t, processor.EnvironmentConfig{}, processor.NamespaceConfig{Value: "team-a"}, demoResource(""))
	if got := batch.GetResource().GetServiceNamespace(); got != "team-a" {
		t.Fatalf("forwarded batch namespace = %q, want %q", got, "team-a")
	}
}

func TestRegisterRoutes_NamespaceUpsert_ReplacesAgentNamespace(t *testing.T) {
	nsCfg := processor.NamespaceConfig{Value: "team-a", Action: processor.Upsert}
	batch := forwardDelta(t, processor.EnvironmentConfig{}, nsCfg, demoResource("team-b"))
	if got := batch.GetResource().GetServiceNamespace(); got != "team-a" {
		t.Fatalf("forwarded batch namespace = %q, want %q", got, "team-a")
	}
}

func TestRegisterRoutes_NamespaceConfigBlank_PassesAgentNamespaceThrough(t *testing.T) {
	for _, agent := range []string{"", "team-b"} {
		batch := forwardDelta(t, processor.EnvironmentConfig{}, processor.NamespaceConfig{}, demoResource(agent))
		if got := batch.GetResource().GetServiceNamespace(); got != agent {
			t.Errorf("agent namespace %q: forwarded namespace = %q, want it unchanged", agent, got)
		}
		if agent == "" && batch.GetResource().HasServiceNamespace() {
			t.Errorf("agent sent no namespace, but the forwarded batch has one set")
		}
	}
}

func TestRegisterRoutes_BothProcessorsSet_StampBothFields(t *testing.T) {
	batch := forwardDelta(t, processor.EnvironmentConfig{Value: "uat"}, processor.NamespaceConfig{Value: "team-a"}, demoResource(""))
	if got := batch.GetResource().GetEnvironment(); got != "uat" {
		t.Fatalf("forwarded batch environment = %q, want %q", got, "uat")
	}
	if got := batch.GetResource().GetServiceNamespace(); got != "team-a" {
		t.Fatalf("forwarded batch namespace = %q, want %q", got, "team-a")
	}
}

// manifestWithLiteral is the smallest manifest the handler accepts, with
// one probe whose branch site tests a string literal. Its top-level
// message and its branch site each carry a field no schema version
// declares. It comes from a test run, so a test can check that the flag
// survives redaction.
func manifestWithLiteral() *otherlodepb.ProbeManifest {
	unknown := protowire.AppendString(protowire.AppendTag(nil, 9999, protowire.BytesType), "a newer literal")
	site := &otherlodepb.BranchSite{
		SiteKey: proto.String(testSiteKey),
		Condition: []*otherlodepb.ConditionPart{
			{Kind: otherlodepb.ConditionPartKind_CODE, Text: "System.getenv("},
			{Kind: otherlodepb.ConditionPartKind_STRING_LITERAL, Text: "ENABLE_LEGACY_DISCOUNT"},
			{Kind: otherlodepb.ConditionPartKind_CODE, Text: ")"},
		},
	}
	site.ProtoReflect().SetUnknown(unknown)
	// A switch on the literal's String.hashCode(), as the agent sends one
	// it could not read back.
	hashSwitch := &otherlodepb.BranchSite{Outcomes: []*otherlodepb.BranchOutcome{
		{Role: otherlodepb.BranchRole_CASE, CaseKey: proto.Int32(testLiteralHash)},
		{Role: otherlodepb.BranchRole_DEFAULT},
	}}
	// A switch the agent marks as a switch on String.hashCode(), whose
	// literal is not in the payload.
	markedSwitch := &otherlodepb.BranchSite{StringHashCodeSwitch: true, Outcomes: []*otherlodepb.BranchOutcome{
		{Role: otherlodepb.BranchRole_CASE, CaseKey: proto.Int32(testUnsentLiteralHash)},
		{Role: otherlodepb.BranchRole_DEFAULT},
	}}
	manifest := &otherlodepb.ProbeManifest{
		Resource: &otherlodepb.ResourceAttributes{ServiceName: "demo-service", ServiceInstanceId: "instance-1", RunId: "run-1", TestRun: true},
		Probes: []*otherlodepb.ProbeLocation{
			{ClassName: "com.example.Pricing", MethodName: "price", BranchSites: []*otherlodepb.BranchSite{site, hashSwitch, markedSwitch}},
			{ClassName: "com.example.Pricing", MethodName: "price", Kind: otherlodepb.ProbeKind_BRANCH, BranchKey: proto.String(testBranchKey)},
		},
	}
	manifest.ProtoReflect().SetUnknown(unknown)
	return manifest
}

const (
	// testBranchKey and testSiteKey have the shape of the agent's keys.
	testBranchKey = "0123456789abcdef0123456789abcdef"
	testSiteKey   = "fedcba9876543210fedcba9876543210"
	// testLiteralHash is Java's "ENABLE_LEGACY_DISCOUNT".hashCode().
	testLiteralHash = -1896388037
	// testUnsentLiteralHash is Java's "legacy".hashCode(), a literal the
	// manifest does not carry.
	testUnsentLiteralHash = -1106578487
)

// forwardManifest posts manifestWithLiteral to a collector wired with
// redactCfg and returns the manifest its backend received.
func forwardManifest(t *testing.T, redactCfg processor.RedactionConfig) *otherlodepb.ProbeManifest {
	t.Helper()
	return forwardManifestLogging(t, redactCfg, nil)
}

// forwardManifestLogging is forwardManifest with the collector logging to
// logger, or to slog.Default() when logger is nil.
func forwardManifestLogging(t *testing.T, redactCfg processor.RedactionConfig, logger *slog.Logger) *otherlodepb.ProbeManifest {
	t.Helper()
	received := make(chan *otherlodepb.ProbeManifest, 1)
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(r.Body)
		if err != nil {
			t.Errorf("read forwarded body: %v", err)
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		var manifest otherlodepb.ProbeManifest
		if err := proto.Unmarshal(body, &manifest); err != nil {
			t.Errorf("unmarshal forwarded body: %v", err)
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		received <- &manifest
		w.WriteHeader(http.StatusOK)
	}))
	defer backend.Close()

	mux := http.NewServeMux()
	fwd := mustRegisterRoutes(t, mux, routeConfig{Logger: logger, Forward: forward.Config{URL: backend.URL}, Redaction: redactCfg})
	defer fwd.Shutdown(context.Background())

	server := httptest.NewServer(mux)
	defer server.Close()

	body, err := proto.Marshal(manifestWithLiteral())
	if err != nil {
		t.Fatalf("marshal manifest: %v", err)
	}
	resp, err := http.Post(server.URL+"/v1/otherlode/manifest", "application/x-protobuf", bytes.NewReader(body))
	if err != nil {
		t.Fatalf("post: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusAccepted {
		t.Fatalf("status = %d, want %d", resp.StatusCode, http.StatusAccepted)
	}

	select {
	case manifest := <-received:
		return manifest
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for the backend to receive the forwarded manifest")
		return nil
	}
}

func TestRegisterRoutes_RedactionOn_ForwardsManifestRedactedWithoutUnknownFields(t *testing.T) {
	manifest := forwardManifest(t, processor.RedactionConfig{BlockedValues: []*regexp.Regexp{regexp.MustCompile("LEGACY")}, Secret: []byte(testRedactSecret)})

	site := manifest.GetProbes()[0].GetBranchSites()[0]
	if got := site.GetCondition()[1].GetText(); got != processor.RedactedText {
		t.Fatalf("forwarded literal = %q, want %q", got, processor.RedactedText)
	}
	if got := site.GetCondition()[0].GetText(); got != "System.getenv(" {
		t.Fatalf("forwarded code part = %q, want it unchanged", got)
	}
	if n := len(manifest.ProtoReflect().GetUnknown()); n != 0 {
		t.Fatalf("forwarded manifest has %d unknown bytes, want 0", n)
	}
	if !manifest.GetResource().GetTestRun() {
		t.Fatal("forwarded manifest lost test_run, a field these bindings know")
	}
	if n := len(site.ProtoReflect().GetUnknown()); n != 0 {
		t.Fatalf("forwarded branch site has %d unknown bytes, want 0", n)
	}
}

func TestRegisterRoutes_RedactionOn_ForwardsReKeyedKeysAndNoHashCodeCaseKeys(t *testing.T) {
	manifest := forwardManifest(t, processor.RedactionConfig{AllLiterals: true, Secret: []byte(testRedactSecret)})

	hexKey := regexp.MustCompile(`^[0-9a-f]{32}$`)
	branchKey := manifest.GetProbes()[1].GetBranchKey()
	if branchKey == testBranchKey || !hexKey.MatchString(branchKey) {
		t.Fatalf("forwarded branch key = %q, want a re-keyed 32-character hex key", branchKey)
	}
	sites := manifest.GetProbes()[0].GetBranchSites()
	if siteKey := sites[0].GetSiteKey(); siteKey == testSiteKey || !hexKey.MatchString(siteKey) {
		t.Fatalf("forwarded site key = %q, want a re-keyed 32-character hex key", siteKey)
	}
	if outcome := sites[1].GetOutcomes()[0]; outcome.HasCaseKey() {
		t.Fatalf("forwarded the redacted literal's hash code %d as a case key", outcome.GetCaseKey())
	}
	if outcome := sites[2].GetOutcomes()[0]; outcome.HasCaseKey() {
		t.Fatalf("forwarded case key %d of a marked hash code switch", outcome.GetCaseKey())
	}
}

func TestRegisterRoutes_RedactionOn_LogsSecretFingerprintNeverSecret(t *testing.T) {
	var logs bytes.Buffer
	logger := slog.New(slog.NewJSONHandler(&logs, &slog.HandlerOptions{Level: slog.LevelDebug}))
	secret := []byte(testRedactSecret)

	forwardManifestLogging(t, processor.RedactionConfig{AllLiterals: true, Secret: secret}, logger)

	out := logs.String()
	if want := `"secret_fingerprint":"` + processor.SecretFingerprint(secret) + `"`; !strings.Contains(out, want) {
		t.Fatalf("logs do not hold %s:\n%s", want, out)
	}
	if strings.Contains(out, testRedactSecret) {
		t.Fatalf("logs hold the redaction secret:\n%s", out)
	}
}

func TestRegisterRoutes_RedactionWithoutValidSecret_ReturnsError(t *testing.T) {
	for name, cfg := range map[string]processor.RedactionConfig{
		"no secret":            {AllLiterals: true},
		"short secret":         {AllLiterals: true, Secret: []byte("short")},
		"secret, no redacting": {Secret: []byte(testRedactSecret)},
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := registerRoutes(http.NewServeMux(), routeConfig{Redaction: cfg}); err == nil {
				t.Fatal("registerRoutes returned no error")
			}
		})
	}
}

func TestRegisterRoutes_RedactionOff_ForwardsManifestWithLiteralAndUnknownFields(t *testing.T) {
	manifest := forwardManifest(t, processor.RedactionConfig{})

	if got := manifest.GetProbes()[1].GetBranchKey(); got != testBranchKey {
		t.Fatalf("forwarded branch key = %q, want it unchanged", got)
	}
	sites := manifest.GetProbes()[0].GetBranchSites()
	if got := sites[0].GetSiteKey(); got != testSiteKey {
		t.Fatalf("forwarded site key = %q, want it unchanged", got)
	}
	if got := sites[1].GetOutcomes()[0]; !got.HasCaseKey() || got.GetCaseKey() != testLiteralHash {
		t.Fatalf("forwarded case key = %v, want it unchanged", got.CaseKey)
	}
	if got := sites[2].GetOutcomes()[0]; !got.HasCaseKey() || got.GetCaseKey() != testUnsentLiteralHash {
		t.Fatalf("forwarded case key of a marked switch = %v, want it unchanged", got.CaseKey)
	}

	site := manifest.GetProbes()[0].GetBranchSites()[0]
	if got := site.GetCondition()[1].GetText(); got != "ENABLE_LEGACY_DISCOUNT" {
		t.Fatalf("forwarded literal = %q, want it unchanged", got)
	}
	if len(manifest.ProtoReflect().GetUnknown()) == 0 {
		t.Fatal("forwarded manifest lost its unknown field, want it passed through")
	}
	if len(site.ProtoReflect().GetUnknown()) == 0 {
		t.Fatal("forwarded branch site lost its unknown field, want it passed through")
	}
}

func waitUntil(t *testing.T, timeout time.Duration, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(time.Millisecond)
	}
	if !cond() {
		t.Fatal("condition not met before timeout")
	}
}

// watchFile re-reads file every few milliseconds until the test ends.
// Cleanup waits for the watcher to stop, so no late re-read reaches the
// reload counter while another test reads it.
func watchFile(t *testing.T, file *tokenfile.File) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		file.Watch(ctx, 5*time.Millisecond, slog.New(slog.NewTextHandler(io.Discard, nil)))
		close(done)
	}()
	t.Cleanup(func() {
		cancel()
		<-done
	})
}

// deltaStatus posts a delta batch with the given bearer token, or with
// no Authorization header when token is empty, and returns the status.
func deltaStatus(t *testing.T, serverURL, token string) int {
	t.Helper()
	req := deltaRequest(t, serverURL)
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("post: %v", err)
	}
	resp.Body.Close()
	return resp.StatusCode
}

func TestRegisterRoutes_TokenList_AnyListedTokenPasses(t *testing.T) {
	tokens, _, err := resolveAuthTokens(envFrom(map[string]string{"OTHERLODE_COLLECTOR_AUTH_TOKEN": "old-token, new-token"}))
	if err != nil {
		t.Fatalf("resolveAuthTokens: %v", err)
	}
	mux := http.NewServeMux()
	if _, err := registerRoutes(mux, routeConfig{AuthTokens: tokens}); err != nil {
		t.Fatalf("registerRoutes: %v", err)
	}
	server := httptest.NewServer(mux)
	defer server.Close()

	for token, want := range map[string]int{
		"old-token":   http.StatusAccepted,
		"new-token":   http.StatusAccepted,
		"other-token": http.StatusUnauthorized,
		"":            http.StatusUnauthorized,
	} {
		if got := deltaStatus(t, server.URL, token); got != want {
			t.Errorf("token %q: status = %d, want %d", token, got, want)
		}
	}
}

func TestRegisterRoutes_AuthTokenFile_ReReadChangesAcceptedTokens(t *testing.T) {
	path := writeTokenFile(t, "old-token\n")
	tokens, file, err := resolveAuthTokens(envFrom(map[string]string{"OTHERLODE_COLLECTOR_AUTH_TOKEN_FILE": path}))
	if err != nil {
		t.Fatalf("resolveAuthTokens: %v", err)
	}
	mux := http.NewServeMux()
	if _, err := registerRoutes(mux, routeConfig{AuthTokens: tokens}); err != nil {
		t.Fatalf("registerRoutes: %v", err)
	}
	server := httptest.NewServer(mux)
	defer server.Close()

	watchFile(t, file)

	if got := deltaStatus(t, server.URL, "old-token"); got != http.StatusAccepted {
		t.Fatalf("old token before the change: status = %d, want %d", got, http.StatusAccepted)
	}

	replaceTokenFile(t, path, "new-token\n")
	waitUntil(t, 2*time.Second, func() bool { return tokens.Matches("new-token") })
	if got := deltaStatus(t, server.URL, "new-token"); got != http.StatusAccepted {
		t.Fatalf("new token after the change: status = %d, want %d", got, http.StatusAccepted)
	}
	if got := deltaStatus(t, server.URL, "old-token"); got != http.StatusUnauthorized {
		t.Fatalf("old token after the change: status = %d, want %d", got, http.StatusUnauthorized)
	}

	failuresBefore := metrics.TokenReloadFailures.Value("auth")
	replaceTokenFile(t, path, "")
	waitUntil(t, 2*time.Second, func() bool { return metrics.TokenReloadFailures.Value("auth") > failuresBefore })
	if got := deltaStatus(t, server.URL, "new-token"); got != http.StatusAccepted {
		t.Fatalf("new token after the file was emptied: status = %d, want %d", got, http.StatusAccepted)
	}
}

func TestRegisterRoutes_ForwardTokenFile_NextRequestSendsReloadedKey(t *testing.T) {
	auths := make(chan string, 8)
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		auths <- r.Header.Get("Authorization")
		w.WriteHeader(http.StatusOK)
	}))
	defer backend.Close()

	path := writeTokenFile(t, "key-1\n")
	cfg, file, err := resolveForwardConfig(envFrom(map[string]string{
		"OTHERLODE_COLLECTOR_FORWARD_URL":             backend.URL,
		"OTHERLODE_COLLECTOR_FORWARD_AUTH_TOKEN_FILE": path,
	}))
	if err != nil {
		t.Fatalf("resolveForwardConfig: %v", err)
	}
	mux := http.NewServeMux()
	fwd, err := registerRoutes(mux, routeConfig{Forward: cfg})
	if err != nil {
		t.Fatalf("registerRoutes: %v", err)
	}
	defer fwd.Shutdown(context.Background())
	server := httptest.NewServer(mux)
	defer server.Close()

	watchFile(t, file)

	receive := func() string {
		t.Helper()
		select {
		case got := <-auths:
			return got
		case <-time.After(2 * time.Second):
			t.Fatal("backend received nothing")
			return ""
		}
	}

	if got := deltaStatus(t, server.URL, ""); got != http.StatusAccepted {
		t.Fatalf("status = %d, want %d", got, http.StatusAccepted)
	}
	if got := receive(); got != "Bearer key-1" {
		t.Fatalf("authorization = %q, want %q", got, "Bearer key-1")
	}

	replaceTokenFile(t, path, "key-2\n")
	waitUntil(t, 2*time.Second, func() bool { return cfg.AuthToken.Token() == "key-2" })
	deltaStatus(t, server.URL, "")
	if got := receive(); got != "Bearer key-2" {
		t.Fatalf("authorization after the change = %q, want %q", got, "Bearer key-2")
	}

	failuresBefore := metrics.TokenReloadFailures.Value("forward")
	replaceTokenFile(t, path, "key-3\nkey-4\n")
	waitUntil(t, 2*time.Second, func() bool { return metrics.TokenReloadFailures.Value("forward") > failuresBefore })
	deltaStatus(t, server.URL, "")
	if got := receive(); got != "Bearer key-2" {
		t.Fatalf("authorization after a two-key file = %q, want the last good %q", got, "Bearer key-2")
	}
}

// forwardedRequest is what the backend received for one forwarded
// payload.
type forwardedRequest struct {
	header http.Header
	body   []byte
}

// forwardAllThree posts a delta batch, a manifest and a static baseline
// to a collector wired with redactCfg and logging to logger. It returns
// what the backend received, by path.
func forwardAllThree(t *testing.T, redactCfg processor.RedactionConfig, logger *slog.Logger) map[string]forwardedRequest {
	t.Helper()
	received := make(chan struct {
		path string
		req  forwardedRequest
	}, 3)
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(r.Body)
		if err != nil {
			t.Errorf("read forwarded body: %v", err)
		}
		received <- struct {
			path string
			req  forwardedRequest
		}{r.URL.Path, forwardedRequest{r.Header.Clone(), body}}
		w.WriteHeader(http.StatusAccepted)
	}))
	defer backend.Close()

	mux := http.NewServeMux()
	fwd := mustRegisterRoutes(t, mux, routeConfig{Logger: logger, Forward: forward.Config{URL: backend.URL}, Redaction: redactCfg})
	defer fwd.Shutdown(context.Background())
	server := httptest.NewServer(mux)
	defer server.Close()

	manifest, err := proto.Marshal(manifestWithLiteral())
	if err != nil {
		t.Fatalf("marshal manifest: %v", err)
	}
	manifestReq, err := http.NewRequest(http.MethodPost, server.URL+"/v1/otherlode/manifest", bytes.NewReader(manifest))
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	manifestReq.Header.Set("Content-Type", "application/x-protobuf")
	for _, req := range []*http.Request{deltaRequest(t, server.URL), manifestReq, staticBaselineRequest(t, server.URL)} {
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatalf("post %s: %v", req.URL.Path, err)
		}
		resp.Body.Close()
		if resp.StatusCode != http.StatusAccepted {
			t.Fatalf("post %s: status = %d, want %d", req.URL.Path, resp.StatusCode, http.StatusAccepted)
		}
	}

	got := make(map[string]forwardedRequest)
	for len(got) < 3 {
		select {
		case r := <-received:
			got[r.path] = r.req
		case <-time.After(5 * time.Second):
			t.Fatalf("timed out with %d of 3 payloads forwarded", len(got))
		}
	}
	return got
}

func TestRegisterRoutes_RedactionOn_EveryForwardedRequestCarriesLoggedFingerprint(t *testing.T) {
	var logs bytes.Buffer
	logger := slog.New(slog.NewJSONHandler(&logs, nil))
	secret := []byte(testRedactSecret)

	got := forwardAllThree(t, processor.RedactionConfig{AllLiterals: true, Secret: secret}, logger)

	fingerprint := processor.SecretFingerprint(secret)
	if want := `"secret_fingerprint":"` + fingerprint + `"`; !strings.Contains(logs.String(), want) {
		t.Fatalf("startup log does not hold %s:\n%s", want, logs.String())
	}
	for path, req := range got {
		if values := req.header.Values(forward.RedactionHeader); len(values) != 1 || values[0] != fingerprint {
			t.Errorf("%s: %s = %q, want the logged fingerprint %q", path, forward.RedactionHeader, values, fingerprint)
		}
		for name, values := range req.header {
			for _, v := range values {
				if strings.Contains(v, testRedactSecret) {
					t.Errorf("%s: header %s carries the redaction secret", path, name)
				}
			}
		}
		if bytes.Contains(req.body, secret) {
			t.Errorf("%s: body carries the redaction secret", path)
		}
	}
}

func TestRegisterRoutes_RedactionOff_ForwardsNoRedactionHeader(t *testing.T) {
	for path, req := range forwardAllThree(t, processor.RedactionConfig{}, nil) {
		if values := req.header.Values(forward.RedactionHeader); len(values) != 0 {
			t.Errorf("%s: %s = %q, want no header", path, forward.RedactionHeader, values)
		}
	}
}
