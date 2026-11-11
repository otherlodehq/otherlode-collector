package main

import (
	"fmt"
	"log/slog"
	"net/http"
	"time"

	"github.com/otherlodehq/otherlode-collector/ingest"
	"github.com/otherlodehq/otherlode-collector/internal/auth"
	"github.com/otherlodehq/otherlode-collector/internal/forward"
	"github.com/otherlodehq/otherlode-collector/internal/processor"
	"github.com/otherlodehq/otherlode-collector/internal/ratelimit"
	"github.com/otherlodehq/otherlode-collector/metrics"
)

var (
	// requestBudget must stay below the 10 second server write timeout.
	requestBudget = 9 * time.Second

	decodeSlotWait = time.Second
)

type routeConfig struct {
	Logger      *slog.Logger
	AuthTokens  *auth.TokenSet
	Limiter     *ratelimit.Limiter
	MaxDecodes  int
	Forward     forward.Config
	Environment processor.EnvironmentConfig
	Namespace   processor.NamespaceConfig
	Redaction   processor.RedactionConfig
}

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
	handler := ingest.NewHandler(sink, logger,
		ingest.WithMaxConcurrentDecodes(cfg.MaxDecodes),
		ingest.WithSlotWait(decodeSlotWait),
		ingest.WithRequestBudget(requestBudget),
	)

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
