// Package forward relays decoded ingest payloads to a backend. It marshals
// each decoded message again, so the body it sends holds what the processors
// left, not the bytes the agent sent.
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

	defaultMaxConcurrentForwards = 32

	unexpectedStatusLogInterval = time.Minute

	maxResponseBytes = 64 << 10

	maxErrorCodeBytes = 64

	maxRetryAfterBytes = 64

	// RedactionHeader is the request header that carries the redaction secret's
	// fingerprint.
	RedactionHeader = "Otherlode-Redaction"
)

// Config configures a ForwardingSink. URL is required. Every other field
// falls back to a default when left zero.
type Config struct {
	// URL is the backend's base URL, with an http or https scheme and a host
	// and no query, fragment or user info. The sink removes trailing slashes
	// and posts to URL plus the ingest path of each payload.
	URL string

	// AuthToken yields the key that authenticates the collector to the
	// backend. The sink reads it before every request and sends a non-empty
	// key as "Authorization: Bearer <key>". Nil sends no header.
	AuthToken TokenSource

	// RedactionFingerprint, when not empty, goes in the RedactionHeader header
	// of every request the sink sends. It must be a valid header value.
	RedactionFingerprint string

	// HTTPClient sends the requests. The default client keeps idle
	// connections to the backend and does not follow redirects. A client you
	// supply must not follow them either, or a redirected POST can turn into
	// a GET whose 2xx counts as stored.
	HTTPClient *http.Client

	// Logger receives the logs for backend notices and unexpected statuses.
	// The default is slog.Default().
	Logger *slog.Logger

	// RequestTimeout bounds the one HTTP attempt made for a payload. It also
	// bounds the wait for a forward slot. The default is 8 seconds.
	RequestTimeout time.Duration

	// MaxConcurrentForwards caps the requests in flight to the backend. The
	// default is 32.
	MaxConcurrentForwards int
}

// TokenSource yields the key a ForwardingSink sends to the backend. The sink
// calls Token before every request. Token must be safe for concurrent use.
type TokenSource interface {
	Token() string
}

// StaticToken is a TokenSource whose key never changes.
type StaticToken string

// Token returns the fixed key.
func (t StaticToken) Token() string { return string(t) }

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

type outbound struct {
	owner instance
	path  string
	body  []byte
}

// ErrShuttingDown is the error an Accept method returns once Shutdown has
// started.
var ErrShuttingDown = errors.New("forward: sink is shutting down")

// ForwardingSink is an ingest.Sink that posts each decoded payload to a
// backend and waits for the answer. An Accept method makes one attempt,
// bounded by Config.RequestTimeout, and retries nothing. It returns nil only
// for a 202 answer. It returns no notices, since backend notices go to the
// log.
//
// A 4xx answer comes back as an *ingest.StatusError with the backend's
// status. A 5xx, another 2xx, a redirect, a timeout or a transport error
// comes back as a plain error.
//
// An Accept method releases its decode slot before the network call, once it
// holds a forward slot.
type ForwardingSink struct {
	cfg Config

	ctx    context.Context
	cancel context.CancelFunc

	noticeLog *noticeLog

	slots chan struct{}

	statusLog *ratelog.Limiter

	// mu makes begin's closed check and wg.Add atomic against Shutdown.
	mu           sync.RWMutex
	closed       bool
	shutdownOnce sync.Once
	wg           sync.WaitGroup
}

var _ ingest.Sink = (*ForwardingSink)(nil)

// NewForwardingSink returns a ForwardingSink. It returns an error for a
// cfg.URL that fails ValidateURL or a RedactionFingerprint that is not a
// valid header value. Call Shutdown to wait for the requests in flight.
func NewForwardingSink(cfg Config) (*ForwardingSink, error) {
	cfg = cfg.withDefaults()

	baseURL, scheme, err := ValidateURL(cfg.URL)
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

// ValidateURL checks that raw is an absolute http or https URL with a host
// and no query, fragment or user info. It returns raw with trailing slashes
// removed and the lowercase scheme. An error never quotes a raw that holds
// "@", since such a value can hold a password.
func ValidateURL(raw string) (base, scheme string, err error) {
	if raw == "" {
		return "", "", errors.New("forward URL is empty")
	}
	label := "forward URL"
	if !strings.Contains(raw, "@") {
		label = fmt.Sprintf("forward URL %q", raw)
	}
	u, err := url.Parse(raw)
	if err != nil {
		if strings.Contains(raw, "@") {
			return "", "", errors.New("forward URL: cannot parse")
		}
		var urlErr *url.Error
		if errors.As(err, &urlErr) {
			err = urlErr.Err
		}
		return "", "", fmt.Errorf("%s: %w", label, err)
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return "", "", fmt.Errorf("%s: scheme must be http or https", label)
	}
	if u.Host == "" {
		return "", "", fmt.Errorf("%s: missing host", label)
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

type instance struct {
	namespace string
	service   string
	id        string
}

func instanceOf(res *otherlodepb.ResourceAttributes) instance {
	return instance{
		namespace: strings.TrimSpace(res.GetServiceNamespace()),
		service:   strings.TrimSpace(res.GetServiceName()),
		id:        res.GetServiceInstanceId(),
	}
}

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

// AcceptStaticBaseline posts baseline to the backend and waits for its answer.
func (s *ForwardingSink) AcceptStaticBaseline(ctx context.Context, baseline *otherlodepb.StaticBaseline) ([]ingest.Notice, error) {
	return nil, s.forward(ctx, baseline, instanceOf(baseline.GetResource()), ingest.StaticBaselinePath)
}

// AcceptClassListing posts listing to the backend and waits for its answer.
func (s *ForwardingSink) AcceptClassListing(ctx context.Context, listing *otherlodepb.ClassListing) ([]ingest.Notice, error) {
	return nil, s.forward(ctx, listing, instanceOf(listing.GetResource()), ingest.ClassListingPath)
}

func (s *ForwardingSink) begin() bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.closed {
		return false
	}
	s.wg.Add(1)
	return true
}

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

var errNoForwardSlot = errors.New("max concurrent forwards reached")

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

func (s *ForwardingSink) logUnexpectedStatus(item outbound, status int) {
	unlogged, ok := s.statusLog.Allow(strconv.Itoa(status))
	if !ok {
		return
	}
	s.cfg.Logger.Error("backend answered 2xx other than 202, so the payload is not counted as stored; check OTHERLODE_COLLECTOR_FORWARD_URL",
		"status", status, "path", item.path, "owner", item.owner.String(), "unlogged_unexpected_statuses", unlogged)
}

type backendError struct {
	status string
	code   string
	owner  instance
}

func (e *backendError) Error() string {
	return "backend returned " + e.status + " for " + e.owner.String()
}

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

// Shutdown stops accepting payloads and waits for the requests in flight. A
// payload that arrives later gets ErrShuttingDown. When ctx ends, Shutdown
// cancels the requests still in flight, so each fails, and waits for them to
// return. Only the first call does anything.
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
