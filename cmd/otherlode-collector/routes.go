package main

import (
	"fmt"
	"log/slog"
	"net/http"

	"github.com/otherlodehq/otherlode-collector/ingest"
	"github.com/otherlodehq/otherlode-collector/internal/auth"
	"github.com/otherlodehq/otherlode-collector/internal/forward"
	"github.com/otherlodehq/otherlode-collector/internal/processor"
	"github.com/otherlodehq/otherlode-collector/internal/ratelimit"
	"github.com/otherlodehq/otherlode-collector/metrics"
)

// routeConfig holds what registerRoutes wires together. Every field may be
// left as its zero value.
type routeConfig struct {
	// Logger receives the route and processor logs. Nil means slog.Default.
	Logger *slog.Logger
	// AuthTokens, when non-nil, makes the ingest routes require an
	// "Authorization: Bearer <token>" header whose token is in the set.
	// Each request reads the current set, so a token file re-read applies
	// without a restart.
	AuthTokens *auth.TokenSet
	// Limiter, when non-nil, throttles the ingest routes per client IP. It
	// runs before auth, so a flood is capped whether or not it carries a
	// valid token.
	Limiter *ratelimit.Limiter
	// MaxDecodes caps concurrent request decodes. Zero or less means no
	// cap.
	MaxDecodes int
	// Forward configures the backend relay. An empty Forward.URL logs
	// payloads instead of forwarding them.
	Forward forward.Config
	// Environment, when its Value is non-empty, adds a processor.Environment.
	Environment processor.EnvironmentConfig
	// Namespace, when its Value is non-empty, adds a processor.Namespace.
	Namespace processor.NamespaceConfig
	// Redaction, when enabled, adds a processor.Redaction. It must pass
	// Validate.
	Redaction processor.RedactionConfig
}

// registerRoutes wires the ingest handler and health check onto mux as cfg
// describes. /healthz and /metrics stay open, unauthenticated and
// unthrottled: the first for liveness and readiness probes, the second for
// a Prometheus scraper. /metrics exposes only counts.
//
// When cfg.Forward.URL is non-empty, ingested payloads are relayed to that
// backend by a forward.ForwardingSink. registerRoutes returns it so the
// caller can shut it down. An empty URL logs payloads instead of
// forwarding them, so local, development and CI runs work with no backend.
// registerRoutes then returns a nil *forward.ForwardingSink. A URL that
// cannot be used is returned as an error.
//
// A processor.Environment wraps the chosen sink. It writes the environment
// onto delta batches, manifests and static baselines before the sink sees
// them. A processor.Namespace wraps the sink outside it and writes the
// service namespace onto the same three payloads. A processor.Redaction
// wraps the sink outside both. It hides string literals, re-keys branch
// and site keys and drops unknown fields before any payload is forwarded.
// It logs a fingerprint of its secret, never the secret, and the
// forwarder sends the same fingerprint in the forward.RedactionHeader
// header of every request. A redaction
// config that fails Validate is returned as an error. A zero config
// leaves the matching processor out of the chain.
func registerRoutes(mux *http.ServeMux, cfg routeConfig) (*forward.ForwardingSink, error) {
	logger := cfg.Logger
	if logger == nil {
		logger = slog.Default()
	}

	if err := cfg.Redaction.Validate(); err != nil {
		return nil, fmt.Errorf("redaction: %w", err)
	}
	var fingerprint string
	if cfg.Redaction.Enabled() {
		fingerprint = processor.SecretFingerprint(cfg.Redaction.Secret)
		cfg.Forward.RedactionFingerprint = fingerprint
	}

	var sink ingest.Sink
	var fwd *forward.ForwardingSink
	if cfg.Forward.URL != "" {
		cfg.Forward.Logger = logger
		var err error
		fwd, err = forward.NewForwardingSink(cfg.Forward)
		if err != nil {
			return nil, err
		}
		sink = fwd
	} else {
		logger.Warn("OTHERLODE_COLLECTOR_FORWARD_URL not set; ingest payloads are only logged, not forwarded")
		sink = ingest.NewLogSink(logger)
	}
	if cfg.Environment.Value != "" {
		logger.Info("stamping environment on ingested payloads", "environment", cfg.Environment.Value, "action", cfg.Environment.Action.String())
		sink = processor.NewEnvironment(sink, cfg.Environment, logger)
	}
	if cfg.Namespace.Value != "" {
		logger.Info("stamping service namespace on ingested payloads", "namespace", cfg.Namespace.Value, "action", cfg.Namespace.Action.String())
		sink = processor.NewNamespace(sink, cfg.Namespace, logger)
	}
	if cfg.Redaction.Enabled() {
		logger.Info("redacting string literals and re-keying branch and site keys in ingested payloads",
			"blocked_values", len(cfg.Redaction.BlockedValues), "all_literals", cfg.Redaction.AllLiterals,
			"secret_fingerprint", fingerprint)
		sink = processor.NewRedaction(sink, cfg.Redaction, logger)
	}
	handler := ingest.NewHandler(sink, logger, ingest.WithMaxConcurrentDecodes(cfg.MaxDecodes))

	ingestMux := http.NewServeMux()
	handler.Register(ingestMux)

	var ingestHandler http.Handler = ingestMux
	if cfg.AuthTokens != nil {
		ingestHandler = auth.RequireBearerToken(cfg.AuthTokens, ingestMux)
	} else {
		logger.Warn("OTHERLODE_COLLECTOR_AUTH_TOKEN and OTHERLODE_COLLECTOR_AUTH_TOKEN_FILE not set; ingest endpoints are unauthenticated")
	}
	if cfg.Limiter != nil {
		ingestHandler = cfg.Limiter.Middleware(ingestHandler)
	} else {
		logger.Warn("rate limiting disabled; ingest endpoints accept requests unthrottled")
	}
	mux.Handle("/v1/otherlode/", ingestHandler)

	mux.HandleFunc("GET "+healthzPath, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	mux.Handle("GET /metrics", metrics.Handler())

	return fwd, nil
}
