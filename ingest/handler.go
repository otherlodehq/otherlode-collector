// Package ingest decodes the wire payloads sent by the Otherlode agent and
// hands them to a Sink for storage or forwarding. It has no storage of its
// own.
package ingest

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"mime"
	"net/http"
	"slices"
	"strings"
	"sync"
	"time"

	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protoreflect"

	otherlodepb "buf.build/gen/go/otherlode/otherlode/protocolbuffers/go/otherlode/v1"

	"github.com/otherlodehq/otherlode-collector/internal/ratelog"
	"github.com/otherlodehq/otherlode-collector/metrics"
)

// Sink receives decoded payloads.
//
// ctx is the request's context. The handler answers 202 only after the sink
// returns a nil error, so a nil error means the sink holds the payload. The
// handler then sends each notice in NoticeHeader on the 202. A sink with no
// notices returns nil.
//
// A non-nil error means the sink did not take the payload, and the handler
// ignores the notices. A *StatusError with a status from 400 to 499 gets that
// status and its Retry-After. Any other error gets 503 with "Retry-After: 5".
type Sink interface {
	AcceptDeltaBatch(ctx context.Context, batch *otherlodepb.DeltaBatch) ([]Notice, error)
	AcceptManifest(ctx context.Context, manifest *otherlodepb.ProbeManifest) ([]Notice, error)
	AcceptStaticBaseline(ctx context.Context, baseline *otherlodepb.StaticBaseline) ([]Notice, error)
	AcceptClassListing(ctx context.Context, listing *otherlodepb.ClassListing) ([]Notice, error)
}

// The agent does not retry a 413 within one send, so a limit too low holds
// back a static baseline scan for the life of the agent process.
const maxBodyBytes = 16 << 20

const contentType = "application/x-protobuf"

// DeltaBatchPath, ManifestPath, StaticBaselinePath and ClassListingPath are
// the agent-facing ingest routes.
const (
	DeltaBatchPath     = "/v1/otherlode/deltas"
	ManifestPath       = "/v1/otherlode/manifest"
	StaticBaselinePath = "/v1/otherlode/static-baseline"
	ClassListingPath   = "/v1/otherlode/class-listing"
)

type payloadKind struct {
	label string
	name  string

	message protoreflect.MessageDescriptor
	limit   int
}

var (
	deltaBatchKind     = payloadKind{label: "deltas", name: "delta batch", message: (&otherlodepb.DeltaBatch{}).ProtoReflect().Descriptor(), limit: MaxDeltaBatchElements}
	manifestKind       = payloadKind{label: "manifest", name: "manifest", message: (&otherlodepb.ProbeManifest{}).ProtoReflect().Descriptor(), limit: MaxManifestElements}
	staticBaselineKind = payloadKind{label: "static_baseline", name: "static baseline", message: (&otherlodepb.StaticBaseline{}).ProtoReflect().Descriptor(), limit: MaxStaticBaselineElements}
	classListingKind   = payloadKind{label: "class_listing", name: "class listing", message: (&otherlodepb.ClassListing{}).ProtoReflect().Descriptor(), limit: MaxClassListingElements}
)

// PayloadLabel returns the metrics label for the payload type served at
// path: "deltas", "manifest", "static_baseline" or "class_listing". Any
// other path gives "deltas".
func PayloadLabel(path string) string {
	switch path {
	case ManifestPath:
		return manifestKind.label
	case StaticBaselinePath:
		return staticBaselineKind.label
	case ClassListingPath:
		return classListingKind.label
	default:
		return deltaBatchKind.label
	}
}

// Handler serves the agent-facing ingest routes. Each route takes one POST of
// a serialized protobuf message.
type Handler struct {
	sink   Sink
	logger *slog.Logger

	slots chan struct{}

	slotWait time.Duration

	budget time.Duration

	sinkLog *ratelog.Limiter

	refuseLog *ratelog.Limiter
}

// maxSlotWait must stay below the server's WriteTimeout in
// cmd/otherlode-collector, or the 503 does not reach the client.
const maxSlotWait = 5 * time.Second

const busyLogInterval = time.Minute

// HandlerOption configures a Handler. See NewHandler.
type HandlerOption func(*Handler)

// WithMaxConcurrentDecodes caps how many requests the Handler decodes and
// passes to the sink at one time. A request holds its slot until the sink
// returns, unless the sink calls ReleaseDecodeSlot first. A value of zero or
// less means no cap, which is the default.
//
// A request that finds no free slot waits for one, for the slot wait (see
// WithSlotWait) or the time left in its budget (see WithRequestBudget),
// whichever is shorter. If it gets no slot, the Handler answers 503 with
// "Retry-After: 1" and counts the request as rejected with reason "canceled"
// (its context ended), "timeout" (its budget ended before the wait began) or
// "busy" (the wait ended). It logs "timeout" and "busy" at most once a minute
// each.
func WithMaxConcurrentDecodes(n int) HandlerOption {
	return func(h *Handler) {
		if n > 0 {
			h.slots = make(chan struct{}, n)
		}
	}
}

// WithSlotWait sets the longest a request waits for a decode slot. A value
// of zero or less keeps the default of five seconds.
func WithSlotWait(d time.Duration) HandlerOption {
	return func(h *Handler) {
		if d > 0 {
			h.slotWait = d
		}
	}
}

// WithRequestBudget sets the time one request may take, measured from the
// moment the handler starts on it. The slot wait and the sink share it. The
// context passed to the sink ends when the budget ends. A value of zero or
// less means no budget, which is the default.
func WithRequestBudget(d time.Duration) HandlerOption {
	return func(h *Handler) {
		if d > 0 {
			h.budget = d
		}
	}
}

type slotKey struct{}

type slotRelease struct {
	once sync.Once
	free func()
}

func (s *slotRelease) release() { s.once.Do(s.free) }

// ReleaseDecodeSlot frees the decode slot of the request that ctx belongs
// to. A sink calls it when it no longer needs the memory of the decoded
// payload. It is safe to call more than once, and does nothing for a context
// the Handler did not make.
func ReleaseDecodeSlot(ctx context.Context) {
	if s, ok := ctx.Value(slotKey{}).(*slotRelease); ok {
		s.release()
	}
}

// NewHandler returns a Handler that passes decoded payloads to sink and
// logs to logger, or slog.Default() when logger is nil.
func NewHandler(sink Sink, logger *slog.Logger, opts ...HandlerOption) *Handler {
	if logger == nil {
		logger = slog.Default()
	}
	h := &Handler{sink: sink, logger: logger, slotWait: maxSlotWait, sinkLog: ratelog.New(busyLogInterval, time.Now), refuseLog: ratelog.New(busyLogInterval, time.Now)}
	for _, opt := range opts {
		opt(h)
	}
	return h
}

// Register adds POST routes for DeltaBatchPath, ManifestPath,
// StaticBaselinePath and ClassListingPath to mux.
func (h *Handler) Register(mux *http.ServeMux) {
	mux.HandleFunc("POST "+DeltaBatchPath, h.handleDeltaBatch)
	mux.HandleFunc("POST "+ManifestPath, h.handleManifest)
	mux.HandleFunc("POST "+StaticBaselinePath, h.handleStaticBaseline)
	mux.HandleFunc("POST "+ClassListingPath, h.handleClassListing)
}

func (h *Handler) handleDeltaBatch(w http.ResponseWriter, r *http.Request) {
	handle(h, w, r, deltaBatchKind, validateDeltaBatch, h.sink.AcceptDeltaBatch)
}

func (h *Handler) handleManifest(w http.ResponseWriter, r *http.Request) {
	handle(h, w, r, manifestKind, validateManifest, h.sink.AcceptManifest)
}

func (h *Handler) handleStaticBaseline(w http.ResponseWriter, r *http.Request) {
	handle(h, w, r, staticBaselineKind, validateStaticBaseline, h.sink.AcceptStaticBaseline)
}

func (h *Handler) handleClassListing(w http.ResponseWriter, r *http.Request) {
	handle(h, w, r, classListingKind, validateClassListing, h.sink.AcceptClassListing)
}

func handle[T proto.Message](h *Handler, w http.ResponseWriter, r *http.Request, kind payloadKind, validate func(T) error, accept func(context.Context, T) ([]Notice, error)) {
	start := time.Now()
	ctx := r.Context()
	if h.budget > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithDeadline(ctx, start.Add(h.budget))
		defer cancel()
	}
	body, ok := h.readRequest(w, r, kind)
	if !ok {
		return
	}
	if !h.withinLimit(w, body, kind) {
		return
	}
	if !h.acquire(ctx, r.Context(), w, kind) {
		return
	}
	slot := &slotRelease{free: h.free}
	defer slot.release()
	ctx = context.WithValue(ctx, slotKey{}, slot)
	msg := newMessage[T]()
	if !h.unmarshal(w, body, msg, kind) {
		return
	}
	if err := validate(msg); err != nil {
		h.reject(w, kind, err)
		return
	}
	notices, err := accept(ctx, msg)
	if err != nil {
		h.sinkFailed(w, kind, err)
		return
	}
	metrics.IngestAccepted.Inc(kind.label)
	writeNotices(w.Header(), notices)
	w.WriteHeader(http.StatusAccepted)
}

func writeNotices(header http.Header, notices []Notice) {
	for i, n := range notices {
		if slices.Contains(notices[:i], n) {
			continue
		}
		header.Add(NoticeHeader, string(n))
	}
}

func newMessage[T proto.Message]() T {
	var zero T
	return zero.ProtoReflect().New().Interface().(T)
}

// MaxResourceValueBytes is the longest value, in bytes, that a payload's
// resource may carry in service_name, service_version, service_instance_id,
// environment, run_id, service_namespace or agent_version. The handler
// rejects a longer value.
const MaxResourceValueBytes = 1024

func validateResource(res *otherlodepb.ResourceAttributes) error {
	if res == nil {
		return errors.New("missing resource")
	}
	for _, f := range []struct{ name, value string }{
		{"service_name", res.GetServiceName()},
		{"service_version", res.GetServiceVersion()},
		{"service_instance_id", res.GetServiceInstanceId()},
		{"environment", res.GetEnvironment()},
		{"run_id", res.GetRunId()},
		{"service_namespace", res.GetServiceNamespace()},
		{"agent_version", res.GetAgentVersion()},
	} {
		if len(f.value) > MaxResourceValueBytes {
			return fmt.Errorf("resource.%s is %d bytes, over the %d-byte limit", f.name, len(f.value), MaxResourceValueBytes)
		}
		if strings.IndexByte(f.value, 0) >= 0 {
			return fmt.Errorf("resource.%s holds a NUL byte", f.name)
		}
	}
	name := strings.TrimSpace(res.GetServiceName())
	if name == "" {
		return errors.New("resource.service_name is empty")
	}
	if isDotSegment(name) {
		return fmt.Errorf("resource.service_name is %q, which no URL path can name", name)
	}
	if namespace := strings.TrimSpace(res.GetServiceNamespace()); isDotSegment(namespace) {
		return fmt.Errorf("resource.service_namespace is %q, which no URL path can name", namespace)
	}
	if res.GetServiceInstanceId() == "" {
		return errors.New("resource.service_instance_id is empty")
	}
	if res.GetRunId() == "" {
		return errors.New("resource.run_id is empty")
	}
	return nil
}

func isDotSegment(v string) bool {
	return v == "." || v == ".."
}

func validateDeltaBatch(batch *otherlodepb.DeltaBatch) error {
	if err := validateResource(batch.GetResource()); err != nil {
		return err
	}
	for i, d := range batch.GetDeltas() {
		if !timestampInRange(d.GetFirstSeenAt()) {
			return timestampError("deltas", i, "first_seen_at")
		}
	}
	for i, d := range batch.GetEndpointDeltas() {
		if !timestampInRange(d.GetFirstSeenAt()) {
			return timestampError("endpoint_deltas", i, "first_seen_at")
		}
	}
	for i, d := range batch.GetDependencyDeltas() {
		if !timestampInRange(d.GetFirstLoadedAt()) {
			return timestampError("dependency_deltas", i, "first_loaded_at")
		}
	}
	return nil
}

// MaxTimestampMillis is the latest time, in milliseconds since the epoch,
// that a payload's timestamp may hold, which is the end of the year 9999. The
// earliest is 0.
const MaxTimestampMillis = 253402300799999

func timestampInRange(ms int64) bool {
	return ms >= 0 && ms <= MaxTimestampMillis
}

func timestampError(list string, index int, field string) error {
	return fmt.Errorf("%s[%d].%s is out of range", list, index, field)
}

func validateManifest(manifest *otherlodepb.ProbeManifest) error {
	if err := validateResource(manifest.GetResource()); err != nil {
		return err
	}
	for i, c := range manifest.GetUnreportedClasses() {
		if !timestampInRange(c.GetFirstSeenUnreportedAt()) {
			return timestampError("unreported_classes", i, "first_seen_unreported_at")
		}
	}
	for i, c := range manifest.GetFailedClasses() {
		if !timestampInRange(c.GetWithheldAt()) {
			return timestampError("failed_classes", i, "withheld_at")
		}
	}
	for i, c := range manifest.GetClassesFailedToInitialize() {
		if !timestampInRange(c.GetObservedAt()) {
			return timestampError("classes_failed_to_initialize", i, "observed_at")
		}
	}
	for i, c := range manifest.GetSkippedClasses() {
		if !timestampInRange(c.GetSkippedAt()) {
			return timestampError("skipped_classes", i, "skipped_at")
		}
	}
	for i, m := range manifest.GetDisabledEndpointModules() {
		if !timestampInRange(m.GetDisabledAt()) {
			return timestampError("disabled_endpoint_modules", i, "disabled_at")
		}
	}
	for i, m := range manifest.GetFrameworkMarkers() {
		if !timestampInRange(m.GetSeenAt()) {
			return timestampError("framework_markers", i, "seen_at")
		}
	}
	for i, c := range manifest.GetClassesFailedToInitialize() {
		if c.GetReason() == otherlodepb.InitializationFailure_INITIALIZATION_FAILURE_UNSPECIFIED {
			return fmt.Errorf("classes_failed_to_initialize[%d].reason is not set", i)
		}
	}
	for i, d := range manifest.GetDependencies() {
		if len(d.GetIdentities()) == 0 {
			return fmt.Errorf("dependencies[%d].identities is empty", i)
		}
	}
	return nil
}

func validateStaticBaseline(baseline *otherlodepb.StaticBaseline) error {
	if err := validateResource(baseline.GetResource()); err != nil {
		return err
	}
	if baseline.GetScannedAt() <= 0 {
		return errors.New("scanned_at is not set")
	}
	if baseline.GetChunkCount() < 1 {
		return errors.New("chunk_count must be at least 1")
	}
	if baseline.GetChunkIndex() < 0 {
		return errors.New("chunk_index is negative")
	}
	if baseline.GetChunkIndex() >= baseline.GetChunkCount() {
		return errors.New("chunk_index is out of range for chunk_count")
	}
	return nil
}

func validateClassListing(listing *otherlodepb.ClassListing) error {
	if err := validateResource(listing.GetResource()); err != nil {
		return err
	}
	if listing.GetListedAt() <= 0 {
		return errors.New("listed_at is not set")
	}
	if listing.GetChunkCount() < 1 {
		return errors.New("chunk_count must be at least 1")
	}
	if listing.GetChunkIndex() < 0 {
		return errors.New("chunk_index is negative")
	}
	if listing.GetChunkIndex() >= listing.GetChunkCount() {
		return errors.New("chunk_index is out of range for chunk_count")
	}
	return nil
}

func (h *Handler) reject(w http.ResponseWriter, kind payloadKind, err error) {
	h.logger.Warn("rejecting invalid "+kind.name, "error", err)
	metrics.IngestRejected.Inc(kind.label, "invalid")
	http.Error(w, "invalid "+kind.name+": "+err.Error(), http.StatusBadRequest)
}

const sinkRetryAfter = "5"

func (h *Handler) sinkFailed(w http.ResponseWriter, kind payloadKind, err error) {
	metrics.IngestRejected.Inc(kind.label, "sink")
	reason := reasonOf(err)
	var statusErr *StatusError
	if errors.As(err, &statusErr) && statusErr.relayed() {
		if unlogged, ok := h.sinkLog.Allow(fmt.Sprintf("%s/%d/%s", kind.label, statusErr.Status, reason)); ok {
			attrs := []any{"error", err, "status", statusErr.Status, "unlogged_sink_refusals", unlogged}
			if statusErr.Code != "" {
				attrs = append(attrs, "backend_error", statusErr.Code)
			}
			h.logger.Warn("sink refused "+kind.name, attrs...)
		}
		if statusErr.RetryAfter != "" {
			w.Header().Set("Retry-After", statusErr.RetryAfter)
		}
		w.WriteHeader(statusErr.Status)
		return
	}
	if unlogged, ok := h.sinkLog.Allow(kind.label + "/" + reason); ok {
		h.logger.Error("sink rejected "+kind.name, "error", err, "reason", reason, "unlogged_sink_failures", unlogged)
	}
	w.Header().Set("Retry-After", sinkRetryAfter)
	w.WriteHeader(http.StatusServiceUnavailable)
}

func (h *Handler) readRequest(w http.ResponseWriter, r *http.Request, kind payloadKind) ([]byte, bool) {
	if !checkContentType(w, r) {
		metrics.IngestRejected.Inc(kind.label, "content_type")
		return nil, false
	}
	body, reason, err := readBody(w, r)
	if err != nil {
		metrics.IngestRejected.Inc(kind.label, reason)
		return nil, false
	}
	return body, true
}

func (h *Handler) withinLimit(w http.ResponseWriter, body []byte, kind payloadKind) bool {
	if _, err := countElements(body, kind.message, kind.limit); !errors.Is(err, errTooManyElements) {
		return true
	}
	h.logger.Warn("refusing "+kind.name+" with too many elements", "limit", kind.limit, "bytes", len(body))
	metrics.IngestRejected.Inc(kind.label, tooManyElementsReason)
	http.Error(w, errTooManyElements.Error(), http.StatusRequestEntityTooLarge)
	return false
}

type admission int

const (
	acquired admission = iota
	busy
	canceled
	timeout
)

func (h *Handler) acquire(ctx, requestCtx context.Context, w http.ResponseWriter, kind payloadKind) bool {
	if h.slots == nil {
		return true
	}
	wait := h.slotWait
	if deadline, ok := ctx.Deadline(); ok {
		wait = min(wait, time.Until(deadline))
	}
	outcome := h.admit(ctx, requestCtx, wait)
	if outcome == acquired {
		return true
	}
	h.refuse(w, kind, outcome, max(wait, 0))
	return false
}

func (h *Handler) admit(ctx, requestCtx context.Context, wait time.Duration) admission {
	if requestCtx.Err() != nil {
		return canceled
	}
	if ctx.Err() != nil || wait <= 0 {
		return timeout
	}
	timer := time.NewTimer(wait)
	defer timer.Stop()
	select {
	case h.slots <- struct{}{}:
		return acquired
	case <-ctx.Done():
	case <-timer.C:
	}
	if requestCtx.Err() != nil {
		return canceled
	}
	return busy
}

func (h *Handler) refuse(w http.ResponseWriter, kind payloadKind, outcome admission, wait time.Duration) {
	w.Header().Set("Retry-After", "1")
	switch outcome {
	case canceled:
		metrics.IngestRejected.Inc(kind.label, "canceled")
	case timeout:
		metrics.IngestRejected.Inc(kind.label, "timeout")
		if unlogged, ok := h.refuseLog.Allow("timeout"); ok {
			h.logger.Warn("refusing "+kind.name+": the request used its time budget before decoding started",
				"budget", h.budget.String(),
				"unlogged_timeout_refusals", unlogged,
			)
		}
	default:
		metrics.IngestRejected.Inc(kind.label, "busy")
		if unlogged, ok := h.refuseLog.Allow("busy"); ok {
			h.logger.Warn("refusing "+kind.name+": no decode slot came free",
				"wait", wait.String(),
				"max_concurrent_decodes", cap(h.slots),
				"unlogged_busy_refusals", unlogged,
			)
		}
	}
	w.WriteHeader(http.StatusServiceUnavailable)
}

func (h *Handler) free() {
	if h.slots != nil {
		<-h.slots
	}
}

func (h *Handler) unmarshal(w http.ResponseWriter, body []byte, msg proto.Message, kind payloadKind) bool {
	if err := proto.Unmarshal(body, msg); err != nil {
		h.logger.Warn("rejecting malformed "+kind.name, "error", err)
		metrics.IngestRejected.Inc(kind.label, "malformed")
		http.Error(w, "malformed "+kind.name, http.StatusBadRequest)
		return false
	}
	return true
}

func checkContentType(w http.ResponseWriter, r *http.Request) bool {
	for _, enc := range strings.Split(strings.Join(r.Header.Values("Content-Encoding"), ","), ",") {
		if enc = strings.TrimSpace(enc); enc != "" && !strings.EqualFold(enc, "identity") {
			http.Error(w, "unsupported content encoding", http.StatusUnsupportedMediaType)
			return false
		}
	}
	mediaType, _, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if err != nil || mediaType != contentType {
		http.Error(w, "unsupported content type", http.StatusUnsupportedMediaType)
		return false
	}
	return true
}

func readBody(w http.ResponseWriter, r *http.Request) (body []byte, reason string, err error) {
	r.Body = http.MaxBytesReader(w, r.Body, maxBodyBytes)
	body, err = io.ReadAll(r.Body)
	if err != nil {
		var tooLarge *http.MaxBytesError
		if errors.As(err, &tooLarge) {
			http.Error(w, "request body too large", http.StatusRequestEntityTooLarge)
			return nil, "too_large", err
		}
		http.Error(w, "failed to read body", http.StatusBadRequest)
		return nil, "read", err
	}
	return body, "", nil
}
