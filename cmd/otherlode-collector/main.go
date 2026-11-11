// Command otherlode-collector receives the Otherlode agent's payloads, runs its
// processors and forwards them to a backend. With no backend it only logs them.
// The argument "healthcheck" probes /healthz and exits 0 or 1.
package main

import (
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"errors"
	"fmt"
	"log/slog"
	"math"
	"net"
	"net/http"
	"os"
	"os/signal"
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"sync/atomic"
	"syscall"
	"time"
	"unicode/utf8"

	"golang.org/x/time/rate"

	"github.com/otherlodehq/otherlode-collector/ingest"
	"github.com/otherlodehq/otherlode-collector/internal/auth"
	"github.com/otherlodehq/otherlode-collector/internal/forward"
	"github.com/otherlodehq/otherlode-collector/internal/processor"
	"github.com/otherlodehq/otherlode-collector/internal/ratelimit"
	"github.com/otherlodehq/otherlode-collector/internal/tokenfile"
)

const (
	defaultAddr = ":4319"

	// shutdownTimeout must not exceed the 10 seconds docker stop waits between
	// SIGTERM and SIGKILL.
	shutdownTimeout = 10 * time.Second

	defaultRateLimitRPS   = 5
	defaultRateLimitBurst = 20

	authFileLabel    = "auth"
	forwardFileLabel = "forward"
)

func main() {
	if len(os.Args) > 1 {
		os.Exit(runSubcommand(context.Background(), os.Args[1:], os.Getenv, os.Stderr))
	}

	level := new(slog.LevelVar)
	logger := slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: level}))

	logLevel, err := resolveLogLevel(os.Getenv("OTHERLODE_COLLECTOR_LOG_LEVEL"))
	if err != nil {
		logger.Error(err.Error())
		os.Exit(1)
	}
	level.Set(logLevel)

	addr := resolveAddr(os.Getenv("OTHERLODE_COLLECTOR_ADDR"))

	redactCfg, err := resolveRedaction(os.Getenv("OTHERLODE_COLLECTOR_REDACT_BLOCKED_VALUES"), os.Getenv("OTHERLODE_COLLECTOR_REDACT_ALL_LITERALS"))
	if err != nil {
		logger.Error(err.Error())
		os.Exit(1)
	}
	redactCfg.Secret, err = readRedactionSecret(os.Getenv, redactCfg.Enabled())
	if err != nil {
		logger.Error(err.Error())
		os.Exit(1)
	}

	authTokens, authFile, err := resolveAuthTokens(os.Getenv, redactCfg.Secret)
	if err != nil {
		logger.Error(err.Error())
		os.Exit(1)
	}

	rps, burst, err := resolveRateLimit(os.Getenv("OTHERLODE_COLLECTOR_RATE_LIMIT_RPS"), os.Getenv("OTHERLODE_COLLECTOR_RATE_LIMIT_BURST"))
	if err != nil {
		logger.Error(err.Error())
		os.Exit(1)
	}
	ipv6Prefix, err := resolveIPv6Prefix(os.Getenv("OTHERLODE_COLLECTOR_RATE_LIMIT_IPV6_PREFIX"))
	if err != nil {
		logger.Error(err.Error())
		os.Exit(1)
	}
	var limiter *ratelimit.Limiter
	if rps > 0 {
		opts := []ratelimit.Option{ratelimit.WithIPv6Prefix(ipv6Prefix)}
		if header := os.Getenv("OTHERLODE_COLLECTOR_CLIENT_IP_HEADER"); header != "" {
			opts = append(opts, ratelimit.WithClientIPHeader(header))
		}
		limiter = ratelimit.New(rps, burst, opts...)
		defer limiter.Stop()
	}

	maxDecodes, err := resolveMaxConcurrentDecodes(os.Getenv)
	if err != nil {
		logger.Error(err.Error())
		os.Exit(1)
	}

	forwardCfg, forwardFile, err := resolveForwardConfig(os.Getenv, redactCfg.Secret)
	if err != nil {
		logger.Error(err.Error())
		os.Exit(1)
	}

	envCfg, err := resolveEnvironment(os.Getenv("OTHERLODE_COLLECTOR_ENVIRONMENT"), os.Getenv("OTHERLODE_COLLECTOR_ENVIRONMENT_ACTION"))
	if err != nil {
		logger.Error(err.Error())
		os.Exit(1)
	}

	nsCfg, err := resolveNamespace(os.Getenv("OTHERLODE_COLLECTOR_SERVICE_NAMESPACE"), os.Getenv("OTHERLODE_COLLECTOR_SERVICE_NAMESPACE_ACTION"))
	if err != nil {
		logger.Error(err.Error())
		os.Exit(1)
	}

	mux := http.NewServeMux()
	fwd, err := registerRoutes(mux, routeConfig{
		Logger:      logger,
		AuthTokens:  authTokens,
		Limiter:     limiter,
		MaxDecodes:  maxDecodes,
		Forward:     forwardCfg,
		Environment: envCfg,
		Namespace:   nsCfg,
		Redaction:   redactCfg,
	})
	if err != nil {
		logger.Error(err.Error())
		os.Exit(1)
	}

	srv := &http.Server{
		Handler:           mux,
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       10 * time.Second,
		WriteTimeout:      10 * time.Second,
		IdleTimeout:       60 * time.Second,
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	if authFile != nil {
		go authFile.Watch(ctx, tokenfile.ReloadInterval, logger)
	}
	if forwardFile != nil && fwd != nil {
		go forwardFile.Watch(ctx, tokenfile.ReloadInterval, logger)
	}

	ln, err := listen(addr, logger)
	if err != nil {
		logger.Error(err.Error())
		os.Exit(1)
	}
	serveErr := make(chan error, 1)
	go func() {
		serveErr <- srv.Serve(ln)
	}()

	select {
	case err := <-serveErr:
		if err != nil && !errors.Is(err, http.ErrServerClosed) {
			logger.Error("server stopped", "error", err)
			os.Exit(1)
		}
	case <-ctx.Done():
		stop()
		logger.Info("shutting down")

		var drain func(context.Context)
		if fwd != nil {
			drain = fwd.Shutdown
		}
		if err := shutdown(srv, drain, shutdownTimeout); err != nil {
			logger.Error("graceful shutdown failed", "error", err)
			os.Exit(1)
		}
	}
}

func listen(addr string, logger *slog.Logger) (net.Listener, error) {
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		return nil, fmt.Errorf("OTHERLODE_COLLECTOR_ADDR: %w", err)
	}
	logger.Info("otherlode-collector listening", "addr", addr, "version", version)
	return ln, nil
}

func shutdown(srv *http.Server, drain func(context.Context), timeout time.Duration) error {
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	drained := make(chan struct{})
	go func() {
		defer close(drained)
		if drain != nil {
			drain(ctx)
		}
	}()
	err := srv.Shutdown(ctx)
	<-drained
	return err
}

func resolveLogLevel(raw string) (slog.Level, error) {
	if raw == "" {
		return slog.LevelInfo, nil
	}
	var level slog.Level
	if err := level.UnmarshalText([]byte(raw)); err != nil {
		return 0, fmt.Errorf("OTHERLODE_COLLECTOR_LOG_LEVEL: %w", err)
	}
	return level, nil
}

func resolveAuthTokens(getenv func(string) string, secret []byte) (*auth.TokenSet, *tokenfile.File, error) {
	raw := getenv("OTHERLODE_COLLECTOR_AUTH_TOKEN")
	path := getenv("OTHERLODE_COLLECTOR_AUTH_TOKEN_FILE")
	insecureNoAuth := false
	if noAuthRaw := getenv("OTHERLODE_COLLECTOR_INSECURE_NO_AUTH"); noAuthRaw != "" {
		var err error
		if insecureNoAuth, err = strconv.ParseBool(noAuthRaw); err != nil {
			return nil, nil, fmt.Errorf("OTHERLODE_COLLECTOR_INSECURE_NO_AUTH: %w", err)
		}
	}
	if raw != "" && path != "" {
		return nil, nil, errors.New("OTHERLODE_COLLECTOR_AUTH_TOKEN and OTHERLODE_COLLECTOR_AUTH_TOKEN_FILE are both set; set one")
	}

	if path != "" {
		tokens := new(auth.TokenSet)
		check := allOf(tokenfile.AtLeastOne, notSecret(secret,
			"the file holds the redaction secret, which equals an agent auth token that every agent holds; use a secret of its own"))
		file, err := tokenfile.Open(path, authFileLabel, check, tokens.Store)
		if err != nil {
			return nil, nil, fmt.Errorf("OTHERLODE_COLLECTOR_AUTH_TOKEN_FILE: %w", err)
		}
		return tokens, file, nil
	}

	if raw != "" {
		list, err := parseTokenList(raw)
		if err != nil {
			return nil, nil, fmt.Errorf("OTHERLODE_COLLECTOR_AUTH_TOKEN: %w", err)
		}
		if err := notSecret(secret, "the list holds the redaction secret, which equals an agent auth token that every agent holds; "+
			"use a secret of its own")(list); err != nil {
			return nil, nil, fmt.Errorf("OTHERLODE_COLLECTOR_AUTH_TOKEN: %w", err)
		}
		return auth.NewTokenSet(list), nil, nil
	}

	if insecureNoAuth {
		return nil, nil, nil
	}
	return nil, nil, errors.New("neither OTHERLODE_COLLECTOR_AUTH_TOKEN nor OTHERLODE_COLLECTOR_AUTH_TOKEN_FILE is set; " +
		"refusing to start without auth (set OTHERLODE_COLLECTOR_INSECURE_NO_AUTH=1 to run unauthenticated)")
}

func parseTokenList(raw string) ([]string, error) {
	parts := strings.Split(raw, ",")
	tokens := make([]string, 0, len(parts))
	for i, part := range parts {
		token := strings.TrimSpace(part)
		if token == "" {
			return nil, fmt.Errorf("entry %d of %d is empty", i+1, len(parts))
		}
		tokens = append(tokens, token)
	}
	return tokens, nil
}

type forwardKey struct {
	key atomic.Pointer[string]
}

func (k *forwardKey) Token() string {
	if p := k.key.Load(); p != nil {
		return *p
	}
	return ""
}

func (k *forwardKey) store(tokens []string) {
	k.key.Store(&tokens[0])
}

func checkHeaderValue(key string) error {
	for i := 0; i < len(key); i++ {
		if c := key[i]; c < 0x20 || c == 0x7f {
			return fmt.Errorf("the key holds a control character at byte %d", i)
		}
	}
	return nil
}

func checkForwardKeys(tokens []string) error {
	if err := tokenfile.ExactlyOne(tokens); err != nil {
		return err
	}
	return checkHeaderValue(tokens[0])
}

func resolveRateLimit(rpsRaw, burstRaw string) (rate.Limit, int, error) {
	rps := float64(defaultRateLimitRPS)
	if rpsRaw != "" {
		parsed, err := strconv.ParseFloat(rpsRaw, 64)
		if err != nil {
			return 0, 0, fmt.Errorf("OTHERLODE_COLLECTOR_RATE_LIMIT_RPS: %w", err)
		}
		rps = parsed
	}

	burst := defaultRateLimitBurst
	if burstRaw != "" {
		parsed, err := strconv.Atoi(burstRaw)
		if err != nil {
			return 0, 0, fmt.Errorf("OTHERLODE_COLLECTOR_RATE_LIMIT_BURST: %w", err)
		}
		burst = parsed
	}

	if math.IsNaN(rps) || math.IsInf(rps, 0) {
		return 0, 0, errors.New("OTHERLODE_COLLECTOR_RATE_LIMIT_RPS must be a finite number")
	}
	if rps < 0 {
		return 0, 0, errors.New("OTHERLODE_COLLECTOR_RATE_LIMIT_RPS must not be negative")
	}
	if rps > 0 && burst <= 0 {
		return 0, 0, errors.New("OTHERLODE_COLLECTOR_RATE_LIMIT_BURST must be positive when rate limiting is enabled")
	}

	return rate.Limit(rps), burst, nil
}

func resolveIPv6Prefix(raw string) (int, error) {
	if raw == "" {
		return ratelimit.DefaultIPv6Prefix, nil
	}
	bits, err := strconv.Atoi(raw)
	if err != nil {
		return 0, fmt.Errorf("OTHERLODE_COLLECTOR_RATE_LIMIT_IPV6_PREFIX: %w", err)
	}
	if bits < 1 || bits > 128 {
		return 0, errors.New("OTHERLODE_COLLECTOR_RATE_LIMIT_IPV6_PREFIX must be from 1 to 128")
	}
	return bits, nil
}

func resolveForwardConfig(getenv func(string) string, secret []byte) (forward.Config, *tokenfile.File, error) {
	cfg := forward.Config{URL: getenv("OTHERLODE_COLLECTOR_FORWARD_URL")}
	if cfg.URL != "" {
		if _, _, err := forward.ValidateURL(cfg.URL); err != nil {
			return forward.Config{}, nil, fmt.Errorf("OTHERLODE_COLLECTOR_FORWARD_URL: %w", err)
		}
	}

	raw := getenv("OTHERLODE_COLLECTOR_FORWARD_AUTH_TOKEN")
	token := strings.TrimSpace(raw)
	path := getenv("OTHERLODE_COLLECTOR_FORWARD_AUTH_TOKEN_FILE")
	if cfg.URL == "" && (raw != "" || path != "") {
		return forward.Config{}, nil, errors.New(
			"OTHERLODE_COLLECTOR_FORWARD_AUTH_TOKEN or OTHERLODE_COLLECTOR_FORWARD_AUTH_TOKEN_FILE is set but OTHERLODE_COLLECTOR_FORWARD_URL is empty; " +
				"set OTHERLODE_COLLECTOR_FORWARD_URL or unset the key variable")
	}
	var file *tokenfile.File
	switch {
	case raw != "" && path != "":
		return forward.Config{}, nil, errors.New(
			"OTHERLODE_COLLECTOR_FORWARD_AUTH_TOKEN and OTHERLODE_COLLECTOR_FORWARD_AUTH_TOKEN_FILE are both set; set one")
	case raw != "" && token == "":
		return forward.Config{}, nil, errors.New("OTHERLODE_COLLECTOR_FORWARD_AUTH_TOKEN holds only spaces")
	case path != "":
		key := new(forwardKey)
		var err error
		check := allOf(checkForwardKeys, notSecret(secret,
			"the file holds the redaction secret, which equals the forward key that the backend holds; use a secret of its own"))
		if file, err = tokenfile.Open(path, forwardFileLabel, check, key.store); err != nil {
			return forward.Config{}, nil, fmt.Errorf("OTHERLODE_COLLECTOR_FORWARD_AUTH_TOKEN_FILE: %w", err)
		}
		cfg.AuthToken = key
	case token != "":
		if err := checkHeaderValue(token); err != nil {
			return forward.Config{}, nil, fmt.Errorf("OTHERLODE_COLLECTOR_FORWARD_AUTH_TOKEN: %w", err)
		}
		if err := notSecret(secret, "the value is the redaction secret, which equals the forward key that the backend holds; "+
			"use a secret of its own")([]string{token}); err != nil {
			return forward.Config{}, nil, fmt.Errorf("OTHERLODE_COLLECTOR_FORWARD_AUTH_TOKEN: %w", err)
		}
		cfg.AuthToken = forward.StaticToken(token)
	}

	var err error
	if cfg.RequestTimeout, err = positiveDurationEnv(getenv, "OTHERLODE_COLLECTOR_FORWARD_REQUEST_TIMEOUT"); err != nil {
		return forward.Config{}, nil, err
	}
	if cfg.RequestTimeout >= requestBudget {
		return forward.Config{}, nil, fmt.Errorf(
			"OTHERLODE_COLLECTOR_FORWARD_REQUEST_TIMEOUT is %s, which is not below the %s time budget of one request; set it lower",
			cfg.RequestTimeout, requestBudget)
	}
	if cfg.MaxConcurrentForwards, err = positiveIntEnv(getenv, "OTHERLODE_COLLECTOR_MAX_CONCURRENT_FORWARDS"); err != nil {
		return forward.Config{}, nil, err
	}
	return cfg, file, nil
}

func resolveMaxConcurrentDecodes(getenv func(string) string) (int, error) {
	n, err := positiveIntEnv(getenv, "OTHERLODE_COLLECTOR_MAX_CONCURRENT_DECODES")
	if err != nil {
		return 0, err
	}
	if n == 0 {
		return runtime.GOMAXPROCS(0), nil
	}
	return n, nil
}

func positiveIntEnv(getenv func(string) string, name string) (int, error) {
	raw := getenv(name)
	if raw == "" {
		return 0, nil
	}
	n, err := strconv.Atoi(raw)
	if err != nil {
		return 0, fmt.Errorf("%s: %w", name, err)
	}
	if n <= 0 {
		return 0, fmt.Errorf("%s must be positive", name)
	}
	return n, nil
}

func positiveDurationEnv(getenv func(string) string, name string) (time.Duration, error) {
	raw := getenv(name)
	if raw == "" {
		return 0, nil
	}
	d, err := time.ParseDuration(raw)
	if err != nil {
		return 0, fmt.Errorf("%s: %w", name, err)
	}
	if d <= 0 {
		return 0, fmt.Errorf("%s must be positive", name)
	}
	return d, nil
}

func resolveEnvironment(valueRaw, actionRaw string) (processor.EnvironmentConfig, error) {
	value := strings.TrimSpace(valueRaw)
	actionRaw = strings.TrimSpace(actionRaw)
	if !utf8.ValidString(value) {
		return processor.EnvironmentConfig{}, errors.New("OTHERLODE_COLLECTOR_ENVIRONMENT is not valid UTF-8")
	}

	action, err := processor.ParseAction(actionRaw)
	if err != nil {
		return processor.EnvironmentConfig{}, fmt.Errorf("OTHERLODE_COLLECTOR_ENVIRONMENT_ACTION: %w", err)
	}

	if value == "" {
		if actionRaw != "" {
			return processor.EnvironmentConfig{}, errors.New(
				"OTHERLODE_COLLECTOR_ENVIRONMENT_ACTION is set but OTHERLODE_COLLECTOR_ENVIRONMENT is empty; " +
					"set OTHERLODE_COLLECTOR_ENVIRONMENT or unset OTHERLODE_COLLECTOR_ENVIRONMENT_ACTION")
		}
		return processor.EnvironmentConfig{}, nil
	}

	if len(value) > ingest.MaxResourceValueBytes {
		return processor.EnvironmentConfig{}, fmt.Errorf("OTHERLODE_COLLECTOR_ENVIRONMENT is %d bytes, over the %d-byte limit", len(value), ingest.MaxResourceValueBytes)
	}

	return processor.EnvironmentConfig{Value: value, Action: action}, nil
}

func resolveNamespace(valueRaw, actionRaw string) (processor.NamespaceConfig, error) {
	value := strings.TrimSpace(valueRaw)
	actionRaw = strings.TrimSpace(actionRaw)
	if !utf8.ValidString(value) {
		return processor.NamespaceConfig{}, errors.New("OTHERLODE_COLLECTOR_SERVICE_NAMESPACE is not valid UTF-8")
	}

	action, err := processor.ParseAction(actionRaw)
	if err != nil {
		return processor.NamespaceConfig{}, fmt.Errorf("OTHERLODE_COLLECTOR_SERVICE_NAMESPACE_ACTION: %w", err)
	}

	if value == "" {
		if actionRaw != "" {
			return processor.NamespaceConfig{}, errors.New(
				"OTHERLODE_COLLECTOR_SERVICE_NAMESPACE_ACTION is set but OTHERLODE_COLLECTOR_SERVICE_NAMESPACE is empty; " +
					"set OTHERLODE_COLLECTOR_SERVICE_NAMESPACE or unset OTHERLODE_COLLECTOR_SERVICE_NAMESPACE_ACTION")
		}
		return processor.NamespaceConfig{}, nil
	}
	if value == "." || value == ".." {
		return processor.NamespaceConfig{}, fmt.Errorf("OTHERLODE_COLLECTOR_SERVICE_NAMESPACE is %q, which no URL path can name", value)
	}

	if len(value) > ingest.MaxResourceValueBytes {
		return processor.NamespaceConfig{}, fmt.Errorf("OTHERLODE_COLLECTOR_SERVICE_NAMESPACE is %d bytes, over the %d-byte limit", len(value), ingest.MaxResourceValueBytes)
	}

	return processor.NamespaceConfig{Value: value, Action: action}, nil
}

func resolveRedaction(blockedRaw, allLiteralsRaw string) (processor.RedactionConfig, error) {
	var cfg processor.RedactionConfig
	for i, line := range strings.Split(blockedRaw, "\n") {
		line = strings.TrimSuffix(line, "\r")
		if strings.TrimSpace(line) == "" {
			continue
		}
		re, err := regexp.Compile(line)
		if err != nil {
			return processor.RedactionConfig{}, fmt.Errorf("OTHERLODE_COLLECTOR_REDACT_BLOCKED_VALUES line %d: %w", i+1, err)
		}
		cfg.BlockedValues = append(cfg.BlockedValues, re)
	}

	if allLiteralsRaw != "" {
		all, err := strconv.ParseBool(allLiteralsRaw)
		if err != nil {
			return processor.RedactionConfig{}, fmt.Errorf("OTHERLODE_COLLECTOR_REDACT_ALL_LITERALS: %w", err)
		}
		cfg.AllLiterals = all
	}
	return cfg, nil
}

func allOf(checks ...func([]string) error) func([]string) error {
	return func(tokens []string) error {
		for _, check := range checks {
			if err := check(tokens); err != nil {
				return err
			}
		}
		return nil
	}
}

func notSecret(secret []byte, message string) func([]string) error {
	if secret == nil {
		return func([]string) error { return nil }
	}
	want := sha256.Sum256(secret)
	return func(tokens []string) error {
		match := 0
		for _, token := range tokens {
			got := sha256.Sum256([]byte(token))
			match |= subtle.ConstantTimeCompare(got[:], want[:])
		}
		if match == 1 {
			return errors.New(message)
		}
		return nil
	}
}

func readRedactionSecret(getenv func(string) string, redacting bool) ([]byte, error) {
	const (
		valueVar = "OTHERLODE_COLLECTOR_REDACT_SECRET"
		fileVar  = "OTHERLODE_COLLECTOR_REDACT_SECRET_FILE"
	)
	raw := getenv(valueVar)
	path := getenv(fileVar)
	if raw != "" && path != "" {
		return nil, errors.New(valueVar + " and " + fileVar + " are both set; set one")
	}
	if !redacting {
		if raw != "" || path != "" {
			return nil, errors.New(valueVar + " or " + fileVar + " is set but redaction is off; " +
				"set OTHERLODE_COLLECTOR_REDACT_BLOCKED_VALUES or OTHERLODE_COLLECTOR_REDACT_ALL_LITERALS, or unset the secret variable")
		}
		return nil, nil
	}

	var name, secret string
	switch {
	case path != "":
		name = fileVar
		tokens, err := tokenfile.Read(path)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", name, err)
		}
		if err := tokenfile.ExactlyOne(tokens); err != nil {
			return nil, fmt.Errorf("%s: %s: %w", name, path, err)
		}
		secret = tokens[0]
	case raw != "":
		name = valueVar
		secret = strings.TrimSpace(raw)
		if secret == "" {
			return nil, errors.New(name + " holds only spaces")
		}
	default:
		return nil, errors.New("redaction is on but neither " + valueVar + " nor " + fileVar + " is set; " +
			"refusing to start, since plain branch and site keys let anyone who holds them test guesses at a redacted literal")
	}

	if err := processor.CheckSecret([]byte(secret)); err != nil {
		return nil, fmt.Errorf("%s: %w", name, err)
	}
	return []byte(secret), nil
}
