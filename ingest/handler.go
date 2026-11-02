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

// Sink receives decoded payloads. A real backend implements this to persist
// or forward them; the collector itself stays stateless.
//
// ctx is the request's context, so middleware such as an auth or tenant
// resolver can attach request scope for the sink to read back. A non-nil
// error means the payload was not taken: the handler answers 503 with
// "Retry-After: 5" and the sender is expected to retry. The handler then
// ignores the notices. When the payload was taken, the handler sends each
// notice in NoticeHeader on the 202. A sink with nothing to say returns nil.
//
// A sink that wants the sender to see a client-error status of its own, for
// example the status a backend gave, returns a *StatusError. The handler
// answers with that status and sends the sender's Retry-After if the error
// holds one. Every other error is a 503, so a sink that returns plain
// errors works as before.
//
// The handler answers 202 only after the sink returns without an error, so
// a sink that returns nil is saying it holds the payload.
type Sink interface {
	AcceptDeltaBatch(ctx context.Context, batch *otherlodepb.DeltaBatch) ([]Notice, error)
	AcceptManifest(ctx context.Context, manifest *otherlodepb.ProbeManifest) ([]Notice, error)
	AcceptStaticBaseline(ctx context.Context, baseline *otherlodepb.StaticBaseline) ([]Notice, error)
	AcceptClassListing(ctx context.Context, listing *otherlodepb.ClassListing) ([]Notice, error)
}

// maxBodyBytes caps a single request body. A static baseline chunk can
// hold up to 20000 method entries and reach several MiB. The agent does
// not retry a 413 within one send. It keeps the chunk and the chunks after
// it, and resends one after each flush the collector confirms. The limit
// is fixed, so a false 413 holds back that scan for the life of the
// process. This limit exists to bound memory use from a bad or hostile
// sender, not to fit any expected payload size.
const maxBodyBytes = 16 << 20 // 16 MiB

// contentType is the only media type the ingest routes accept.
const contentType = "application/x-protobuf"

// DeltaBatchPath, ManifestPath, StaticBaselinePath and ClassListingPath
// are the agent-facing ingest routes. A Sink that relays payloads onward (see
// forward.ForwardingSink) posts to the same paths on the backend, so
// both sides share these constants instead of each holding its own copy
// of the literal.
const (
	DeltaBatchPath     = "/v1/otherlode/deltas"
	ManifestPath       = "/v1/otherlode/manifest"
	StaticBaselinePath = "/v1/otherlode/static-baseline"
	ClassListingPath   = "/v1/otherlode/class-listing"
)

// payloadKind names one payload type for logging and metrics: label is
// the metrics label value, name is the text used in log lines and error
// bodies.
type payloadKind struct {
	label string
	name  string

	// message describes the payload's message, and limit is the most
	// elements it may hold.
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

// Handler implements the agent-facing HTTP surface described in the
// Otherlode agent's "Transport" design: one POST per flush interval, body
// is a serialized protobuf message, no gRPC.
type Handler struct {
	sink   Sink
	logger *slog.Logger

	// slots is a counting semaphore for concurrent decodes. It is nil
	// when decodes are not capped.
	slots chan struct{}

	// slotWait is the longest a request waits for a slot.
	slotWait time.Duration

	// budget is the time one request may take from handler entry. Zero
	// means no budget.
	budget time.Duration

	// sinkLog limits the log lines for sink failures. See sinkFailed.
	sinkLog *ratelog.Limiter

	// busyMu guards busyLogged and busyUnlogged. See logBusy.
	busyMu       sync.Mutex
	busyLogEvery time.Duration
	busyLogged   time.Time
	busyUnlogged int
}

// maxSlotWait is how long a request waits for a decode slot. It is shorter
// than the collector's 10 second write timeout, so in most cases the 503
// reaches the client. The write deadline starts when the headers are read,
// so a slow body upload can still use up the rest.
const maxSlotWait = 5 * time.Second

// busyLogInterval is the shortest gap between two log lines for requests
// refused because no decode slot came free.
const busyLogInterval = time.Minute

// HandlerOption configures a Handler. See NewHandler.
type HandlerOption func(*Handler)

// WithMaxConcurrentDecodes caps how many requests the Handler decodes
// and passes to the sink at one time. A request holds its slot until the
// sink returns, unless the sink calls ReleaseDecodeSlot first. A value of
// zero or less means no cap, which is the default.
//
// Decoding a protobuf can take far more memory than the body. The handler
// refuses a body over its element limit before it decodes it. The decoded
// message stays alive until the sink returns, so the cap bounds decode
// memory at about n times the largest decode. A request reads its body
// before it takes a slot, so bodies waiting for a slot are held as read,
// up to 16 MiB each.
//
// A slow client holds no slot while it sends, and the cap counts only the
// work that needs the memory. A request that finds no free slot waits for
// one, for the slot wait (see WithSlotWait) or the time left in its
// budget (see WithRequestBudget), whichever is shorter. If its context
// ends first, the Handler counts the request as rejected with reason
// "canceled". If the wait ends first, it counts reason "busy" and logs a
// warning at most once a minute. Either way the Handler answers 503 with
// "Retry-After: 1", since a handler that writes nothing makes net/http
// send 200.
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
// moment the handler starts on it. The slot wait and the sink's work share
// it: the context passed to the sink ends when the budget ends, and a wait
// for a decode slot ends then too. When the budget ends before the sink
// takes the payload, the Handler answers 503 with "Retry-After". A value
// of zero or less means no budget, which is the default.
func WithRequestBudget(d time.Duration) HandlerOption {
	return func(h *Handler) {
		if d > 0 {
			h.budget = d
		}
	}
}

type slotKey struct{}

// slotRelease frees a decode slot once, however often it is called.
type slotRelease struct {
	once sync.Once
	free func()
}

func (s *slotRelease) release() { s.once.Do(s.free) }

// ReleaseDecodeSlot frees the decode slot of the request that ctx belongs
// to. A sink calls it when it no longer needs the memory of the decoded
// payload, for example before a network call. It is safe to call more than
// once, and does nothing for a context the Handler did not make. A sink
// that never calls it keeps the slot until it returns.
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
	h := &Handler{sink: sink, logger: logger, slotWait: maxSlotWait, busyLogEvery: busyLogInterval, sinkLog: ratelog.New(busyLogInterval, time.Now)}
	for _, opt := range opts {
		opt(h)
	}
	return h
}

// Register adds POST routes for DeltaBatchPath, ManifestPath,
// StaticBaselinePath and ClassListingPath to mux. They match the paths
// HttpExporter posts to.
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

// handle serves one ingest request. It reads the body, counts its
// elements, takes a decode slot, decodes the body into a new T, validates
// it, passes it to accept, and answers 202 with one NoticeHeader value for
// each notice accept returned. It holds the slot until accept returns or
// calls ReleaseDecodeSlot. With a request budget, the context passed to
// accept ends when the budget does. T is the pointer type of the payload
// message.
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

// writeNotices adds one NoticeHeader value to header for each distinct
// notice, in the order given.
func writeNotices(header http.Header, notices []Notice) {
	for i, n := range notices {
		if slices.Contains(notices[:i], n) {
			continue
		}
		header.Add(NoticeHeader, string(n))
	}
}

// newMessage returns a new, empty message of the type T points to.
func newMessage[T proto.Message]() T {
	var zero T
	return zero.ProtoReflect().New().Interface().(T)
}

// MaxResourceValueBytes is the longest value, in bytes, that a payload's
// resource may carry in service_name, service_version,
// service_instance_id, environment, run_id, service_namespace or
// agent_version. The handler rejects a longer value.
const MaxResourceValueBytes = 1024

// validateResource checks the identity a Sink needs: a service name that
// is not blank, an instance ID and a run ID. It rejects a value longer
// than MaxResourceValueBytes, or one that holds a NUL byte, in any of the
// seven identity strings. It measures the value as sent, before any trimming, and never quotes it.
// It rejects a service name or namespace of "." or ".." (see
// isDotSegment). class_id and
// cumulative totals mean something only within one run of one instance,
// so the run ID is required. The agent makes a fresh run ID per process,
// so a restarted instance with a pinned instance ID still names a
// different run.
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

// isDotSegment reports whether v is "." or "..". A service is read at a
// URL path that holds its namespace and name as segments, and browsers
// remove a dot segment even when it is percent-escaped. So a service
// named that way could never be opened, and a namespace ".." would lead
// to another service. Backends key a service by the trimmed values, so
// callers pass trimmed values.
func isDotSegment(v string) bool {
	return v == "." || v == ".."
}

// validateDeltaBatch checks the resource a Sink needs to attribute the
// batch. The agent always sends it, even on an empty heartbeat batch,
// so a batch without it is a broken or foreign sender, not a quiet
// instance. It also rejects a first-seen or first-loaded time outside
// the range of MaxTimestampMillis.
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
// that a payload's timestamp may hold: the end of the year 9999. The
// earliest is 0, the start of 1970.
const MaxTimestampMillis = 253402300799999

// timestampInRange reports whether ms is from 0 to MaxTimestampMillis.
func timestampInRange(ms int64) bool {
	return ms >= 0 && ms <= MaxTimestampMillis
}

// timestampError names the field at list[index].field, never its value.
func timestampError(list string, index int, field string) error {
	return fmt.Errorf("%s[%d].%s is out of range", list, index, field)
}

// validateManifest checks that the manifest's resource names the
// service, instance and run it describes. class_id is assigned per
// run in load order, so the same class_id can mean a different class in
// two instances of the same service, or in two runs of one instance. A
// backend that keys on less than all three would misattribute probes.
// It also rejects a timestamp outside the range of MaxTimestampMillis, a
// class that failed to initialize with no reason, and a dependency with no
// identity.
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

// validateStaticBaseline checks the fields a backend cannot do without:
// the resource and scanned_at name the scan, and the chunk fields say
// whether the scan is complete. A chunk missing any of them can never be
// attributed or diffed.
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

// validateClassListing checks the fields a backend cannot do without: the
// resource and listed_at name the listing, and the chunk fields say
// whether the listing is complete. A chunk missing any of them can never
// be attributed or read as a whole.
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

// reject answers a decoded but unusable payload with 400.
func (h *Handler) reject(w http.ResponseWriter, kind payloadKind, err error) {
	h.logger.Warn("rejecting invalid "+kind.name, "error", err)
	metrics.IngestRejected.Inc(kind.label, "invalid")
	http.Error(w, "invalid "+kind.name+": "+err.Error(), http.StatusBadRequest)
}

// sinkRetryAfter is the Retry-After value, in seconds, on a 503 for a
// payload the sink did not take. A sender that honours the header waits
// longer than after a busy refusal.
const sinkRetryAfter = "5"

// sinkFailed answers a valid payload the sink did not take. A
// *StatusError with a client-error status gets that status, with its
// Retry-After when it has one. Any other error gets 503, "Retry-After: 5"
// and no body. Either way the sender keeps the payload and retries instead
// of the data being lost.
//
// It writes the first log line of each kind in an interval: one per
// payload type, status and cause class. A sink marks the cause class with
// WithReason, using the reasons of the forward failure metric; an error
// without one is class "error". The metric counts every failure.
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

// readRequest reads r's body. It reports false after writing the error
// response itself: 415 for the wrong media type or a content encoding
// other than identity, 413 for a body over maxBodyBytes, 400 for a body
// that cannot be read. kind names the payload in metrics labels.
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

// withinLimit counts the elements of body from the wire format, before
// anything decodes it. It reports false after answering 413 "payload has
// too many elements" and counting reason "too_many_elements", for a body
// over the limit of its kind. A body that is not valid protobuf passes
// unless the count passed the limit first, and unmarshal refuses it. The
// log line never quotes the body.
func (h *Handler) withinLimit(w http.ResponseWriter, body []byte, kind payloadKind) bool {
	if _, err := countElements(body, kind.message, kind.limit); !errors.Is(err, errTooManyElements) {
		return true
	}
	h.logger.Warn("refusing "+kind.name+" with too many elements", "limit", kind.limit, "bytes", len(body))
	metrics.IngestRejected.Inc(kind.label, tooManyElementsReason)
	http.Error(w, errTooManyElements.Error(), http.StatusRequestEntityTooLarge)
	return false
}

// acquire waits for a free decode slot. Without a cap it returns true at
// once. It reports false when it took no slot, after answering 503 with
// "Retry-After: 1". ctx carries the request budget, and requestCtx is the
// client's own context. The wait is the slot wait or the time left in ctx,
// whichever is shorter. If requestCtx ends first, it counts reason
// "canceled". If the wait ends first, it counts reason "busy" and logs it
// through logBusy.
func (h *Handler) acquire(ctx, requestCtx context.Context, w http.ResponseWriter, kind payloadKind) bool {
	if h.slots == nil {
		return true
	}
	wait := h.slotWait
	if deadline, ok := ctx.Deadline(); ok {
		wait = min(wait, time.Until(deadline))
	}
	if ctx.Err() == nil {
		timer := time.NewTimer(max(wait, 0))
		defer timer.Stop()
		select {
		case h.slots <- struct{}{}:
			return true
		case <-ctx.Done():
		case <-timer.C:
		}
	}
	w.Header().Set("Retry-After", "1")
	if requestCtx.Err() != nil {
		metrics.IngestRejected.Inc(kind.label, "canceled")
	} else {
		metrics.IngestRejected.Inc(kind.label, "busy")
		h.logBusy(kind, max(wait, 0))
	}
	w.WriteHeader(http.StatusServiceUnavailable)
	return false
}

// logBusy logs a request refused because no decode slot came free. Such
// refusals come in bursts while the collector is overloaded, so it writes
// at most one line per busyLogEvery. The line counts the refusals since
// the previous line that wrote none of their own. wait is the time the
// request waited.
func (h *Handler) logBusy(kind payloadKind, wait time.Duration) {
	h.busyMu.Lock()
	now := time.Now()
	if !h.busyLogged.IsZero() && now.Sub(h.busyLogged) < h.busyLogEvery {
		h.busyUnlogged++
		h.busyMu.Unlock()
		return
	}
	unlogged := h.busyUnlogged
	h.busyLogged = now
	h.busyUnlogged = 0
	h.busyMu.Unlock()
	h.logger.Warn("refusing "+kind.name+": no decode slot came free",
		"wait", wait.String(),
		"max_concurrent_decodes", cap(h.slots),
		"unlogged_busy_refusals", unlogged,
	)
}

// free frees the slot that a successful acquire took.
func (h *Handler) free() {
	if h.slots != nil {
		<-h.slots
	}
}

// unmarshal decodes body into msg. It reports false after writing a 400
// for anything that is not valid protobuf. kind names the payload in log
// lines, error bodies, and metrics labels.
func (h *Handler) unmarshal(w http.ResponseWriter, body []byte, msg proto.Message, kind payloadKind) bool {
	if err := proto.Unmarshal(body, msg); err != nil {
		h.logger.Warn("rejecting malformed "+kind.name, "error", err)
		metrics.IngestRejected.Inc(kind.label, "malformed")
		http.Error(w, "malformed "+kind.name, http.StatusBadRequest)
		return false
	}
	return true
}

// checkContentType accepts application/x-protobuf with any parameters,
// so a client that appends a charset is not turned away. It rejects a
// Content-Encoding other than identity, since the handler does not
// decompress and would misread the body as malformed protobuf. All
// Content-Encoding lines are joined, and every entry must be empty or
// identity.
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

// readBody reads the request body up to maxBodyBytes. On failure it
// writes the error response and returns the metrics reason label.
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
