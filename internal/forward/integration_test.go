package forward

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"google.golang.org/protobuf/proto"

	otherlodepb "buf.build/gen/go/otherlode/otherlode/protocolbuffers/go/otherlode/v1"

	"github.com/otherlodehq/otherlode-collector/ingest"
)

type captureSink struct {
	mu           sync.Mutex
	deltaBatches []*otherlodepb.DeltaBatch
	manifests    []*otherlodepb.ProbeManifest
	baselines    []*otherlodepb.StaticBaseline
	listings     []*otherlodepb.ClassListing
}

func (c *captureSink) AcceptDeltaBatch(_ context.Context, batch *otherlodepb.DeltaBatch) ([]ingest.Notice, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.deltaBatches = append(c.deltaBatches, batch)
	return nil, nil
}

func (c *captureSink) AcceptManifest(_ context.Context, manifest *otherlodepb.ProbeManifest) ([]ingest.Notice, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.manifests = append(c.manifests, manifest)
	return nil, nil
}

func (c *captureSink) AcceptStaticBaseline(_ context.Context, baseline *otherlodepb.StaticBaseline) ([]ingest.Notice, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.baselines = append(c.baselines, baseline)
	return nil, nil
}

func (c *captureSink) AcceptClassListing(_ context.Context, listing *otherlodepb.ClassListing) ([]ingest.Notice, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.listings = append(c.listings, listing)
	return nil, nil
}

func (c *captureSink) listingCount() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return len(c.listings)
}

func (c *captureSink) deltaBatchCount() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return len(c.deltaBatches)
}

func (c *captureSink) manifestCount() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return len(c.manifests)
}

func (c *captureSink) baselineCount() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return len(c.baselines)
}

func (c *captureSink) lastDeltaBatch() *otherlodepb.DeltaBatch {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.deltaBatches[len(c.deltaBatches)-1]
}

func (c *captureSink) lastManifest() *otherlodepb.ProbeManifest {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.manifests[len(c.manifests)-1]
}

func (c *captureSink) baselinesSnapshot() []*otherlodepb.StaticBaseline {
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([]*otherlodepb.StaticBaseline(nil), c.baselines...)
}

func newReferenceBackend() (*httptest.Server, *captureSink) {
	sink := &captureSink{}
	mux := http.NewServeMux()
	ingest.NewHandler(sink, slog.New(slog.DiscardHandler)).Register(mux)
	return httptest.NewServer(mux), sink
}

func newFrontCollector(t *testing.T, backendURL string) *httptest.Server {
	t.Helper()
	fwd := mustNewSink(t, testConfig(backendURL))
	t.Cleanup(func() { fwd.Shutdown(t.Context()) })
	mux := http.NewServeMux()
	ingest.NewHandler(fwd, slog.New(slog.DiscardHandler)).Register(mux)
	return httptest.NewServer(mux)
}

func TestIntegration_DeltaBatch_RoundTripsThroughRealHandlerOnBothEnds(t *testing.T) {
	backend, sink := newReferenceBackend()
	defer backend.Close()

	front := newFrontCollector(t, backend.URL)
	defer front.Close()

	sent := &otherlodepb.DeltaBatch{
		Resource: &otherlodepb.ResourceAttributes{
			ServiceName:       "checkout",
			ServiceInstanceId: "instance-7",
			RunId:             "run-1",
		},
		Deltas: []*otherlodepb.ProbeDelta{
			{ClassId: 3, ProbeIndex: 1, Kind: otherlodepb.ProbeKind_BRANCH, HitsTotal: 42},
		},
		EndpointDeltas: []*otherlodepb.EndpointDelta{
			{EndpointId: 5, FirstSeenAt: 1700000000, HitsTotal: 9},
		},
		DependencyDeltas: []*otherlodepb.DependencyDelta{
			{DependencyId: 2, FirstLoadedAt: 1700000000, LoadedClassesTotal: 378},
		},
	}
	body, err := proto.Marshal(sent)
	if err != nil {
		t.Fatalf("marshal batch: %v", err)
	}

	resp, err := http.Post(front.URL+ingest.DeltaBatchPath, "application/x-protobuf", strings.NewReader(string(body)))
	if err != nil {
		t.Fatalf("post to front collector: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusAccepted {
		t.Fatalf("front collector status = %d, want %d", resp.StatusCode, http.StatusAccepted)
	}

	waitFor(t, time.Second, func() bool { return sink.deltaBatchCount() == 1 })
	if got := sink.lastDeltaBatch(); !proto.Equal(got, sent) {
		t.Errorf("backend decoded %v, want %v", got, sent)
	}
}

func TestIntegration_Manifest_RoundTripsThroughRealHandlerOnBothEnds(t *testing.T) {
	backend, sink := newReferenceBackend()
	defer backend.Close()

	front := newFrontCollector(t, backend.URL)
	defer front.Close()

	sent := &otherlodepb.ProbeManifest{
		Resource: &otherlodepb.ResourceAttributes{ServiceName: "checkout", ServiceInstanceId: "instance-7", RunId: "run-1"},
		Probes: []*otherlodepb.ProbeLocation{
			{ClassId: 3, ProbeIndex: 1, Kind: otherlodepb.ProbeKind_BRANCH, ClassName: "CheckoutService", MethodName: "applyDiscount"},
			{
				ClassId: 3, ProbeIndex: 0, Kind: otherlodepb.ProbeKind_METHOD, ClassName: "CheckoutService", MethodName: "checkout",
				Calls: []*otherlodepb.CallEdge{
					{ClassName: "CheckoutService", MethodName: "applyDiscount", MethodDescriptor: "()V", Virtual: true},
				},
				ReferencedClasses: []string{"tools.jackson.databind.json.JsonMapper"},
			},
		},
		ClassLocations: []*otherlodepb.ClassLocation{
			{ClassId: 3, SuperClassName: "java.lang.Object", InterfaceNames: []string{"Service"}},
		},
		Endpoints: []*otherlodepb.EndpointLocation{
			{
				EndpointId:        5,
				Verb:              "POST",
				RouteTemplate:     "/checkout/{id}",
				VerbatimTemplate:  "/checkout/{id}",
				Framework:         "spring-mvc",
				DiscoverySource:   otherlodepb.EndpointDiscoverySource_REGISTRATION,
				HandlerClass:      proto.String("CheckoutService"),
				HandlerMethod:     proto.String("applyDiscount"),
				HandlerDescriptor: proto.String("()V"),
			},
		},
		DisabledEndpointModules: []*otherlodepb.DisabledEndpointModule{
			{Module: "ktor-2", Reason: "no supported framework class on the classpath", DisabledAt: 1700000000},
		},
		Dependencies: []*otherlodepb.DependencyLocation{
			{
				DependencyId:    2,
				Identities:      []*otherlodepb.DependencyIdentity{{GroupId: "tools.jackson.core", ArtifactId: "jackson-databind", Version: "3.1.5"}},
				IdentitySource:  otherlodepb.DependencyIdentitySource_POM_PROPERTIES,
				Location:        "BOOT-INF/lib/jackson-databind-3.1.5.jar",
				DiscoverySource: otherlodepb.DependencyDiscoverySource_STARTUP_CLASSPATH,
				ClassCount:      proto.Int32(880),
			},
		},
		ClassReferences: []*otherlodepb.ClassReferences{
			{ClassId: 3, ReferencedClasses: []string{"org.springframework.stereotype.Service"}},
		},
		ExternalClasses: []*otherlodepb.ExternalClass{
			{ClassName: "tools.jackson.databind.json.JsonMapper", DependencyId: proto.Int32(2)},
			{ClassName: "org.example.Missing", Absent: true},
		},
		ReferencesRecorded: true,
		DependenciesListed: true,
	}
	body, err := proto.Marshal(sent)
	if err != nil {
		t.Fatalf("marshal manifest: %v", err)
	}

	resp, err := http.Post(front.URL+ingest.ManifestPath, "application/x-protobuf", strings.NewReader(string(body)))
	if err != nil {
		t.Fatalf("post to front collector: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusAccepted {
		t.Fatalf("front collector status = %d, want %d", resp.StatusCode, http.StatusAccepted)
	}

	waitFor(t, time.Second, func() bool { return sink.manifestCount() == 1 })
	if got := sink.lastManifest(); !proto.Equal(got, sent) {
		t.Errorf("backend decoded %v, want %v", got, sent)
	}
}

func TestIntegration_StaticBaseline_RoundTripsThroughRealHandlerOnBothEnds(t *testing.T) {
	backend, sink := newReferenceBackend()
	defer backend.Close()

	front := newFrontCollector(t, backend.URL)
	defer front.Close()

	resource := &otherlodepb.ResourceAttributes{ServiceName: "checkout", ServiceInstanceId: "instance-7", RunId: "run-1"}
	const scannedAt = 1700000000

	chunk0 := &otherlodepb.StaticBaseline{
		Resource:   resource,
		ScannedAt:  scannedAt,
		ChunkIndex: 0,
		ChunkCount: 2,
		DeclaredClasses: []*otherlodepb.DeclaredClass{
			{
				ClassName:         "CheckoutService",
				Methods:           []*otherlodepb.DeclaredMethod{{MethodName: "applyDiscount", MethodDescriptor: "()V", ReferencedClasses: []string{"tools.jackson.databind.json.JsonMapper"}}},
				ReferencedClasses: []string{"org.springframework.stereotype.Service"},
			},
		},
	}
	chunk1 := &otherlodepb.StaticBaseline{
		Resource:   resource,
		ScannedAt:  scannedAt,
		ChunkIndex: 1,
		ChunkCount: 2,
		DeclaredClasses: []*otherlodepb.DeclaredClass{
			{ClassName: "PaymentService", Methods: []*otherlodepb.DeclaredMethod{{MethodName: "charge", MethodDescriptor: "()V"}}},
		},
	}

	for _, chunk := range []*otherlodepb.StaticBaseline{chunk0, chunk1} {
		body, err := proto.Marshal(chunk)
		if err != nil {
			t.Fatalf("marshal baseline chunk %d: %v", chunk.GetChunkIndex(), err)
		}
		resp, err := http.Post(front.URL+ingest.StaticBaselinePath, "application/x-protobuf", strings.NewReader(string(body)))
		if err != nil {
			t.Fatalf("post chunk %d to front collector: %v", chunk.GetChunkIndex(), err)
		}
		resp.Body.Close()
		if resp.StatusCode != http.StatusAccepted {
			t.Fatalf("front collector status for chunk %d = %d, want %d", chunk.GetChunkIndex(), resp.StatusCode, http.StatusAccepted)
		}
	}

	waitFor(t, time.Second, func() bool { return sink.baselineCount() == 2 })

	got := sink.baselinesSnapshot()
	byChunk := map[int32]*otherlodepb.StaticBaseline{got[0].GetChunkIndex(): got[0], got[1].GetChunkIndex(): got[1]}
	if !proto.Equal(byChunk[0], chunk0) {
		t.Errorf("backend decoded chunk 0 as %v, want %v", byChunk[0], chunk0)
	}
	if !proto.Equal(byChunk[1], chunk1) {
		t.Errorf("backend decoded chunk 1 as %v, want %v", byChunk[1], chunk1)
	}
	if byChunk[0].GetScannedAt() != byChunk[1].GetScannedAt() {
		t.Errorf("chunk scanned_at mismatch: %d vs %d", byChunk[0].GetScannedAt(), byChunk[1].GetScannedAt())
	}
}

func TestIntegration_ClassListing_RoundTripsThroughRealHandlerOnBothEnds(t *testing.T) {
	backend, sink := newReferenceBackend()
	defer backend.Close()

	front := newFrontCollector(t, backend.URL)
	defer front.Close()

	sent := &otherlodepb.ClassListing{
		Resource:   &otherlodepb.ResourceAttributes{ServiceName: "checkout", ServiceInstanceId: "instance-7", RunId: "run-1"},
		ClassNames: []string{"com.acme.CheckoutService", "com.acme.CheckoutService$Inner", "com.acme.PaymentService"},
		IncludeRules: &otherlodepb.IncludeRules{
			IncludePackages: []string{"com.acme"},
			ExcludePackages: []string{"com.acme.generated"},
		},
		ListedAt:   1700000000,
		ChunkIndex: 0,
		ChunkCount: 1,
	}
	body, err := proto.Marshal(sent)
	if err != nil {
		t.Fatalf("marshal listing: %v", err)
	}
	resp, err := http.Post(front.URL+ingest.ClassListingPath, "application/x-protobuf", strings.NewReader(string(body)))
	if err != nil {
		t.Fatalf("post to front collector: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusAccepted {
		t.Fatalf("front collector status = %d, want %d", resp.StatusCode, http.StatusAccepted)
	}

	waitFor(t, time.Second, func() bool { return sink.listingCount() == 1 })
	sink.mu.Lock()
	got := sink.listings[0]
	sink.mu.Unlock()
	if !proto.Equal(got, sent) {
		t.Errorf("backend decoded %v, want %v", got, sent)
	}
}

func frontCollectorWith(t *testing.T, cfg Config, maxDecodes int, opts ...ingest.HandlerOption) (*httptest.Server, *ForwardingSink) {
	t.Helper()
	fwd := mustNewSink(t, cfg)
	t.Cleanup(func() { fwd.Shutdown(context.Background()) })
	mux := http.NewServeMux()
	ingest.NewHandler(fwd, slog.New(slog.DiscardHandler), append([]ingest.HandlerOption{ingest.WithMaxConcurrentDecodes(maxDecodes)}, opts...)...).Register(mux)
	front := httptest.NewServer(mux)
	t.Cleanup(front.Close)
	return front, fwd
}

func postManifest(t *testing.T, frontURL, serviceName string) *http.Response {
	t.Helper()
	body, err := proto.Marshal(manifestWithService(serviceName))
	if err != nil {
		t.Fatalf("marshal manifest for %s: %v", serviceName, err)
	}
	resp, err := http.Post(frontURL+ingest.ManifestPath, "application/x-protobuf", bytes.NewReader(body))
	if err != nil {
		t.Fatalf("post manifest for %s: %v", serviceName, err)
	}
	resp.Body.Close()
	return resp
}

func TestIntegration_BackendStatus_IsWhatTheAgentSees(t *testing.T) {
	tests := []struct {
		name           string
		backendStatus  int
		backendHeaders map[string]string
		wantStatus     int
		wantRetryAfter string
	}{
		{"200 is not a confirmation", http.StatusOK, nil, http.StatusServiceUnavailable, "5"},
		{"204 is not a confirmation", http.StatusNoContent, nil, http.StatusServiceUnavailable, "5"},
		{"202", http.StatusAccepted, nil, http.StatusAccepted, ""},
		{"429 passes Retry-After", http.StatusTooManyRequests, map[string]string{"Retry-After": "42"}, http.StatusTooManyRequests, "42"},
		{"400", http.StatusBadRequest, nil, http.StatusBadRequest, ""},
		{"401", http.StatusUnauthorized, nil, http.StatusUnauthorized, ""},
		{"403", http.StatusForbidden, nil, http.StatusForbidden, ""},
		{"404", http.StatusNotFound, nil, http.StatusNotFound, ""},
		{"413", http.StatusRequestEntityTooLarge, nil, http.StatusRequestEntityTooLarge, ""},
		{"500", http.StatusInternalServerError, nil, http.StatusServiceUnavailable, "5"},
		{"502", http.StatusBadGateway, nil, http.StatusServiceUnavailable, "5"},
		{"503 does not pass its Retry-After", http.StatusServiceUnavailable, map[string]string{"Retry-After": "99"}, http.StatusServiceUnavailable, "5"},
		{"302", http.StatusFound, map[string]string{"Location": "http://127.0.0.1:1/"}, http.StatusServiceUnavailable, "5"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			backend, _ := statusBackend(t, tt.backendStatus, tt.backendHeaders, "")
			front := newFrontCollector(t, backend.URL)
			defer front.Close()

			resp := postManifest(t, front.URL, "svc")

			if resp.StatusCode != tt.wantStatus {
				t.Errorf("status = %d, want %d", resp.StatusCode, tt.wantStatus)
			}
			if got := resp.Header.Get("Retry-After"); got != tt.wantRetryAfter {
				t.Errorf("Retry-After = %q, want %q", got, tt.wantRetryAfter)
			}
		})
	}
}

func TestIntegration_BackendTimeout_AnswersServiceUnavailable(t *testing.T) {
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.Copy(io.Discard, r.Body)
		<-r.Context().Done()
	}))
	defer backend.Close()
	cfg := testConfig(backend.URL)
	cfg.RequestTimeout = 100 * time.Millisecond
	front, _ := frontCollectorWith(t, cfg, 4)

	resp := postManifest(t, front.URL, "svc")

	if resp.StatusCode != http.StatusServiceUnavailable || resp.Header.Get("Retry-After") != "5" {
		t.Fatalf("status = %d, Retry-After = %q, want 503 and 5", resp.StatusCode, resp.Header.Get("Retry-After"))
	}
}

func TestIntegration_DecodeSlot_ReleasedBeforeTheNetworkCall(t *testing.T) {
	url, entered, release := blockingBackend(t)
	cfg := testConfig(url)
	cfg.RequestTimeout = time.Hour
	front, _ := frontCollectorWith(t, cfg, 1, ingest.WithSlotWait(50*time.Millisecond))

	results := make(chan int, 2)
	go func() { results <- postManifest(t, front.URL, "svc-1").StatusCode }()
	waitEntered(t, entered)
	go func() { results <- postManifest(t, front.URL, "svc-2").StatusCode }()
	waitEntered(t, entered)

	release()
	for range 2 {
		if got := <-results; got != http.StatusAccepted {
			t.Errorf("status = %d, want %d", got, http.StatusAccepted)
		}
	}
}

func TestIntegration_DecodeSlot_HeldByASinkThatNeverReleases(t *testing.T) {
	gate := make(chan struct{})
	entered := make(chan struct{}, 4)
	mux := http.NewServeMux()
	ingest.NewHandler(&holdingSink{entered: entered, gate: gate}, slog.New(slog.DiscardHandler),
		ingest.WithMaxConcurrentDecodes(1), ingest.WithSlotWait(50*time.Millisecond)).Register(mux)
	front := httptest.NewServer(mux)
	defer front.Close()
	defer close(gate)

	go func() { postManifest(t, front.URL, "svc-1") }()
	<-entered

	if got := postManifest(t, front.URL, "svc-2"); got.StatusCode != http.StatusServiceUnavailable || got.Header.Get("Retry-After") == "" {
		t.Errorf("status = %d, Retry-After = %q, want 503 with Retry-After", got.StatusCode, got.Header.Get("Retry-After"))
	}
}

type holdingSink struct {
	captureSink
	entered chan struct{}
	gate    chan struct{}
}

func (h *holdingSink) AcceptManifest(context.Context, *otherlodepb.ProbeManifest) ([]ingest.Notice, error) {
	h.entered <- struct{}{}
	<-h.gate
	return nil, nil
}

func TestIntegration_ForwardCap_AnswersServiceUnavailable(t *testing.T) {
	url, entered, release := blockingBackend(t)
	cfg := testConfig(url)
	cfg.RequestTimeout = time.Hour
	cfg.MaxConcurrentForwards = 1
	front, fwd := frontCollectorWith(t, cfg, 8, ingest.WithRequestBudget(300*time.Millisecond))
	before := failedCount("manifest", "busy")

	first := make(chan error, 1)
	go func() {
		_, err := fwd.AcceptManifest(context.Background(), manifestWithService("svc-1"))
		first <- err
	}()
	waitEntered(t, entered)

	start := time.Now()
	second := postManifest(t, front.URL, "svc-2")
	if second.StatusCode != http.StatusServiceUnavailable || second.Header.Get("Retry-After") != "5" {
		t.Errorf("request past the forward cap: status = %d, Retry-After = %q, want 503 and 5", second.StatusCode, second.Header.Get("Retry-After"))
	}
	if elapsed := time.Since(start); elapsed > 2*time.Second {
		t.Errorf("request past the forward cap answered after %v, want about the 300ms budget", elapsed)
	}
	if got := failedCount("manifest", "busy"); got != before+1 {
		t.Errorf("failed(busy) = %d, want %d", got, before+1)
	}

	release()
	if err := <-first; err != nil {
		t.Errorf("request holding the forward slot: %v", err)
	}
}

func TestIntegration_Budget_SlowForwardGets503BeforeTheBudgetEnds(t *testing.T) {
	url, entered, release := blockingBackend(t)
	defer release()
	cfg := testConfig(url)
	cfg.RequestTimeout = time.Hour
	const budget = 500 * time.Millisecond
	front, _ := frontCollectorWith(t, cfg, 4, ingest.WithRequestBudget(budget))

	start := time.Now()
	resp := postManifest(t, front.URL, "svc")
	elapsed := time.Since(start)
	waitEntered(t, entered)

	if resp.StatusCode != http.StatusServiceUnavailable || resp.Header.Get("Retry-After") != "5" {
		t.Errorf("status = %d, Retry-After = %q, want 503 and 5", resp.StatusCode, resp.Header.Get("Retry-After"))
	}
	if elapsed < budget-50*time.Millisecond || elapsed > budget+500*time.Millisecond {
		t.Errorf("answered after %v, want about the %v budget", elapsed, budget)
	}
}

func TestIntegration_Budget_SlotWaitAndForwardShareIt(t *testing.T) {
	entered := make(chan struct{}, 4)
	url, _, release := blockingBackend(t)
	defer release()
	cfg := testConfig(url)
	cfg.RequestTimeout = time.Hour
	fwd := mustNewSink(t, cfg)
	t.Cleanup(func() { fwd.Shutdown(context.Background()) })

	const budget = 600 * time.Millisecond
	const hold = 300 * time.Millisecond
	mux := http.NewServeMux()
	gated := &slotHoldSink{Sink: fwd, hold: hold, entered: entered}
	ingest.NewHandler(gated, slog.New(slog.DiscardHandler), ingest.WithMaxConcurrentDecodes(1), ingest.WithSlotWait(time.Hour), ingest.WithRequestBudget(budget)).Register(mux)
	front := httptest.NewServer(mux)
	defer front.Close()

	go func() { postManifest(t, front.URL, "holder") }()
	<-entered

	start := time.Now()
	resp := postManifest(t, front.URL, "svc")
	elapsed := time.Since(start)

	if resp.StatusCode != http.StatusServiceUnavailable {
		t.Errorf("status = %d, want 503", resp.StatusCode)
	}
	if elapsed < budget-100*time.Millisecond || elapsed > budget+200*time.Millisecond {
		t.Errorf("answered after %v, want about the %v budget, not the budget plus the wait", elapsed, budget)
	}
	deadline := gated.deadline.Load()
	if deadline == nil {
		t.Fatal("the second request never reached the sink")
	}
	if got := deadline.Sub(start); got > budget+50*time.Millisecond {
		t.Errorf("sink deadline is %v after the request started, want about %v: the budget starts at handler entry", got, budget)
	}
}

type slotHoldSink struct {
	ingest.Sink
	hold    time.Duration
	entered chan struct{}
	once    sync.Once

	deadline atomic.Pointer[time.Time]
}

func (s *slotHoldSink) AcceptManifest(ctx context.Context, m *otherlodepb.ProbeManifest) ([]ingest.Notice, error) {
	first := false
	s.once.Do(func() { first = true })
	if first {
		s.entered <- struct{}{}
		time.Sleep(s.hold)
		return nil, nil
	}
	if d, ok := ctx.Deadline(); ok {
		s.deadline.Store(&d)
	}
	return s.Sink.AcceptManifest(ctx, m)
}

func TestIntegration_Shutdown_FinishesInFlightAndRefusesNew(t *testing.T) {
	url, entered, release := blockingBackend(t)
	cfg := testConfig(url)
	cfg.RequestTimeout = time.Hour
	front, fwd := frontCollectorWith(t, cfg, 4)

	inFlight := make(chan int, 1)
	go func() { inFlight <- postManifest(t, front.URL, "svc-1").StatusCode }()
	waitEntered(t, entered)

	shutdownDone := make(chan struct{})
	go func() {
		fwd.Shutdown(context.Background())
		close(shutdownDone)
	}()
	waitFor(t, time.Second, func() bool {
		fwd.mu.RLock()
		defer fwd.mu.RUnlock()
		return fwd.closed
	})

	if got := postManifest(t, front.URL, "svc-2").StatusCode; got != http.StatusServiceUnavailable {
		t.Errorf("request after shutdown started: status = %d, want %d", got, http.StatusServiceUnavailable)
	}

	release()
	if got := <-inFlight; got != http.StatusAccepted {
		t.Errorf("request in flight: status = %d, want %d", got, http.StatusAccepted)
	}
	<-shutdownDone
}

func TestIntegration_ForwardCapPushesBackOnDecoding(t *testing.T) {
	url, entered, release := blockingBackend(t)
	defer release()
	cfg := testConfig(url)
	cfg.RequestTimeout = time.Hour
	cfg.MaxConcurrentForwards = 1
	fwd := mustNewSink(t, cfg)
	t.Cleanup(func() { fwd.Shutdown(context.Background()) })

	const decodes = 2
	counting := &countingSink{Sink: fwd}
	mux := http.NewServeMux()
	ingest.NewHandler(counting, slog.New(slog.DiscardHandler),
		ingest.WithMaxConcurrentDecodes(decodes),
		ingest.WithSlotWait(50*time.Millisecond),
		ingest.WithRequestBudget(600*time.Millisecond)).Register(mux)
	front := httptest.NewServer(mux)
	defer front.Close()

	var wg sync.WaitGroup
	for i := range 8 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			postManifest(t, front.URL, fmt.Sprintf("svc-%d", i))
		}()
		if i == 0 {
			waitEntered(t, entered)
		}
	}
	wg.Wait()

	if got := counting.maxActive.Load(); got > decodes+1 {
		t.Errorf("requests inside the sink at once = %d, want at most %d: the decode cap %d plus the one holding the forward slot", got, decodes+1, decodes)
	}
	if got := counting.maxActive.Load(); got < decodes+1 {
		t.Errorf("requests inside the sink at once = %d, want %d reached, or the test proves nothing", got, decodes+1)
	}
}

type countingSink struct {
	ingest.Sink
	active    atomic.Int32
	maxActive atomic.Int32
}

func (c *countingSink) AcceptManifest(ctx context.Context, m *otherlodepb.ProbeManifest) ([]ingest.Notice, error) {
	n := c.active.Add(1)
	defer c.active.Add(-1)
	for {
		m := c.maxActive.Load()
		if n <= m || c.maxActive.CompareAndSwap(m, n) {
			break
		}
	}
	return c.Sink.AcceptManifest(ctx, m)
}

func TestIntegration_SinkFailureLogs_KeepApartByCause(t *testing.T) {
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer backend.Close()
	fwd := mustNewSink(t, testConfig(backend.URL))
	var logs bytes.Buffer
	mux := http.NewServeMux()
	ingest.NewHandler(fwd, slog.New(slog.NewJSONHandler(&logs, nil))).Register(mux)
	front := httptest.NewServer(mux)
	defer front.Close()

	postManifest(t, front.URL, "svc")
	postManifest(t, front.URL, "svc")
	fwd.Shutdown(context.Background())
	postManifest(t, front.URL, "svc")

	out := logs.String()
	for _, reason := range []string{"server_error", "shutting_down"} {
		if got := strings.Count(out, `"reason":"`+reason+`"`); got != 1 {
			t.Errorf("log lines with reason %s = %d, want 1: %s", reason, got, out)
		}
	}
}
