package forward

import (
	"bytes"
	"context"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"google.golang.org/protobuf/proto"

	otherlodepb "buf.build/gen/go/otherlode/otherlode/protocolbuffers/go/otherlode/v1"

	"github.com/otherlodehq/otherlode-collector/ingest"
	"github.com/otherlodehq/otherlode-collector/internal/ratelog"
	"github.com/otherlodehq/otherlode-collector/metrics"
)

// mustNewSink constructs a sink for a config the test expects to be valid.
func mustNewSink(t *testing.T, cfg Config) *ForwardingSink {
	t.Helper()
	sink, err := NewForwardingSink(cfg)
	if err != nil {
		t.Fatalf("NewForwardingSink: %v", err)
	}
	return sink
}

func manifestWithService(name string) *otherlodepb.ProbeManifest {
	return &otherlodepb.ProbeManifest{Resource: &otherlodepb.ResourceAttributes{ServiceName: name, ServiceInstanceId: "instance-1", RunId: "run-1"}}
}

func waitFor(t *testing.T, timeout time.Duration, cond func() bool) {
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

func TestForwardingSink_Manifest_RelayedToBackendPath(t *testing.T) {
	var gotPath, gotContentType, gotAuth string
	var gotBody []byte
	var received atomic.Bool

	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		gotContentType = r.Header.Get("Content-Type")
		gotAuth = r.Header.Get("Authorization")
		gotBody, _ = io.ReadAll(r.Body)
		received.Store(true)
		w.WriteHeader(http.StatusAccepted)
	}))
	defer backend.Close()

	cfg := testConfig(backend.URL)
	cfg.AuthToken = StaticToken("backend-secret")
	sink := mustNewSink(t, cfg)
	defer sink.Shutdown(context.Background())

	if _, err := sink.AcceptManifest(context.Background(), manifestWithService("demo-service")); err != nil {
		t.Fatalf("AcceptManifest: %v", err)
	}

	waitFor(t, time.Second, received.Load)

	if gotPath != ingest.ManifestPath {
		t.Errorf("path = %q, want %q", gotPath, ingest.ManifestPath)
	}
	if gotContentType != "application/x-protobuf" {
		t.Errorf("content-type = %q, want application/x-protobuf", gotContentType)
	}
	if gotAuth != "Bearer backend-secret" {
		t.Errorf("authorization = %q, want %q", gotAuth, "Bearer backend-secret")
	}

	var manifest otherlodepb.ProbeManifest
	if err := proto.Unmarshal(gotBody, &manifest); err != nil {
		t.Fatalf("unmarshal relayed body: %v", err)
	}
	if manifest.GetResource().GetServiceName() != "demo-service" {
		t.Errorf("relayed service name = %q, want %q", manifest.GetResource().GetServiceName(), "demo-service")
	}
}

func TestForwardingSink_DeltaBatch_RelayedToBackendPath(t *testing.T) {
	var gotPath string
	var received atomic.Bool

	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		received.Store(true)
		w.WriteHeader(http.StatusAccepted)
	}))
	defer backend.Close()

	sink := mustNewSink(t, testConfig(backend.URL))
	defer sink.Shutdown(context.Background())

	if _, err := sink.AcceptDeltaBatch(context.Background(), &otherlodepb.DeltaBatch{
		Resource: &otherlodepb.ResourceAttributes{ServiceName: "demo-service", ServiceInstanceId: "instance-1", RunId: "run-1"},
	}); err != nil {
		t.Fatalf("AcceptDeltaBatch: %v", err)
	}

	waitFor(t, time.Second, received.Load)
	if gotPath != ingest.DeltaBatchPath {
		t.Errorf("path = %q, want %q", gotPath, ingest.DeltaBatchPath)
	}
}

func TestForwardingSink_StaticBaseline_RelayedToBackendPath(t *testing.T) {
	var gotPath, gotContentType string
	var gotBody []byte
	var received atomic.Bool

	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		gotContentType = r.Header.Get("Content-Type")
		gotBody, _ = io.ReadAll(r.Body)
		received.Store(true)
		w.WriteHeader(http.StatusAccepted)
	}))
	defer backend.Close()

	sink := mustNewSink(t, testConfig(backend.URL))
	defer sink.Shutdown(context.Background())

	sent := &otherlodepb.StaticBaseline{
		Resource:   &otherlodepb.ResourceAttributes{ServiceName: "demo-service", ServiceInstanceId: "instance-1", RunId: "run-1"},
		ScannedAt:  1700000000,
		ChunkIndex: 1,
		ChunkCount: 2,
	}
	if _, err := sink.AcceptStaticBaseline(context.Background(), sent); err != nil {
		t.Fatalf("AcceptStaticBaseline: %v", err)
	}

	waitFor(t, time.Second, received.Load)
	if gotPath != ingest.StaticBaselinePath {
		t.Errorf("path = %q, want %q", gotPath, ingest.StaticBaselinePath)
	}
	if gotContentType != "application/x-protobuf" {
		t.Errorf("content-type = %q, want application/x-protobuf", gotContentType)
	}

	var got otherlodepb.StaticBaseline
	if err := proto.Unmarshal(gotBody, &got); err != nil {
		t.Fatalf("unmarshal relayed body: %v", err)
	}
	if got.GetChunkIndex() != sent.GetChunkIndex() || got.GetChunkCount() != sent.GetChunkCount() || got.GetScannedAt() != sent.GetScannedAt() {
		t.Errorf("relayed baseline = %v, want it to match %v", &got, sent)
	}
}

func TestForwardingSink_ClassListing_RelayedToBackendPath(t *testing.T) {
	var gotPath, gotContentType string
	var gotBody []byte
	var received atomic.Bool

	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		gotContentType = r.Header.Get("Content-Type")
		gotBody, _ = io.ReadAll(r.Body)
		received.Store(true)
		w.WriteHeader(http.StatusAccepted)
	}))
	defer backend.Close()

	sink := mustNewSink(t, testConfig(backend.URL))
	defer sink.Shutdown(context.Background())

	sent := &otherlodepb.ClassListing{
		Resource:     &otherlodepb.ResourceAttributes{ServiceName: "demo-service", ServiceInstanceId: "instance-1", RunId: "run-1"},
		ClassNames:   []string{"com.example.Bar", "com.example.Foo"},
		IncludeRules: &otherlodepb.IncludeRules{IncludePackages: []string{"com.example"}},
		ListedAt:     1700000000,
		ChunkIndex:   1,
		ChunkCount:   2,
	}
	if _, err := sink.AcceptClassListing(context.Background(), sent); err != nil {
		t.Fatalf("AcceptClassListing: %v", err)
	}

	waitFor(t, time.Second, received.Load)
	if gotPath != ingest.ClassListingPath {
		t.Errorf("path = %q, want %q", gotPath, ingest.ClassListingPath)
	}
	if gotContentType != "application/x-protobuf" {
		t.Errorf("content-type = %q, want application/x-protobuf", gotContentType)
	}

	var got otherlodepb.ClassListing
	if err := proto.Unmarshal(gotBody, &got); err != nil {
		t.Fatalf("unmarshal relayed body: %v", err)
	}
	if !proto.Equal(&got, sent) {
		t.Errorf("relayed listing = %v, want %v", &got, sent)
	}
}

func TestForwardingSink_Shutdown_SecondCallIsANoOp(t *testing.T) {
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusAccepted)
	}))
	defer backend.Close()

	sink := mustNewSink(t, testConfig(backend.URL))
	sink.Shutdown(context.Background())
	sink.Shutdown(context.Background()) // must not panic on the closed stop channel
}

func TestNewForwardingSink_InvalidURL_ReturnsError(t *testing.T) {
	for _, raw := range invalidURLs {
		cfg := testConfig(raw)
		if _, err := NewForwardingSink(cfg); err == nil {
			t.Errorf("URL %q: expected an error, got nil", raw)
		}
	}
}

var invalidURLs = []string{
	"", "backend.example.com", "ftp://backend.example.com", "http://", "://bad",
	"https://h/base?x=1", "https://h/base?", "https://h/base#frag", "https://h/base#",
	"https://user:pass@h", "https://user@h/base",
}

func TestValidateURL(t *testing.T) {
	for _, raw := range invalidURLs {
		if _, _, err := ValidateURL(raw); err == nil {
			t.Errorf("ValidateURL(%q): expected an error, got nil", raw)
		}
	}

	valid := []struct {
		raw        string
		wantBase   string
		wantScheme string
	}{
		{"https://h", "https://h", "https"},
		{"https://h/base", "https://h/base", "https"},
		{"https://h/base//", "https://h/base", "https"},
		{"http://h:8080/", "http://h:8080", "http"},
		{"HTTP://h/base/", "HTTP://h/base", "http"},
	}
	for _, tt := range valid {
		base, scheme, err := ValidateURL(tt.raw)
		if err != nil {
			t.Errorf("ValidateURL(%q): unexpected error: %v", tt.raw, err)
			continue
		}
		if base != tt.wantBase || scheme != tt.wantScheme {
			t.Errorf("ValidateURL(%q) = %q, %q, want %q, %q", tt.raw, base, scheme, tt.wantBase, tt.wantScheme)
		}
	}
}

func TestForwardingSink_TrailingSlashURL_PostsToCleanPath(t *testing.T) {
	var gotPath string
	var received atomic.Bool

	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		received.Store(true)
		w.WriteHeader(http.StatusAccepted)
	}))
	defer backend.Close()

	sink := mustNewSink(t, testConfig(backend.URL+"/"))
	defer sink.Shutdown(context.Background())

	if _, err := sink.AcceptManifest(context.Background(), manifestWithService("svc")); err != nil {
		t.Fatalf("AcceptManifest: %v", err)
	}
	waitFor(t, time.Second, received.Load)
	if gotPath != ingest.ManifestPath {
		t.Fatalf("path = %q, want %q (no doubled slash from the trailing slash)", gotPath, ingest.ManifestPath)
	}
}

func TestInstanceOf_NilResource_ZeroInstance(t *testing.T) {
	if got := instanceOf(nil); got != (instance{}) {
		t.Fatalf("instanceOf(nil) = %+v, want the zero instance", got)
	}
}

// TestDefaultHTTPClient_UsesTheEnvironmentProxy pins that the default
// forwarding client honours HTTPS_PROXY, HTTP_PROXY and NO_PROXY, since a
// backend outside the network may be reachable only through a proxy. The
// healthcheck client must do the opposite; see
// TestHealthcheckClient_NeverUsesAProxy in cmd/otherlode-collector.
//
// The test compares functions instead of setting the variables, because
// http.ProxyFromEnvironment reads them once per process and an earlier
// test may already have made it do so.
func TestDefaultHTTPClient_UsesTheEnvironmentProxy(t *testing.T) {
	client := Config{}.withDefaults().HTTPClient
	transport, ok := client.Transport.(*http.Transport)
	if !ok {
		t.Fatalf("transport is %T, want *http.Transport", client.Transport)
	}
	if transport.Proxy == nil {
		t.Fatal("forward transport has no proxy function; HTTPS_PROXY would be ignored")
	}
	if reflect.ValueOf(transport.Proxy).Pointer() != reflect.ValueOf(http.ProxyFromEnvironment).Pointer() {
		t.Fatal("forward transport's proxy function is not http.ProxyFromEnvironment")
	}
}

func TestForwardingSink_LargeResponseBody_DoesNotBlockDelivery(t *testing.T) {
	deliveredBefore := metrics.ForwardDelivered.Value("manifest")
	var received atomic.Bool
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		received.Store(true)
		w.WriteHeader(http.StatusAccepted)
		// Several times the read bound; the sink must not try to drain it all.
		chunk := bytes.Repeat([]byte("x"), 1<<20)
		for i := 0; i < 4; i++ {
			if _, err := w.Write(chunk); err != nil {
				return
			}
		}
	}))
	defer backend.Close()

	sink := mustNewSink(t, testConfig(backend.URL))
	if _, err := sink.AcceptManifest(context.Background(), manifestWithService("svc")); err != nil {
		t.Fatalf("AcceptManifest: %v", err)
	}
	waitFor(t, time.Second, received.Load)

	done := make(chan struct{})
	go func() {
		sink.Shutdown(context.Background())
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("Shutdown did not return; the worker is likely still reading the oversized response")
	}
	if got := metrics.ForwardDelivered.Value("manifest"); got != deliveredBefore+1 {
		t.Fatalf("delivered count = %d, want %d (a 200 with a big body is still a success)", got, deliveredBefore+1)
	}
}

// swappableToken is a TokenSource a test can change while the sink runs.
type swappableToken struct {
	v atomic.Value
}

func (s *swappableToken) Token() string {
	token, _ := s.v.Load().(string)
	return token
}

func TestForwardingSink_AuthTokenChanges_NextRequestSendsNewKey(t *testing.T) {
	auths := make(chan string, 8)
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		auths <- r.Header.Get("Authorization")
		w.WriteHeader(http.StatusAccepted)
	}))
	defer backend.Close()

	token := new(swappableToken)
	token.v.Store("old-key")
	cfg := testConfig(backend.URL)
	cfg.AuthToken = token
	sink := mustNewSink(t, cfg)
	defer sink.Shutdown(context.Background())

	if _, err := sink.AcceptManifest(context.Background(), manifestWithService("demo-service")); err != nil {
		t.Fatalf("AcceptManifest: %v", err)
	}
	if got := <-auths; got != "Bearer old-key" {
		t.Fatalf("first authorization = %q, want %q", got, "Bearer old-key")
	}

	token.v.Store("new-key")
	if _, err := sink.AcceptManifest(context.Background(), manifestWithService("demo-service")); err != nil {
		t.Fatalf("AcceptManifest: %v", err)
	}
	if got := <-auths; got != "Bearer new-key" {
		t.Fatalf("second authorization = %q, want %q", got, "Bearer new-key")
	}
}

func TestForwardingSink_EmptyAuthToken_SendsNoHeader(t *testing.T) {
	for name, source := range map[string]TokenSource{
		"nil source":   nil,
		"empty static": StaticToken(""),
	} {
		t.Run(name, func(t *testing.T) {
			auths := make(chan []string, 1)
			backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				auths <- r.Header.Values("Authorization")
				w.WriteHeader(http.StatusAccepted)
			}))
			defer backend.Close()

			cfg := testConfig(backend.URL)
			cfg.AuthToken = source
			sink := mustNewSink(t, cfg)
			defer sink.Shutdown(context.Background())

			if _, err := sink.AcceptManifest(context.Background(), manifestWithService("demo-service")); err != nil {
				t.Fatalf("AcceptManifest: %v", err)
			}
			if got := <-auths; len(got) != 0 {
				t.Fatalf("authorization headers = %q, want none", got)
			}
		})
	}
}

func TestNewForwardingSink_PlainHTTPWithKey_Warns(t *testing.T) {
	token := new(swappableToken)
	token.v.Store("file-key")

	tests := map[string]struct {
		url      string
		token    TokenSource
		wantWarn bool
	}{
		"http with static key":   {url: "http://backend.example.com", token: StaticToken("backend-secret"), wantWarn: true},
		"http with changing key": {url: "http://backend.example.com", token: token, wantWarn: true},
		"http without key":       {url: "http://backend.example.com", token: nil, wantWarn: false},
		"https with key":         {url: "https://backend.example.com", token: StaticToken("backend-secret"), wantWarn: false},
		"upper-case HTTP":        {url: "HTTP://backend.example.com", token: StaticToken("backend-secret"), wantWarn: true},
	}

	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			var logs bytes.Buffer
			cfg := testConfig(tt.url)
			cfg.AuthToken = tt.token
			cfg.Logger = slog.New(slog.NewTextHandler(&logs, nil))
			sink := mustNewSink(t, cfg)
			sink.Shutdown(context.Background())

			warned := strings.Contains(logs.String(), "plain http")
			if warned != tt.wantWarn {
				t.Fatalf("plain http warning logged = %v, want %v:\n%s", warned, tt.wantWarn, logs.String())
			}
			if strings.Contains(logs.String(), "backend-secret") || strings.Contains(logs.String(), "file-key") {
				t.Fatalf("log shows the key:\n%s", logs.String())
			}
		})
	}
}

// blockingBackend starts a backend that holds every request until the
// returned release function runs. entered receives one value per request.
func blockingBackend(t *testing.T) (url string, entered chan struct{}, release func()) {
	t.Helper()
	gate := make(chan struct{})
	entered = make(chan struct{}, 16)
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		entered <- struct{}{}
		<-gate
		w.WriteHeader(http.StatusAccepted)
	}))
	var once sync.Once
	release = func() { once.Do(func() { close(gate) }) }
	t.Cleanup(backend.Close)
	t.Cleanup(release)
	return backend.URL, entered, release
}

func waitEntered(t *testing.T, entered chan struct{}) {
	t.Helper()
	select {
	case <-entered:
	case <-time.After(time.Second):
		t.Fatal("backend never received a request")
	}
}

// sendEveryKind passes one payload of each kind to sink.
func sendEveryKind(t *testing.T, sink *ForwardingSink) {
	t.Helper()
	res := &otherlodepb.ResourceAttributes{ServiceName: "demo-service", ServiceInstanceId: "instance-1", RunId: "run-1"}
	if _, err := sink.AcceptDeltaBatch(context.Background(), &otherlodepb.DeltaBatch{Resource: res}); err != nil {
		t.Fatalf("AcceptDeltaBatch: %v", err)
	}
	if _, err := sink.AcceptManifest(context.Background(), &otherlodepb.ProbeManifest{Resource: res}); err != nil {
		t.Fatalf("AcceptManifest: %v", err)
	}
	if _, err := sink.AcceptStaticBaseline(context.Background(), &otherlodepb.StaticBaseline{Resource: res}); err != nil {
		t.Fatalf("AcceptStaticBaseline: %v", err)
	}
	if _, err := sink.AcceptClassListing(context.Background(), &otherlodepb.ClassListing{Resource: res}); err != nil {
		t.Fatalf("AcceptClassListing: %v", err)
	}
}

// redactionHeaders runs a sink with fingerprint over every payload
// kind and returns the RedactionHeader values each path received.
func redactionHeaders(t *testing.T, fingerprint string) map[string][]string {
	t.Helper()
	var mu sync.Mutex
	got := make(map[string][]string)
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		got[r.URL.Path] = r.Header.Values(RedactionHeader)
		mu.Unlock()
		w.WriteHeader(http.StatusAccepted)
	}))
	defer backend.Close()

	cfg := testConfig(backend.URL)
	cfg.RedactionFingerprint = fingerprint
	sink := mustNewSink(t, cfg)
	sendEveryKind(t, sink)
	sink.Shutdown(context.Background())
	waitFor(t, time.Second, func() bool {
		mu.Lock()
		defer mu.Unlock()
		return len(got) == 4
	})
	return got
}

func TestForwardingSink_RedactionFingerprint_SentOnEveryPath(t *testing.T) {
	got := redactionHeaders(t, "3b53bbb8311eab74")
	for _, path := range []string{ingest.DeltaBatchPath, ingest.ManifestPath, ingest.StaticBaselinePath, ingest.ClassListingPath} {
		if values := got[path]; len(values) != 1 || values[0] != "3b53bbb8311eab74" {
			t.Errorf("%s: %s = %q, want exactly the fingerprint", path, RedactionHeader, values)
		}
	}
}

func TestForwardingSink_NoRedactionFingerprint_SendsNoHeader(t *testing.T) {
	for path, values := range redactionHeaders(t, "") {
		if len(values) != 0 {
			t.Errorf("%s: %s = %q, want no header", path, RedactionHeader, values)
		}
	}
}

func TestNewForwardingSink_FingerprintNotAHeaderValue_ReturnsError(t *testing.T) {
	cfg := testConfig("http://backend.example")
	cfg.RedactionFingerprint = "abc\ndef"
	if _, err := NewForwardingSink(cfg); err == nil {
		t.Fatal("NewForwardingSink accepted a fingerprint with a newline")
	}
}

// syncBuffer is a bytes.Buffer safe for the sink's worker and the test.
type syncBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func TestErrorCode_BoundedAndCleaned(t *testing.T) {
	long := strings.Repeat("x", 200)
	for body, want := range map[string]string{
		`{"error":"redaction_required"}`:       "redaction_required",
		`{"error":"` + long + `"}`:             long[:maxErrorCodeBytes],
		`{"error":"bad\nline\u0007"}`:          "bad?line?",
		`{"error":"caf\u00e9"}`:                "caf??",
		`{"error":"redaction_required","x":1}`: "redaction_required",
		`not json`:                             "",
	} {
		if got := errorCode([]byte(body)); got != want {
			t.Errorf("errorCode(%q) = %q, want %q", body, got, want)
		}
	}
}

func (b *syncBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *syncBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}

// testConfig returns a Config with a short request timeout in place of the
// multi-second default. Its logger discards the Warn lines these tests
// trigger on purpose.
func testConfig(url string) Config {
	return Config{
		URL:            url,
		Logger:         slog.New(slog.DiscardHandler),
		RequestTimeout: 200 * time.Millisecond,
	}
}

func failedCount(payload, reason string) int64 {
	return metrics.ForwardFailed.Value(payload, reason)
}

// statusBackend starts a backend that answers every request with status,
// the given headers and body, and counts the requests it gets.
func statusBackend(t *testing.T, status int, headers map[string]string, body string) (*httptest.Server, *atomic.Int32) {
	t.Helper()
	var attempts atomic.Int32
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		attempts.Add(1)
		for k, v := range headers {
			w.Header().Set(k, v)
		}
		w.WriteHeader(status)
		io.WriteString(w, body)
	}))
	t.Cleanup(backend.Close)
	return backend, &attempts
}

func TestForwardingSink_202_ReturnsNilAndCountsDelivered(t *testing.T) {
	backend, attempts := statusBackend(t, http.StatusAccepted, nil, "")
	sink := mustNewSink(t, testConfig(backend.URL))
	defer sink.Shutdown(context.Background())
	before := metrics.ForwardDelivered.Value("manifest")

	if _, err := sink.AcceptManifest(context.Background(), manifestWithService("svc")); err != nil {
		t.Fatalf("AcceptManifest: %v", err)
	}
	if got := metrics.ForwardDelivered.Value("manifest"); got != before+1 {
		t.Errorf("delivered = %d, want %d", got, before+1)
	}
	if got := attempts.Load(); got != 1 {
		t.Errorf("attempts = %d, want 1", got)
	}
}

// TestForwardingSink_Non202_2xx_IsNotAConfirmation pins that a 2xx other
// than 202 returns a plain error, which the handler answers with 503, and
// is counted as a failure, not a delivery.
func TestForwardingSink_Non202_2xx_IsNotAConfirmation(t *testing.T) {
	for _, status := range []int{http.StatusOK, http.StatusNoContent, http.StatusPartialContent} {
		t.Run(http.StatusText(status), func(t *testing.T) {
			backend, attempts := statusBackend(t, status, nil, "")
			sink := mustNewSink(t, testConfig(backend.URL))
			defer sink.Shutdown(context.Background())
			delivered := metrics.ForwardDelivered.Value("manifest")
			failed := failedCount("manifest", "unexpected_status")

			_, err := sink.AcceptManifest(context.Background(), manifestWithService("svc"))

			if err == nil {
				t.Fatal("AcceptManifest returned nil for a 2xx that is not 202")
			}
			var statusErr *ingest.StatusError
			if errors.As(err, &statusErr) {
				t.Errorf("error is a *StatusError (%d); the handler would relay it instead of answering 503", statusErr.Status)
			}
			if got := metrics.ForwardDelivered.Value("manifest"); got != delivered {
				t.Errorf("delivered = %d, want unchanged at %d", got, delivered)
			}
			if got := failedCount("manifest", "unexpected_status"); got != failed+1 {
				t.Errorf("failed(unexpected_status) = %d, want %d", got, failed+1)
			}
			if got := attempts.Load(); got != 1 {
				t.Errorf("attempts = %d, want 1", got)
			}
		})
	}
}

// TestForwardingSink_Non202_2xx_LogsAtErrorOncePerStatus pins that the log
// line is an ERROR with the status, and that a burst writes one line.
func TestForwardingSink_Non202_2xx_LogsAtErrorOncePerStatus(t *testing.T) {
	backend, _ := statusBackend(t, http.StatusOK, nil, "")
	var logs syncBuffer
	cfg := testConfig(backend.URL)
	cfg.Logger = slog.New(slog.NewJSONHandler(&logs, nil))
	sink := mustNewSink(t, cfg)
	defer sink.Shutdown(context.Background())

	for range 3 {
		_, _ = sink.AcceptManifest(context.Background(), manifestWithService("svc"))
	}

	var lines []string
	for _, line := range strings.Split(strings.TrimSpace(logs.String()), "\n") {
		if strings.Contains(line, "other than 202") {
			lines = append(lines, line)
		}
	}
	if len(lines) != 1 {
		t.Fatalf("log lines after 3 answers = %d, want 1: %q", len(lines), logs.String())
	}
	if !strings.Contains(lines[0], `"level":"ERROR"`) || !strings.Contains(lines[0], `"status":200`) {
		t.Errorf("line = %s, want an ERROR with status 200", lines[0])
	}

	now := time.Now().Add(unexpectedStatusLogInterval)
	sink.statusLog = ratelog.New(unexpectedStatusLogInterval, func() time.Time { return now })
	_, _ = sink.AcceptManifest(context.Background(), manifestWithService("svc"))
	if got := strings.Count(logs.String(), "other than 202"); got != 2 {
		t.Errorf("log lines after a fresh limiter = %d, want 2", got)
	}
}

func TestForwardingSink_429_ReturnsStatusErrorWithBackendRetryAfter(t *testing.T) {
	backend, attempts := statusBackend(t, http.StatusTooManyRequests, map[string]string{"Retry-After": "17"}, `{"error":"rate_limited"}`)
	sink := mustNewSink(t, testConfig(backend.URL))
	defer sink.Shutdown(context.Background())
	before := failedCount("manifest", "rate_limited")

	_, err := sink.AcceptManifest(context.Background(), manifestWithService("svc"))

	var statusErr *ingest.StatusError
	if !errors.As(err, &statusErr) {
		t.Fatalf("error = %v, want an *ingest.StatusError", err)
	}
	if statusErr.Status != http.StatusTooManyRequests || statusErr.RetryAfter != "17" || statusErr.Code != "rate_limited" {
		t.Errorf("StatusError = %+v, want 429, Retry-After 17, code rate_limited", statusErr)
	}
	if got := failedCount("manifest", "rate_limited"); got != before+1 {
		t.Errorf("failed(rate_limited) = %d, want %d", got, before+1)
	}
	if got := attempts.Load(); got != 1 {
		t.Errorf("attempts = %d, want 1 (the sink retries nothing)", got)
	}
}

func TestForwardingSink_429WithoutRetryAfter_PassesNone(t *testing.T) {
	backend, _ := statusBackend(t, http.StatusTooManyRequests, nil, "")
	sink := mustNewSink(t, testConfig(backend.URL))
	defer sink.Shutdown(context.Background())

	_, err := sink.AcceptManifest(context.Background(), manifestWithService("svc"))

	var statusErr *ingest.StatusError
	if !errors.As(err, &statusErr) || statusErr.RetryAfter != "" {
		t.Fatalf("error = %v, want a StatusError with no Retry-After", err)
	}
}

func TestForwardingSink_Other4xx_ReturnsStatusErrorWithTheSameStatus(t *testing.T) {
	for _, status := range []int{400, 401, 403, 404, 409, 413, 422} {
		t.Run(http.StatusText(status), func(t *testing.T) {
			backend, attempts := statusBackend(t, status, nil, `{"error":"redaction_required"}`)
			sink := mustNewSink(t, testConfig(backend.URL))
			defer sink.Shutdown(context.Background())
			before := failedCount("deltas", "rejected")

			_, err := sink.AcceptDeltaBatch(context.Background(), &otherlodepb.DeltaBatch{Resource: manifestWithService("svc").GetResource()})

			var statusErr *ingest.StatusError
			if !errors.As(err, &statusErr) {
				t.Fatalf("error = %v, want an *ingest.StatusError", err)
			}
			if statusErr.Status != status || statusErr.RetryAfter != "" || statusErr.Code != "redaction_required" {
				t.Errorf("StatusError = %+v, want status %d, no Retry-After, code redaction_required", statusErr, status)
			}
			if got := failedCount("deltas", "rejected"); got != before+1 {
				t.Errorf("failed(rejected) = %d, want %d", got, before+1)
			}
			if got := attempts.Load(); got != 1 {
				t.Errorf("attempts = %d, want 1", got)
			}
		})
	}
}

func TestForwardingSink_5xxAndRedirect_ReturnPlainError(t *testing.T) {
	for _, status := range []int{302, 500, 502, 503, 504} {
		t.Run(http.StatusText(status), func(t *testing.T) {
			var target atomic.Int32
			targetServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				target.Add(1)
				w.WriteHeader(http.StatusAccepted)
			}))
			defer targetServer.Close()
			headers := map[string]string{"Retry-After": "3"}
			if status == 302 {
				headers["Location"] = targetServer.URL
			}
			backend, attempts := statusBackend(t, status, headers, "")
			sink := mustNewSink(t, testConfig(backend.URL))
			defer sink.Shutdown(context.Background())
			before := failedCount("manifest", "server_error")

			_, err := sink.AcceptManifest(context.Background(), manifestWithService("svc"))

			if err == nil {
				t.Fatal("AcceptManifest returned nil for a status outside 2xx")
			}
			var statusErr *ingest.StatusError
			if errors.As(err, &statusErr) {
				t.Fatalf("error = %v, want a plain error so the handler answers 503", err)
			}
			if got := failedCount("manifest", "server_error"); got != before+1 {
				t.Errorf("failed(server_error) = %d, want %d", got, before+1)
			}
			if got := attempts.Load(); got != 1 {
				t.Errorf("attempts = %d, want 1 (the sink retries nothing)", got)
			}
			if got := target.Load(); got != 0 {
				t.Errorf("redirect target got %d requests, want 0", got)
			}
		})
	}
}

func TestForwardingSink_NoConnection_ReturnsPlainErrorCountedAsTransport(t *testing.T) {
	backend := httptest.NewServer(http.NotFoundHandler())
	url := backend.URL
	backend.Close()
	sink := mustNewSink(t, testConfig(url))
	defer sink.Shutdown(context.Background())
	before := failedCount("manifest", "transport")

	_, err := sink.AcceptManifest(context.Background(), manifestWithService("svc"))

	var statusErr *ingest.StatusError
	if err == nil || errors.As(err, &statusErr) {
		t.Fatalf("error = %v, want a plain error", err)
	}
	if got := failedCount("manifest", "transport"); got != before+1 {
		t.Errorf("failed(transport) = %d, want %d", got, before+1)
	}
}

func TestForwardingSink_Timeout_ReturnsPlainErrorAfterOneAttempt(t *testing.T) {
	var attempts atomic.Int32
	release := make(chan struct{})
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		attempts.Add(1)
		_, _ = io.Copy(io.Discard, r.Body)
		select {
		case <-release:
		case <-r.Context().Done():
		}
	}))
	defer backend.Close()
	defer close(release)
	cfg := testConfig(backend.URL)
	cfg.RequestTimeout = 100 * time.Millisecond
	sink := mustNewSink(t, cfg)
	defer sink.Shutdown(context.Background())
	before := failedCount("manifest", "timeout")

	start := time.Now()
	_, err := sink.AcceptManifest(context.Background(), manifestWithService("svc"))
	elapsed := time.Since(start)

	var statusErr *ingest.StatusError
	if err == nil || errors.As(err, &statusErr) {
		t.Fatalf("error = %v, want a plain error", err)
	}
	if elapsed < 100*time.Millisecond || elapsed > 2*time.Second {
		t.Errorf("Accept took %v, want about the 100ms request timeout", elapsed)
	}
	if got := failedCount("manifest", "timeout"); got != before+1 {
		t.Errorf("failed(timeout) = %d, want %d", got, before+1)
	}
	if got := attempts.Load(); got != 1 {
		t.Errorf("attempts = %d, want 1", got)
	}
}

func TestConfig_DefaultRequestTimeoutIsEightSeconds(t *testing.T) {
	if got := (Config{}).withDefaults().RequestTimeout; got != 8*time.Second {
		t.Fatalf("default RequestTimeout = %v, want 8s", got)
	}
}

func TestForwardingSink_RequestContextEnds_StopsTheForward(t *testing.T) {
	entered := make(chan struct{}, 1)
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.Copy(io.Discard, r.Body)
		entered <- struct{}{}
		<-r.Context().Done()
	}))
	defer backend.Close()
	cfg := testConfig(backend.URL)
	cfg.RequestTimeout = time.Hour
	sink := mustNewSink(t, cfg)
	defer sink.Shutdown(context.Background())
	before := failedCount("manifest", "canceled")

	ctx, cancel := context.WithCancel(context.Background())
	errc := make(chan error, 1)
	go func() {
		_, err := sink.AcceptManifest(ctx, manifestWithService("svc"))
		errc <- err
	}()
	waitEntered(t, entered)
	cancel()

	select {
	case err := <-errc:
		if err == nil {
			t.Fatal("AcceptManifest returned nil after its context ended")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("AcceptManifest did not return after its context ended")
	}
	if got := failedCount("manifest", "canceled"); got != before+1 {
		t.Errorf("failed(canceled) = %d, want %d", got, before+1)
	}
}

func TestForwardingSink_FailureNamesTheServiceAndTheBackendCode(t *testing.T) {
	backend, _ := statusBackend(t, http.StatusForbidden, nil, `{"error":"redaction_required"}`)
	sink := mustNewSink(t, testConfig(backend.URL))
	defer sink.Shutdown(context.Background())
	resource := &otherlodepb.ResourceAttributes{ServiceName: "demo-service", ServiceInstanceId: "instance-1", RunId: "run-1"}
	resource.SetServiceNamespace("team-a")

	_, err := sink.AcceptDeltaBatch(context.Background(), &otherlodepb.DeltaBatch{Resource: resource})

	if err == nil {
		t.Fatal("AcceptDeltaBatch returned nil")
	}
	for _, want := range []string{`namespace "team-a"`, `service "demo-service"`, `instance "instance-1"`, "403 Forbidden"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %q does not contain %q", err, want)
		}
	}
}

func TestForwardingSink_StatusErrorCode_NoneWhenBodyHasNoCode(t *testing.T) {
	for name, body := range map[string]string{
		"empty body":     "",
		"not JSON":       "<html>forbidden</html>",
		"no error field": `{"message":"forbidden"}`,
		"error not text": `{"error":42}`,
	} {
		t.Run(name, func(t *testing.T) {
			backend, _ := statusBackend(t, http.StatusForbidden, nil, body)
			sink := mustNewSink(t, testConfig(backend.URL))
			defer sink.Shutdown(context.Background())

			_, err := sink.AcceptManifest(context.Background(), manifestWithService("svc"))

			var statusErr *ingest.StatusError
			if !errors.As(err, &statusErr) || statusErr.Code != "" {
				t.Fatalf("error = %v, want a StatusError with no code", err)
			}
		})
	}
}

func TestForwardingSink_MarshalFailure_ReturnsBadRequest(t *testing.T) {
	backend, attempts := statusBackend(t, http.StatusAccepted, nil, "")
	sink := mustNewSink(t, testConfig(backend.URL))
	defer sink.Shutdown(context.Background())
	before := failedCount("manifest", "marshal")

	_, err := sink.AcceptManifest(context.Background(), manifestWithService("bad\xff"))

	var statusErr *ingest.StatusError
	if !errors.As(err, &statusErr) || statusErr.Status != http.StatusBadRequest {
		t.Fatalf("error = %v, want a 400 StatusError", err)
	}
	if got := failedCount("manifest", "marshal"); got != before+1 {
		t.Errorf("failed(marshal) = %d, want %d", got, before+1)
	}
	if got := attempts.Load(); got != 0 {
		t.Errorf("backend got %d requests, want 0", got)
	}
}

func TestCleanRetryAfter(t *testing.T) {
	long := strings.Repeat("9", maxRetryAfterBytes+1)
	for in, want := range map[string]string{
		"7":                             "7",
		" 120 ":                         "120",
		"Wed, 21 Oct 2026 07:28:00 GMT": "Wed, 21 Oct 2026 07:28:00 GMT",
		"":                              "",
		"1\r\nX: y":                     "",
		long:                            "",
	} {
		if got := cleanRetryAfter(in); got != want {
			t.Errorf("cleanRetryAfter(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestForwardingSink_Shutdown_WaitsForRequestsInFlight(t *testing.T) {
	url, entered, release := blockingBackend(t)
	cfg := testConfig(url)
	cfg.RequestTimeout = time.Hour
	sink := mustNewSink(t, cfg)

	acceptErr := make(chan error, 1)
	go func() {
		_, err := sink.AcceptManifest(context.Background(), manifestWithService("svc"))
		acceptErr <- err
	}()
	waitEntered(t, entered)

	shutdownDone := make(chan struct{})
	go func() {
		sink.Shutdown(context.Background())
		close(shutdownDone)
	}()
	select {
	case <-shutdownDone:
		t.Fatal("Shutdown returned while a request was in flight")
	case <-time.After(100 * time.Millisecond):
	}

	release()
	select {
	case err := <-acceptErr:
		if err != nil {
			t.Fatalf("in-flight Accept error = %v, want nil: it finishes during Shutdown", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("in-flight Accept did not finish")
	}
	select {
	case <-shutdownDone:
	case <-time.After(2 * time.Second):
		t.Fatal("Shutdown did not return after the request finished")
	}
}

func TestForwardingSink_Shutdown_NewRequestsGetErrShuttingDown(t *testing.T) {
	backend, attempts := statusBackend(t, http.StatusAccepted, nil, "")
	sink := mustNewSink(t, testConfig(backend.URL))
	sink.Shutdown(context.Background())
	before := failedCount("manifest", "shutting_down")

	_, err := sink.AcceptManifest(context.Background(), manifestWithService("svc"))

	if !errors.Is(err, ErrShuttingDown) {
		t.Fatalf("error = %v, want it to wrap ErrShuttingDown", err)
	}
	var statusErr *ingest.StatusError
	if errors.As(err, &statusErr) {
		t.Fatalf("error = %v, want a plain error so the handler answers 503", err)
	}
	if got := failedCount("manifest", "shutting_down"); got != before+1 {
		t.Errorf("failed(shutting_down) = %d, want %d", got, before+1)
	}
	if got := attempts.Load(); got != 0 {
		t.Errorf("backend got %d requests after Shutdown, want 0", got)
	}
}

func TestForwardingSink_Shutdown_DeadlineCancelsRequestsInFlight(t *testing.T) {
	url, entered, _ := blockingBackend(t)
	cfg := testConfig(url)
	cfg.RequestTimeout = time.Hour
	sink := mustNewSink(t, cfg)

	acceptErr := make(chan error, 1)
	go func() {
		_, err := sink.AcceptManifest(context.Background(), manifestWithService("svc"))
		acceptErr <- err
	}()
	waitEntered(t, entered)

	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	done := make(chan struct{})
	go func() {
		sink.Shutdown(ctx)
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("Shutdown did not return after its deadline")
	}
	select {
	case err := <-acceptErr:
		if err == nil {
			t.Fatal("Accept returned nil although Shutdown cancelled it")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Accept did not return after Shutdown cancelled it")
	}
}

func TestForwardingSink_ForwardCap_WaitsThenFailsBusy(t *testing.T) {
	backend, attempts := statusBackend(t, http.StatusAccepted, nil, "")
	cfg := testConfig(backend.URL)
	cfg.MaxConcurrentForwards = 1
	cfg.RequestTimeout = 80 * time.Millisecond
	sink := mustNewSink(t, cfg)
	defer sink.Shutdown(context.Background())
	sink.slots <- struct{}{}
	before := failedCount("manifest", "busy")

	start := time.Now()
	_, err := sink.AcceptManifest(context.Background(), manifestWithService("svc-2"))

	if !errors.Is(err, errNoForwardSlot) {
		t.Fatalf("error = %v, want one wrapping errNoForwardSlot", err)
	}
	var statusErr *ingest.StatusError
	if errors.As(err, &statusErr) {
		t.Errorf("error is a *StatusError; the handler would not answer 503")
	}
	if elapsed := time.Since(start); elapsed < 60*time.Millisecond || elapsed > 2*time.Second {
		t.Errorf("waited %v, want about 80ms", elapsed)
	}
	if got := failedCount("manifest", "busy"); got != before+1 {
		t.Errorf("failed(busy) = %d, want %d", got, before+1)
	}
	if got := attempts.Load(); got != 0 {
		t.Errorf("attempts = %d, want 0", got)
	}
}

func TestForwardingSink_ForwardCap_ContextDeadlineEndsTheWait(t *testing.T) {
	url, entered, release := blockingBackend(t)
	cfg := testConfig(url)
	cfg.MaxConcurrentForwards = 1
	cfg.RequestTimeout = time.Hour
	sink := mustNewSink(t, cfg)
	defer sink.Shutdown(context.Background())
	defer release()

	go sink.AcceptManifest(context.Background(), manifestWithService("svc-1"))
	waitEntered(t, entered)

	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	start := time.Now()
	_, err := sink.AcceptManifest(ctx, manifestWithService("svc-2"))

	if !errors.Is(err, errNoForwardSlot) {
		t.Fatalf("error = %v, want one wrapping errNoForwardSlot", err)
	}
	if elapsed := time.Since(start); elapsed > 2*time.Second {
		t.Errorf("waited %v, want about 50ms", elapsed)
	}
}

func TestForwardingSink_SlotFreedAfterEveryOutcome(t *testing.T) {
	for _, status := range []int{http.StatusAccepted, http.StatusBadRequest, http.StatusInternalServerError, http.StatusOK} {
		backend, _ := statusBackend(t, status, nil, "")
		cfg := testConfig(backend.URL)
		cfg.MaxConcurrentForwards = 1
		cfg.RequestTimeout = 50 * time.Millisecond
		sink := mustNewSink(t, cfg)
		for range 3 {
			_, err := sink.AcceptManifest(context.Background(), manifestWithService("svc"))
			if errors.Is(err, errNoForwardSlot) {
				t.Fatalf("status %d: the forward slot was not freed", status)
			}
		}
		sink.Shutdown(context.Background())
	}
}

func TestConfig_MaxConcurrentForwards_DefaultsAndSizesTheClient(t *testing.T) {
	cfg := Config{}.withDefaults()
	if cfg.MaxConcurrentForwards != 32 {
		t.Errorf("default = %d, want 32", cfg.MaxConcurrentForwards)
	}
	cfg = Config{MaxConcurrentForwards: 7}.withDefaults()
	transport, ok := cfg.HTTPClient.Transport.(*http.Transport)
	if !ok {
		t.Fatalf("transport is %T", cfg.HTTPClient.Transport)
	}
	if transport.MaxIdleConnsPerHost != 7 {
		t.Errorf("MaxIdleConnsPerHost = %d, want 7", transport.MaxIdleConnsPerHost)
	}
	if transport.MaxIdleConns < 7 {
		t.Errorf("MaxIdleConns = %d, want at least 7", transport.MaxIdleConns)
	}
	sink := mustNewSink(t, Config{URL: "http://127.0.0.1:1", MaxConcurrentForwards: 7})
	defer sink.Shutdown(context.Background())
	if cap(sink.slots) != 7 {
		t.Errorf("slots = %d, want 7", cap(sink.slots))
	}
}

func TestValidateURL_NeverQuotesAValueWithUserInfo(t *testing.T) {
	for _, raw := range []string{
		"user:s3cret@backend.example.com",
		"https://user:s3cret@",
		"http://user:s3cret@[::1",
		"ftp://user:s3cret@backend.example.com",
		"https://user:s3cret@backend.example.com",
		"https://user:s3/cret@backend.example.com",
		"https://user:s3?cret@backend.example.com",
		"https://user:s3#cret@backend.example.com",
		"https://user:s3%zzcret@backend.example.com",
	} {
		_, _, err := ValidateURL(raw)
		if err == nil {
			t.Fatalf("ValidateURL(%q): expected an error, got nil", raw)
		}
		for _, part := range []string{"s3", "cret", "zz"} {
			if strings.Contains(err.Error(), part) {
				t.Errorf("ValidateURL(%q) error %q holds part of the password", raw, err.Error())
			}
		}
	}
}

func TestValidateURL_QuotesAValueWithoutUserInfo(t *testing.T) {
	tests := []struct{ raw, want string }{
		{"collector.example.com", `forward URL "collector.example.com": scheme must be http or https`},
		{"https://", `forward URL "https://": missing host`},
		{"http://[::1", `forward URL "http://[::1": missing ']' in host`},
	}
	for _, tt := range tests {
		_, _, err := ValidateURL(tt.raw)
		if err == nil || err.Error() != tt.want {
			t.Errorf("ValidateURL(%q) error = %v, want %q", tt.raw, err, tt.want)
		}
	}
}
