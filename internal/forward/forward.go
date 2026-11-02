// Package forward relays decoded ingest payloads to a backend. It uses the
// shape this collector accepts from the agent: the same content type and
// paths, with a protobuf body. It plays the role of an OTel Collector
// exporter. It marshals the decoded message again, so the body it sends
// holds what the processors left, not the bytes the agent sent. It changes
// no field itself.
package forward

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"

	"google.golang.org/protobuf/proto"

	otherlodepb "buf.build/gen/go/otherlode/otherlode/protocolbuffers/go/otherlode/v1"

	"github.com/otherlodehq/otherlode-collector/ingest"
	"github.com/otherlodehq/otherlode-collector/internal/ratelog"
	"github.com/otherlodehq/otherlode-collector/metrics"
)

const (
	defaultRequestTimeout = 8 * time.Second

	// defaultMaxConcurrentForwards is how many requests may be in flight to
	// the backend at once. The default client keeps that many idle
	// connections.
	defaultMaxConcurrentForwards = 32

	// unexpectedStatusLogInterval is the shortest gap between two log lines
	// for a 2xx answer other than 202.
	unexpectedStatusLogInterval = time.Minute

	// maxResponseBytes bounds how much of a backend response body is read
	// before the connection is released. A 2xx carries no body worth
	// reading; the read only exists to let the connection be reused.
	maxResponseBytes = 64 << 10

	// maxErrorCodeBytes bounds the backend error code a failure log carries.
	maxErrorCodeBytes = 64

	// maxRetryAfterBytes bounds the Retry-After value passed on to the
	// agent.
	maxRetryAfterBytes = 64

	// RedactionHeader is the request header that carries the redaction
	// secret's fingerprint while redaction is on (ADR 0007).
	RedactionHeader = "Otherlode-Redaction"
)

// Config configures a ForwardingSink. URL is required. Every other field
// falls back to a default when left zero.
type Config struct {
	// URL is the backend's base URL, with an http or https scheme and a
	// host. NewForwardingSink rejects a URL with a query, a fragment or
	// user info. ForwardingSink posts the re-marshaled payload to
	// URL+ingest.DeltaBatchPath, URL+ingest.ManifestPath,
	// URL+ingest.StaticBaselinePath and URL+ingest.ClassListingPath.
	// Trailing slashes are removed so the joined path has a single
	// separator.
	URL string

	// AuthToken yields the key that authenticates the collector to the
	// backend. The sink asks for it before every request and sends it as
	// "Authorization: Bearer <key>" when it is not empty. A nil AuthToken
	// sends no header. This is a separate trust boundary from the
	// agent-facing bearer token the collector itself checks: the agent
	// authenticates to the collector, the collector authenticates to the
	// backend, and the two need not share a secret.
	AuthToken TokenSource

	// RedactionFingerprint, when not empty, goes on every request in the
	// RedactionHeader header. It is the fingerprint of the redaction
	// secret, which the collector computes once at startup, never the
	// secret. A backend can then refuse a payload that no redacting
	// collector sent, such as one an agent posted to it directly. Leave it
	// empty while redaction is off, so no header is sent. It must be a
	// valid header value.
	RedactionFingerprint string

	// HTTPClient sends the relayed requests. Defaults to a client that
	// keeps idle connections to the backend. Each request carries its own
	// timeout via context, so the client itself does not need one. The
	// default client does not follow redirects: a 3xx response is a
	// failure. A client you supply should do the same, because a followed
	// redirect can turn the POST into a GET and a 2xx from the target
	// would count as stored.
	HTTPClient *http.Client

	// Logger receives the Warn logs for the notices the backend sends
	// with an accepted payload. Defaults to slog.Default().
	Logger *slog.Logger

	// RequestTimeout bounds the one HTTP attempt made for a payload.
	// Defaults to defaultRequestTimeout.
	RequestTimeout time.Duration

	// MaxConcurrentForwards caps the requests in flight to the backend. A
	// forward that finds no free slot waits for one until its context ends
	// or RequestTimeout passes, then fails. Defaults to
	// defaultMaxConcurrentForwards. The default client keeps this many idle
	// connections to the backend.
	MaxConcurrentForwards int
}

// TokenSource yields the key a ForwardingSink sends to the backend.
// The sink calls Token before every request, so the key can change
// while the sink runs. Token must be safe for concurrent use.
type TokenSource interface {
	Token() string
}

// StaticToken is a TokenSource whose key never changes.
type StaticToken string

// Token returns the fixed key.
func (t StaticToken) Token() string { return string(t) }

// authToken returns the key to send, or "" when there is none.
func (c Config) authToken() string {
	if c.AuthToken == nil {
		return ""
	}
	return c.AuthToken.Token()
}

func (c Config) withDefaults() Config {
	if c.MaxConcurrentForwards <= 0 {
		c.MaxConcurrentForwards = defaultMaxConcurrentForwards
	}
	if c.HTTPClient == nil {
		c.HTTPClient = newHTTPClient(c.MaxConcurrentForwards)
	}
	if c.Logger == nil {
		c.Logger = slog.Default()
	}
	if c.RequestTimeout <= 0 {
		c.RequestTimeout = defaultRequestTimeout
	}
	return c
}

// newHTTPClient returns a client for one backend host that keeps up to
// conns idle connections to it. It keeps
// http.DefaultTransport's proxy function, so HTTPS_PROXY, HTTP_PROXY and
// NO_PROXY apply. It does not follow redirects, so a 3xx response reaches
// send as a failure.
func newHTTPClient(conns int) *http.Client {
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.MaxIdleConnsPerHost = conns
	if transport.MaxIdleConns < conns {
		transport.MaxIdleConns = conns
	}
	return &http.Client{
		Transport: transport,
		CheckRedirect: func(*http.Request, []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}
}

// outbound is a payload already marshaled to wire bytes, ready to POST.
// owner names the instance in errors, so an operator can tell whose data
// was not stored.
type outbound struct {
	owner instance
	path  string
	body  []byte
}

// ErrShuttingDown is the error AcceptDeltaBatch, AcceptManifest,
// AcceptStaticBaseline and AcceptClassListing return once Shutdown has
// started. The ingest handler answers 503 for it, so the agent keeps its
// counts and sends the payload again on its next flush.
var ErrShuttingDown = errors.New("forward: sink is shutting down")

// ForwardingSink is a Sink that relays decoded payloads to a backend. An
// Accept method posts the payload and waits for the backend's answer, in
// one attempt bounded by Config.RequestTimeout. It returns nil only when
// the backend answered 202, so the handler tells the agent the payload is
// stored only when it is. Any other 2xx is an error: it comes from a wrong
// URL or a proxy, not from Otherlode.
//
// An Accept method marshals the payload, waits for a forward slot and then
// calls ingest.ReleaseDecodeSlot, so the request holds no decode slot
// during the network call. A request that waits for a forward slot still
// holds its decode slot, so a full forward cap pushes back on decoding.
// Requests in flight to the backend are capped by
// Config.MaxConcurrentForwards.
//
// For any other outcome the Accept method returns an error. A 429 or any
// other 4xx from the backend comes back as an *ingest.StatusError with the
// backend's status, and the handler answers with that status. A 5xx, a
// redirect, a timeout or a transport error comes back as a plain error,
// and the handler answers 503 with Retry-After. The sink retries
// nothing: the agent sends the payload again on its next flush. An Accept
// method returns no notices, since the backend's notices go to the log.
type ForwardingSink struct {
	cfg Config

	// ctx bounds every attempt. Shutdown cancels it once its own deadline
	// passes, so a slow in-flight request cannot outlive the shutdown
	// budget.
	ctx    context.Context
	cancel context.CancelFunc

	noticeLog *noticeLog

	// slots is a counting semaphore for requests in flight to the backend.
	slots chan struct{}

	// statusLog limits the log lines for a 2xx answer other than 202.
	statusLog *ratelog.Limiter

	// mu holds begin's shutdown check and its wg.Add together against
	// Shutdown setting closed. A request that passed the check is counted
	// before Shutdown starts waiting, not after it.
	mu           sync.RWMutex
	closed       bool
	shutdownOnce sync.Once
	wg           sync.WaitGroup
}

var _ ingest.Sink = (*ForwardingSink)(nil)

// NewForwardingSink returns a ready-to-use ForwardingSink. Call Shutdown
// to wait for the requests in flight. It returns an error for a cfg.URL
// that could never reach a backend, so a bad setting stops the collector
// at startup rather than failing every payload.
func NewForwardingSink(cfg Config) (*ForwardingSink, error) {
	cfg = cfg.withDefaults()

	baseURL, scheme, err := validateURL(cfg.URL)
	if err != nil {
		return nil, err
	}
	cfg.URL = baseURL
	for i := 0; i < len(cfg.RedactionFingerprint); i++ {
		if c := cfg.RedactionFingerprint[i]; c < 0x21 || c > 0x7e {
			return nil, fmt.Errorf("redaction fingerprint holds a byte a header value cannot carry at byte %d", i)
		}
	}
	if scheme == "http" && cfg.authToken() != "" {
		cfg.Logger.Warn("forward URL uses plain http; the backend auth token is sent unencrypted", "url", cfg.URL)
	}

	ctx, cancel := context.WithCancel(context.Background())
	return &ForwardingSink{
		cfg:       cfg,
		ctx:       ctx,
		cancel:    cancel,
		noticeLog: newNoticeLog(time.Now, maxNoticeKeys),
		slots:     make(chan struct{}, cfg.MaxConcurrentForwards),
		statusLog: ratelog.New(unexpectedStatusLogInterval, time.Now),
	}, nil
}

// validateURL checks that raw is an absolute http or https URL with a
// host and no query, fragment or user info. The sink joins paths onto the
// URL by string concatenation, so a query or fragment would end up in the
// middle of the request URL. User info would be logged as written. It
// returns raw with trailing slashes removed, and the lowercase scheme.
func validateURL(raw string) (base, scheme string, err error) {
	if raw == "" {
		return "", "", errors.New("forward URL is empty")
	}
	u, err := url.Parse(raw)
	if err != nil {
		return "", "", fmt.Errorf("forward URL %q: %w", raw, err)
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return "", "", fmt.Errorf("forward URL %q: scheme must be http or https", raw)
	}
	if u.Host == "" {
		return "", "", fmt.Errorf("forward URL %q: missing host", raw)
	}
	if u.User != nil {
		return "", "", errors.New("forward URL must not contain user info")
	}
	if u.RawQuery != "" || u.ForceQuery {
		return "", "", errors.New("forward URL must not contain a query")
	}
	if u.Fragment != "" || strings.Contains(raw, "#") {
		return "", "", errors.New("forward URL must not contain a fragment")
	}
	return strings.TrimRight(raw, "/"), u.Scheme, nil
}

// instance names the service instance a payload came from: the service's
// identity, which is its namespace and name, plus the instance ID. An
// empty namespace is the unspecified one.
type instance struct {
	namespace string
	service   string
	id        string
}

// instanceOf reads the instance from res. It trims the namespace and
// service name, as the backend does when it keys a service, so one
// service never splits in two. A nil res gives the zero instance.
func instanceOf(res *otherlodepb.ResourceAttributes) instance {
	return instance{
		namespace: strings.TrimSpace(res.GetServiceNamespace()),
		service:   strings.TrimSpace(res.GetServiceName()),
		id:        res.GetServiceInstanceId(),
	}
}

// String names the instance for people to read in errors and logs. It
// quotes each part, so a reader can tell where one part ends.
func (i instance) String() string {
	if i.namespace == "" {
		return fmt.Sprintf("service %q instance %q", i.service, i.id)
	}
	return fmt.Sprintf("namespace %q service %q instance %q", i.namespace, i.service, i.id)
}

// AcceptDeltaBatch posts batch to the backend and waits for its answer.
func (s *ForwardingSink) AcceptDeltaBatch(ctx context.Context, batch *otherlodepb.DeltaBatch) ([]ingest.Notice, error) {
	return nil, s.forward(ctx, batch, instanceOf(batch.GetResource()), ingest.DeltaBatchPath)
}

// AcceptManifest posts manifest to the backend and waits for its answer.
func (s *ForwardingSink) AcceptManifest(ctx context.Context, manifest *otherlodepb.ProbeManifest) ([]ingest.Notice, error) {
	return nil, s.forward(ctx, manifest, instanceOf(manifest.GetResource()), ingest.ManifestPath)
}

// AcceptStaticBaseline posts baseline to the backend and waits for its
// answer.
func (s *ForwardingSink) AcceptStaticBaseline(ctx context.Context, baseline *otherlodepb.StaticBaseline) ([]ingest.Notice, error) {
	return nil, s.forward(ctx, baseline, instanceOf(baseline.GetResource()), ingest.StaticBaselinePath)
}

// AcceptClassListing posts listing to the backend and waits for its
// answer.
func (s *ForwardingSink) AcceptClassListing(ctx context.Context, listing *otherlodepb.ClassListing) ([]ingest.Notice, error) {
	return nil, s.forward(ctx, listing, instanceOf(listing.GetResource()), ingest.ClassListingPath)
}

// begin counts a request in flight. It reports false once Shutdown has
// started.
func (s *ForwardingSink) begin() bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.closed {
		return false
	}
	s.wg.Add(1)
	return true
}

// forward marshals msg and posts it to path. It returns nil when the
// backend answered 202. ctx is the agent's request: when it ends, the post
// ends. The forward ends by the deadline of ctx too, so it shares the
// request's time budget.
func (s *ForwardingSink) forward(ctx context.Context, msg proto.Message, owner instance, path string) error {
	payload := ingest.PayloadLabel(path)
	if !s.begin() {
		metrics.ForwardFailed.Inc(payload, "shutting_down")
		return ingest.WithReason(fmt.Errorf("%w (%s)", ErrShuttingDown, owner), "shutting_down")
	}
	defer s.wg.Done()

	body, err := proto.Marshal(msg)
	if err != nil {
		metrics.ForwardFailed.Inc(payload, "marshal")
		return ingest.WithReason(&ingest.StatusError{Status: http.StatusBadRequest, Err: fmt.Errorf("marshal payload for %s: %w", owner, err)}, "marshal")
	}
	item := outbound{owner: owner, path: path, body: body}

	ctx, stop := context.WithCancel(ctx)
	defer stop()
	defer context.AfterFunc(s.ctx, stop)()

	if reason, err := s.acquire(ctx, owner); err != nil {
		metrics.ForwardFailed.Inc(payload, reason)
		return ingest.WithReason(err, reason)
	}
	defer func() { <-s.slots }()
	ingest.ReleaseDecodeSlot(ctx)

	reason, err := s.send(ctx, item)
	if err != nil {
		metrics.ForwardFailed.Inc(payload, reason)
		return ingest.WithReason(err, reason)
	}
	metrics.ForwardDelivered.Inc(payload)
	return nil
}

// acquire waits for a free forward slot until ctx ends or
// cfg.RequestTimeout passes. It returns the reason the failure is counted
// under and the error when it took no slot.
func (s *ForwardingSink) acquire(ctx context.Context, owner instance) (reason string, err error) {
	select {
	case s.slots <- struct{}{}:
		return "", nil
	default:
	}
	timer := time.NewTimer(s.cfg.RequestTimeout)
	defer timer.Stop()
	select {
	case s.slots <- struct{}{}:
		return "", nil
	case <-ctx.Done():
		reason = "canceled"
		if errors.Is(ctx.Err(), context.DeadlineExceeded) {
			reason = "busy"
		}
	case <-timer.C:
		reason = "busy"
	}
	return reason, fmt.Errorf("forward for %s: no forward slot came free (max %d): %w", owner, cap(s.slots), errNoForwardSlot)
}

// errNoForwardSlot is the cause of a forward that waited in vain for a
// free slot.
var errNoForwardSlot = errors.New("max concurrent forwards reached")

// send makes the one HTTP attempt, bounded by cfg.RequestTimeout and by
// ctx. It returns nil for a 202 answer. For anything else it returns the
// error the handler answers with and the reason the failure is counted
// under: "rate_limited" for a 429 and "rejected" for another 4xx, both as
// an *ingest.StatusError; "server_error" for any other status; "timeout",
// "canceled" or "transport" when no answer came; "unexpected_status" for another 2xx;
// "request" when the request could not be built.
func (s *ForwardingSink) send(ctx context.Context, item outbound) (reason string, err error) {
	reqCtx, cancel := context.WithTimeout(ctx, s.cfg.RequestTimeout)
	defer cancel()

	req, err := http.NewRequestWithContext(reqCtx, http.MethodPost, s.cfg.URL+item.path, bytes.NewReader(item.body))
	if err != nil {
		return "request", fmt.Errorf("build request for %s: %w", item.owner, err)
	}
	req.Header.Set("Content-Type", "application/x-protobuf")
	if token := s.cfg.authToken(); token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	if s.cfg.RedactionFingerprint != "" {
		req.Header.Set(RedactionHeader, s.cfg.RedactionFingerprint)
	}

	resp, err := s.cfg.HTTPClient.Do(req)
	if err != nil {
		switch {
		case errors.Is(ctx.Err(), context.Canceled):
			reason = "canceled"
		case errors.Is(reqCtx.Err(), context.DeadlineExceeded):
			reason = "timeout"
		default:
			reason = "transport"
		}
		return reason, fmt.Errorf("forward for %s: %w", item.owner, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusAccepted {
		_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, maxResponseBytes))
		s.noticed(item, resp.Header.Values(ingest.NoticeHeader))
		return "", nil
	}
	if resp.StatusCode >= 200 && resp.StatusCode < 300 {
		s.logUnexpectedStatus(item, resp.StatusCode)
		return "unexpected_status", fmt.Errorf("backend returned %s, not 202, for %s", resp.Status, item.owner)
	}
	body, _ := io.ReadAll(io.LimitReader(resp.Body, maxResponseBytes))
	backendErr := &backendError{status: resp.Status, code: errorCode(body), owner: item.owner}
	switch {
	case resp.StatusCode == http.StatusTooManyRequests:
		return "rate_limited", &ingest.StatusError{
			Status:     resp.StatusCode,
			RetryAfter: cleanRetryAfter(resp.Header.Get("Retry-After")),
			Code:       backendErr.code,
			Err:        backendErr,
		}
	case resp.StatusCode >= 400 && resp.StatusCode < 500:
		return "rejected", &ingest.StatusError{Status: resp.StatusCode, Code: backendErr.code, Err: backendErr}
	default:
		return "server_error", backendErr
	}
}

// logUnexpectedStatus logs a 2xx answer other than 202 at ERROR, at most
// once per status in an interval. Such an answer comes from a wrong URL or
// a proxy, not from Otherlode, so the payload is not known to be stored.
func (s *ForwardingSink) logUnexpectedStatus(item outbound, status int) {
	unlogged, ok := s.statusLog.Allow(strconv.Itoa(status))
	if !ok {
		return
	}
	s.cfg.Logger.Error("backend answered 2xx other than 202, so the payload is not counted as stored; check OTHERLODE_COLLECTOR_FORWARD_URL",
		"status", status, "path", item.path, "owner", item.owner.String(), "unlogged_unexpected_statuses", unlogged)
}

// backendError is a response from the backend outside 2xx. code is the
// "error" field of the backend's JSON body, as errorCode cleans it, or ""
// when the body has none.
type backendError struct {
	status string
	code   string
	owner  instance
}

func (e *backendError) Error() string {
	return "backend returned " + e.status + " for " + e.owner.String()
}

// errorCode returns the "error" string of body, a JSON object such as
// {"error":"redaction_required"}, or "" when body is not such an object.
// The backend's code goes into a log line, so errorCode keeps at most
// maxErrorCodeBytes and replaces each byte outside printable ASCII with
// '?'.
func errorCode(body []byte) string {
	var parsed struct {
		Error string `json:"error"`
	}
	if json.Unmarshal(body, &parsed) != nil {
		return ""
	}
	code := []byte(parsed.Error)
	if len(code) > maxErrorCodeBytes {
		code = code[:maxErrorCodeBytes]
	}
	for i, c := range code {
		if c < 0x20 || c > 0x7e {
			code[i] = '?'
		}
	}
	return string(code)
}

// cleanRetryAfter returns v when it is a short run of printable ASCII, and
// "" otherwise. The handler passes it to the agent as written.
func cleanRetryAfter(v string) string {
	v = strings.TrimSpace(v)
	if len(v) > maxRetryAfterBytes {
		return ""
	}
	for i := 0; i < len(v); i++ {
		if c := v[i]; c < 0x20 || c > 0x7e {
			return ""
		}
	}
	return v
}

// Shutdown stops accepting new payloads and waits for the requests in
// flight. A payload that arrives after Shutdown starts gets
// ErrShuttingDown, so the handler answers 503.
//
// ctx bounds the wait. When it ends, Shutdown cancels the requests still
// in flight, so each answers 503, and waits for them to return.
//
// Only the first call does anything. Later calls return at once.
func (s *ForwardingSink) Shutdown(ctx context.Context) {
	first := false
	s.shutdownOnce.Do(func() { first = true })
	if !first {
		return
	}
	s.mu.Lock()
	s.closed = true
	s.mu.Unlock()

	waitDone := make(chan struct{})
	go func() {
		s.wg.Wait()
		close(waitDone)
	}()
	select {
	case <-waitDone:
	case <-ctx.Done():
		s.cancel()
		<-waitDone
	}
	s.cancel()
}
