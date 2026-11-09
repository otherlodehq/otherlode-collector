package main

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"
	"time"

	"golang.org/x/time/rate"

	"github.com/otherlodehq/otherlode-collector/ingest"
	"github.com/otherlodehq/otherlode-collector/internal/forward"
	"github.com/otherlodehq/otherlode-collector/internal/processor"
	"github.com/otherlodehq/otherlode-collector/metrics"
)

// writeTokenFile writes content to a file in a fresh temp directory, so
// each test owns its token file.
func writeTokenFile(t *testing.T, content string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "tokens")
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatalf("write token file: %v", err)
	}
	return path
}

// replaceTokenFile swaps path's content through a rename, so a re-read
// never sees a half-written file.
func replaceTokenFile(t *testing.T, path, content string) {
	t.Helper()
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, []byte(content), 0o600); err != nil {
		t.Fatalf("write token file: %v", err)
	}
	if err := os.Rename(tmp, path); err != nil {
		t.Fatalf("rename token file: %v", err)
	}
}

func TestParseTokenList(t *testing.T) {
	tests := map[string]struct {
		raw     string
		want    []string
		wantErr bool
	}{
		"single token":         {raw: "s3cret", want: []string{"s3cret"}},
		"several tokens":       {raw: "a,b,c", want: []string{"a", "b", "c"}},
		"spaces trimmed":       {raw: " a , b\t,c ", want: []string{"a", "b", "c"}},
		"inner spaces kept":    {raw: "a b,c", want: []string{"a b", "c"}},
		"empty middle entry":   {raw: "a,,b", wantErr: true},
		"leading comma":        {raw: ",a", wantErr: true},
		"trailing comma":       {raw: "a,", wantErr: true},
		"entry of only spaces": {raw: "a,   ,b", wantErr: true},
		"only spaces":          {raw: "   ", wantErr: true},
		"only a comma":         {raw: ",", wantErr: true},
	}

	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			got, err := parseTokenList(tt.raw)
			if tt.wantErr {
				if err == nil {
					t.Fatalf("expected an error, got %q", got)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if !slices.Equal(got, tt.want) {
				t.Fatalf("parseTokenList(%q) = %q, want %q", tt.raw, got, tt.want)
			}
		})
	}
}

func TestResolveAuthTokens(t *testing.T) {
	goodFile := writeTokenFile(t, "# agents\nfile-a\n\n  file-b  \n")
	emptyFile := writeTokenFile(t, "")
	commentFile := writeTokenFile(t, "# no tokens yet\n\n")
	missingFile := filepath.Join(t.TempDir(), "missing")

	tests := map[string]struct {
		env      map[string]string
		accepted []string
		refused  []string
		noAuth   bool
		wantFile bool
		wantErr  bool
	}{
		"single token": {
			env:      map[string]string{"OTHERLODE_COLLECTOR_AUTH_TOKEN": "s3cret"},
			accepted: []string{"s3cret"},
			refused:  []string{"other"},
		},
		"token list": {
			env:      map[string]string{"OTHERLODE_COLLECTOR_AUTH_TOKEN": "old, new"},
			accepted: []string{"old", "new"},
			refused:  []string{"old, new", " new", "other"},
		},
		"token and opt-out, token wins": {
			env:      map[string]string{"OTHERLODE_COLLECTOR_AUTH_TOKEN": "s3cret", "OTHERLODE_COLLECTOR_INSECURE_NO_AUTH": "1"},
			accepted: []string{"s3cret"},
			refused:  []string{""},
		},
		"empty list entry": {
			env:     map[string]string{"OTHERLODE_COLLECTOR_AUTH_TOKEN": "a,,b"},
			wantErr: true,
		},
		"empty list entry with opt-out": {
			env:     map[string]string{"OTHERLODE_COLLECTOR_AUTH_TOKEN": "a,", "OTHERLODE_COLLECTOR_INSECURE_NO_AUTH": "1"},
			wantErr: true,
		},
		"token file": {
			env:      map[string]string{"OTHERLODE_COLLECTOR_AUTH_TOKEN_FILE": goodFile},
			accepted: []string{"file-a", "file-b"},
			refused:  []string{"# agents", "", "other"},
			wantFile: true,
		},
		"both variables set": {
			env:     map[string]string{"OTHERLODE_COLLECTOR_AUTH_TOKEN": "s3cret", "OTHERLODE_COLLECTOR_AUTH_TOKEN_FILE": goodFile},
			wantErr: true,
		},
		"missing token file": {
			env:     map[string]string{"OTHERLODE_COLLECTOR_AUTH_TOKEN_FILE": missingFile},
			wantErr: true,
		},
		"missing token file with opt-out": {
			env:     map[string]string{"OTHERLODE_COLLECTOR_AUTH_TOKEN_FILE": missingFile, "OTHERLODE_COLLECTOR_INSECURE_NO_AUTH": "1"},
			wantErr: true,
		},
		"empty token file with opt-out": {
			env:     map[string]string{"OTHERLODE_COLLECTOR_AUTH_TOKEN_FILE": emptyFile, "OTHERLODE_COLLECTOR_INSECURE_NO_AUTH": "1"},
			wantErr: true,
		},
		"comment-only token file with opt-out": {
			env:     map[string]string{"OTHERLODE_COLLECTOR_AUTH_TOKEN_FILE": commentFile, "OTHERLODE_COLLECTOR_INSECURE_NO_AUTH": "true"},
			wantErr: true,
		},
		"nothing set": {
			env:     nil,
			wantErr: true,
		},
		"nothing set, explicit opt-out": {
			env:    map[string]string{"OTHERLODE_COLLECTOR_INSECURE_NO_AUTH": "1"},
			noAuth: true,
		},
		"nothing set, opt-out false": {
			env:     map[string]string{"OTHERLODE_COLLECTOR_INSECURE_NO_AUTH": "false"},
			wantErr: true,
		},
		"nothing set, unparsable opt-out": {
			env:     map[string]string{"OTHERLODE_COLLECTOR_INSECURE_NO_AUTH": "not-a-bool"},
			wantErr: true,
		},
		"token set, unparsable opt-out": {
			env:     map[string]string{"OTHERLODE_COLLECTOR_AUTH_TOKEN": "s3cret", "OTHERLODE_COLLECTOR_INSECURE_NO_AUTH": "yes"},
			wantErr: true,
		},
	}

	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			tokens, file, err := resolveAuthTokens(envFrom(tt.env), nil)
			if tt.wantErr {
				if err == nil {
					t.Fatal("expected an error, got nil")
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if tt.noAuth {
				if tokens != nil || file != nil {
					t.Fatalf("tokens = %v, file = %v, want both nil for no auth", tokens, file)
				}
				return
			}
			if (file != nil) != tt.wantFile {
				t.Fatalf("file returned = %v, want %v", file != nil, tt.wantFile)
			}
			for _, token := range tt.accepted {
				if !tokens.Matches(token) {
					t.Errorf("token %q refused, want accepted", token)
				}
			}
			for _, token := range tt.refused {
				if tokens.Matches(token) {
					t.Errorf("token %q accepted, want refused", token)
				}
			}
		})
	}
}

func TestListen_PortInUse_ErrorsWithoutLoggingListening(t *testing.T) {
	taken, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	defer taken.Close()

	var logs bytes.Buffer
	ln, err := listen(taken.Addr().String(), slog.New(slog.NewJSONHandler(&logs, nil)))
	if err == nil {
		ln.Close()
		t.Fatal("expected an error for a port in use, got nil")
	}
	if strings.Contains(logs.String(), "listening") {
		t.Fatalf("logged %q for a bind that failed", logs.String())
	}
}

func TestListen_FreePort_LogsListening(t *testing.T) {
	var logs bytes.Buffer
	ln, err := listen("127.0.0.1:0", slog.New(slog.NewJSONHandler(&logs, nil)))
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	defer ln.Close()
	if !strings.Contains(logs.String(), `"msg":"otherlode-collector listening"`) {
		t.Fatalf("logs = %q, want the listening line", logs.String())
	}
}

// TestShutdown_SlowHTTPDrain_ForwarderStillGetsItsTime pins that the
// forwarder drains while the HTTP server stops, not after it. A request
// that outlasts the deadline must not leave the forwarder an expired
// context.
func TestShutdown_SlowHTTPDrain_ForwarderStillGetsItsTime(t *testing.T) {
	entered := make(chan struct{})
	release := make(chan struct{})
	srv := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		close(entered)
		<-release
	})}
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	go func() { _ = srv.Serve(ln) }()
	defer srv.Close()
	defer close(release)
	go func() {
		if resp, err := http.Get("http://" + ln.Addr().String()); err == nil {
			resp.Body.Close()
		}
	}()
	<-entered

	var drainErr error
	drain := func(ctx context.Context) {
		// Stands in for one final delivery attempt that needs some time.
		select {
		case <-time.After(100 * time.Millisecond):
		case <-ctx.Done():
		}
		drainErr = ctx.Err()
	}
	if err := shutdown(srv, drain, 500*time.Millisecond); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("shutdown error = %v, want the deadline, since a request was still running", err)
	}
	if drainErr != nil {
		t.Fatalf("the forwarder's context ended before its drain finished: %v", drainErr)
	}
}

func TestShutdown_NoForwarder_StopsServer(t *testing.T) {
	srv := &http.Server{Handler: http.NotFoundHandler()}
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	served := make(chan error, 1)
	go func() { served <- srv.Serve(ln) }()
	if err := shutdown(srv, nil, time.Second); err != nil {
		t.Fatalf("shutdown: %v", err)
	}
	if err := <-served; !errors.Is(err, http.ErrServerClosed) {
		t.Fatalf("Serve returned %v, want http.ErrServerClosed", err)
	}
}

// TestResolveAuthTokens_UnparsableOptOut_ErrorNamesVariableAndValue pins
// that a typo in the opt-out stops startup with a message that points at
// it, instead of the generic "no token set" error.
func TestResolveAuthTokens_UnparsableOptOut_ErrorNamesVariableAndValue(t *testing.T) {
	_, _, err := resolveAuthTokens(envFrom(map[string]string{"OTHERLODE_COLLECTOR_INSECURE_NO_AUTH": "ture"}), nil)
	if err == nil {
		t.Fatal("expected an error, got nil")
	}
	for _, want := range []string{"OTHERLODE_COLLECTOR_INSECURE_NO_AUTH", `"ture"`} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %q does not contain %s", err, want)
		}
	}
}

func TestResolveRateLimit_BothUnset_UsesDefaults(t *testing.T) {
	rps, burst, err := resolveRateLimit("", "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if rps != rate.Limit(defaultRateLimitRPS) {
		t.Fatalf("rps = %v, want %v", rps, defaultRateLimitRPS)
	}
	if burst != defaultRateLimitBurst {
		t.Fatalf("burst = %d, want %d", burst, defaultRateLimitBurst)
	}
}

func TestResolveRateLimit_BothSet_UsesProvidedValues(t *testing.T) {
	rps, burst, err := resolveRateLimit("10", "50")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if rps != rate.Limit(10) {
		t.Fatalf("rps = %v, want 10", rps)
	}
	if burst != 50 {
		t.Fatalf("burst = %d, want 50", burst)
	}
}

func TestResolveRateLimit_RPSZero_DisablesLimiting(t *testing.T) {
	rps, _, err := resolveRateLimit("0", "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if rps != 0 {
		t.Fatalf("rps = %v, want 0", rps)
	}
}

func TestResolveRateLimit_NegativeRPS_Errors(t *testing.T) {
	_, _, err := resolveRateLimit("-1", "")
	if err == nil {
		t.Fatal("expected error, got nil")
	}
}

func TestResolveRateLimit_GarbageRPS_Errors(t *testing.T) {
	_, _, err := resolveRateLimit("not-a-number", "")
	if err == nil {
		t.Fatal("expected error, got nil")
	}
}

func TestResolveRateLimit_GarbageBurst_Errors(t *testing.T) {
	_, _, err := resolveRateLimit("", "not-a-number")
	if err == nil {
		t.Fatal("expected error, got nil")
	}
}

func TestResolveRateLimit_EnabledWithZeroBurst_Errors(t *testing.T) {
	_, _, err := resolveRateLimit("5", "0")
	if err == nil {
		t.Fatal("expected error, got nil")
	}
}

func TestResolveRateLimit_NonFiniteRPS_Errors(t *testing.T) {
	for _, raw := range []string{"NaN", "Inf", "+Inf", "-Inf"} {
		if _, _, err := resolveRateLimit(raw, ""); err == nil {
			t.Errorf("rps %q: expected an error, got nil", raw)
		}
	}
}

func TestResolveIPv6Prefix(t *testing.T) {
	for raw, want := range map[string]int{"": 128, "1": 1, "64": 64, "128": 128} {
		got, err := resolveIPv6Prefix(raw)
		if err != nil {
			t.Errorf("prefix %q: unexpected error: %v", raw, err)
			continue
		}
		if got != want {
			t.Errorf("prefix %q = %d, want %d", raw, got, want)
		}
	}
	for _, raw := range []string{"0", "129", "-1", "abc", "64.5", " "} {
		if _, err := resolveIPv6Prefix(raw); err == nil {
			t.Errorf("prefix %q: expected an error, got nil", raw)
		}
	}
}

func TestResolveLogLevel(t *testing.T) {
	for raw, want := range map[string]slog.Level{
		"":      slog.LevelInfo,
		"debug": slog.LevelDebug,
		"INFO":  slog.LevelInfo,
		"warn":  slog.LevelWarn,
		"Error": slog.LevelError,
	} {
		got, err := resolveLogLevel(raw)
		if err != nil {
			t.Errorf("level %q: unexpected error: %v", raw, err)
			continue
		}
		if got != want {
			t.Errorf("level %q = %v, want %v", raw, got, want)
		}
	}
}

func TestResolveLogLevel_Garbage_Errors(t *testing.T) {
	if _, err := resolveLogLevel("loud"); err == nil {
		t.Fatal("expected an error for an unknown level, got nil")
	}
}

func envFrom(vars map[string]string) func(string) string {
	return func(name string) string { return vars[name] }
}

func TestResolveForwardConfig_Unset_LeavesZeroValues(t *testing.T) {
	cfg, file, err := resolveForwardConfig(envFrom(nil), nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg != (forward.Config{}) {
		t.Fatalf("config = %+v, want all zero so the forward package applies its defaults", cfg)
	}
	if file != nil {
		t.Fatal("token file returned with no file configured")
	}
}

func TestResolveForwardConfig_AllSet_ParsesEveryField(t *testing.T) {
	cfg, _, err := resolveForwardConfig(envFrom(map[string]string{
		"OTHERLODE_COLLECTOR_FORWARD_URL":             "https://backend.example.com",
		"OTHERLODE_COLLECTOR_FORWARD_AUTH_TOKEN":      "backend-secret",
		"OTHERLODE_COLLECTOR_FORWARD_REQUEST_TIMEOUT": "5s",
		"OTHERLODE_COLLECTOR_MAX_CONCURRENT_FORWARDS": "12",
	}), nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	want := forward.Config{
		URL:            "https://backend.example.com",
		AuthToken:      forward.StaticToken("backend-secret"),
		RequestTimeout: 5 * time.Second,

		MaxConcurrentForwards: 12,
	}
	if cfg != want {
		t.Fatalf("config = %+v, want %+v", cfg, want)
	}
}

func TestResolveForwardConfig_AuthToken(t *testing.T) {
	oneKey := writeTokenFile(t, "# backend key\n  file-key  \n\n")
	noKey := writeTokenFile(t, "# rotating\n")
	twoKeys := writeTokenFile(t, "key-1\nkey-2\n")
	controlKey := writeTokenFile(t, "bad\x01key\n")
	missing := filepath.Join(t.TempDir(), "missing")

	tests := map[string]struct {
		env      map[string]string
		want     string
		wantFile bool
		wantErr  bool
		errHas   string
		noURL    bool
	}{
		"no key": {
			env:  nil,
			want: "",
		},
		"key from variable, never split": {
			env:  map[string]string{"OTHERLODE_COLLECTOR_FORWARD_AUTH_TOKEN": "a,b"},
			want: "a,b",
		},
		"key from variable, spaces trimmed": {
			env:  map[string]string{"OTHERLODE_COLLECTOR_FORWARD_AUTH_TOKEN": " backend-secret\n"},
			want: "backend-secret",
		},
		"variable with only spaces": {
			env:     map[string]string{"OTHERLODE_COLLECTOR_FORWARD_AUTH_TOKEN": " \n"},
			wantErr: true,
		},
		"variable with only spaces and a file": {
			env:     map[string]string{"OTHERLODE_COLLECTOR_FORWARD_AUTH_TOKEN": " ", "OTHERLODE_COLLECTOR_FORWARD_AUTH_TOKEN_FILE": oneKey},
			wantErr: true,
			errHas:  "both set",
		},
		"key from file": {
			env:      map[string]string{"OTHERLODE_COLLECTOR_FORWARD_AUTH_TOKEN_FILE": oneKey},
			want:     "file-key",
			wantFile: true,
		},
		"both variables set": {
			env:     map[string]string{"OTHERLODE_COLLECTOR_FORWARD_AUTH_TOKEN": "backend-secret", "OTHERLODE_COLLECTOR_FORWARD_AUTH_TOKEN_FILE": oneKey},
			wantErr: true,
		},
		"file with no key": {
			env:     map[string]string{"OTHERLODE_COLLECTOR_FORWARD_AUTH_TOKEN_FILE": noKey},
			wantErr: true,
		},
		"file with two keys": {
			env:     map[string]string{"OTHERLODE_COLLECTOR_FORWARD_AUTH_TOKEN_FILE": twoKeys},
			wantErr: true,
		},
		"missing file": {
			env:     map[string]string{"OTHERLODE_COLLECTOR_FORWARD_AUTH_TOKEN_FILE": missing},
			wantErr: true,
		},
		"variable with a newline inside": {
			env:     map[string]string{"OTHERLODE_COLLECTOR_FORWARD_AUTH_TOKEN": "bad\nkey"},
			wantErr: true,
			errHas:  "control character",
		},
		"variable with a tab inside": {
			env:     map[string]string{"OTHERLODE_COLLECTOR_FORWARD_AUTH_TOKEN": "bad\tkey"},
			wantErr: true,
			errHas:  "control character",
		},
		"variable with DEL": {
			env:     map[string]string{"OTHERLODE_COLLECTOR_FORWARD_AUTH_TOKEN": "bad\x7fkey"},
			wantErr: true,
			errHas:  "control character",
		},
		"file with a control character": {
			env:     map[string]string{"OTHERLODE_COLLECTOR_FORWARD_AUTH_TOKEN_FILE": controlKey},
			wantErr: true,
			errHas:  "control character",
		},
		"key variable without a URL": {
			env:     map[string]string{"OTHERLODE_COLLECTOR_FORWARD_AUTH_TOKEN": "backend-secret"},
			noURL:   true,
			wantErr: true,
			errHas:  "OTHERLODE_COLLECTOR_FORWARD_URL",
		},
		"key file without a URL": {
			env:     map[string]string{"OTHERLODE_COLLECTOR_FORWARD_AUTH_TOKEN_FILE": oneKey},
			noURL:   true,
			wantErr: true,
			errHas:  "OTHERLODE_COLLECTOR_FORWARD_URL",
		},
	}

	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			env := map[string]string{}
			if !tt.noURL {
				env["OTHERLODE_COLLECTOR_FORWARD_URL"] = "http://backend.example.com"
			}
			for k, v := range tt.env {
				env[k] = v
			}
			cfg, file, err := resolveForwardConfig(envFrom(env), nil)
			if tt.wantErr {
				if err == nil {
					t.Fatal("expected an error, got nil")
				}
				if !strings.Contains(err.Error(), tt.errHas) {
					t.Fatalf("error %q does not contain %q", err, tt.errHas)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if (file != nil) != tt.wantFile {
				t.Fatalf("file returned = %v, want %v", file != nil, tt.wantFile)
			}
			got := ""
			if cfg.AuthToken != nil {
				got = cfg.AuthToken.Token()
			}
			if got != tt.want {
				t.Fatalf("key = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestResolveEnvironment(t *testing.T) {
	tests := map[string]struct {
		value   string
		action  string
		want    processor.EnvironmentConfig
		wantErr bool
	}{
		"unset": {
			value:  "",
			action: "",
			want:   processor.EnvironmentConfig{},
		},
		"value only": {
			value:  "prod",
			action: "",
			want:   processor.EnvironmentConfig{Value: "prod", Action: processor.Insert},
		},
		"value with surrounding spaces": {
			value:  "  prod  ",
			action: "",
			want:   processor.EnvironmentConfig{Value: "prod", Action: processor.Insert},
		},
		"value and upsert": {
			value:  "prod",
			action: "upsert",
			want:   processor.EnvironmentConfig{Value: "prod", Action: processor.Upsert},
		},
		"action with surrounding spaces": {
			value:  "prod",
			action: " upsert ",
			want:   processor.EnvironmentConfig{Value: "prod", Action: processor.Upsert},
		},
		"whitespace-only action without value": {
			value:  "",
			action: "  ",
			want:   processor.EnvironmentConfig{},
		},
		"bad action": {
			value:   "prod",
			action:  "overwrite",
			wantErr: true,
		},
		"action without value": {
			value:   "",
			action:  "upsert",
			wantErr: true,
		},
		"whitespace-only value with action": {
			value:   "   ",
			action:  "upsert",
			wantErr: true,
		},
		"value that is not valid UTF-8": {
			value:   "pr\xffod",
			wantErr: true,
		},
		"value at the byte limit": {
			value: strings.Repeat("e", ingest.MaxResourceValueBytes),
			want:  processor.EnvironmentConfig{Value: strings.Repeat("e", ingest.MaxResourceValueBytes), Action: processor.Insert},
		},
		"value over the byte limit": {
			value:   strings.Repeat("e", ingest.MaxResourceValueBytes+1),
			wantErr: true,
		},
	}

	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			got, err := resolveEnvironment(tt.value, tt.action)
			if tt.wantErr {
				if err == nil {
					t.Fatal("expected an error, got nil")
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got != tt.want {
				t.Fatalf("config = %+v, want %+v", got, tt.want)
			}
		})
	}
}

func TestResolveNamespace(t *testing.T) {
	tests := map[string]struct {
		value   string
		action  string
		want    processor.NamespaceConfig
		wantErr bool
	}{
		"unset": {
			value:  "",
			action: "",
			want:   processor.NamespaceConfig{},
		},
		"value only": {
			value:  "team-a",
			action: "",
			want:   processor.NamespaceConfig{Value: "team-a", Action: processor.Insert},
		},
		"value with surrounding spaces": {
			value:  "  team-a  ",
			action: "",
			want:   processor.NamespaceConfig{Value: "team-a", Action: processor.Insert},
		},
		"value keeps its case": {
			value:  "Team-A",
			action: "",
			want:   processor.NamespaceConfig{Value: "Team-A", Action: processor.Insert},
		},
		"value and insert": {
			value:  "team-a",
			action: "insert",
			want:   processor.NamespaceConfig{Value: "team-a", Action: processor.Insert},
		},
		"value and upsert": {
			value:  "team-a",
			action: "UPSERT",
			want:   processor.NamespaceConfig{Value: "team-a", Action: processor.Upsert},
		},
		"action with surrounding spaces": {
			value:  "team-a",
			action: "upsert\n",
			want:   processor.NamespaceConfig{Value: "team-a", Action: processor.Upsert},
		},
		"bad action": {
			value:   "team-a",
			action:  "overwrite",
			wantErr: true,
		},
		"dot value": {
			value:   ".",
			action:  "",
			wantErr: true,
		},
		"dots value with spaces": {
			value:   " .. ",
			action:  "upsert",
			wantErr: true,
		},
		"action without value": {
			value:   "",
			action:  "insert",
			wantErr: true,
		},
		"whitespace-only value with action": {
			value:   "   ",
			action:  "upsert",
			wantErr: true,
		},
		"value that is not valid UTF-8": {
			value:   "te\xc3am",
			wantErr: true,
		},
		"value at the byte limit": {
			value: strings.Repeat("n", ingest.MaxResourceValueBytes),
			want:  processor.NamespaceConfig{Value: strings.Repeat("n", ingest.MaxResourceValueBytes), Action: processor.Insert},
		},
		"value over the byte limit": {
			value:   strings.Repeat("n", ingest.MaxResourceValueBytes+1),
			wantErr: true,
		},
	}

	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			got, err := resolveNamespace(tt.value, tt.action)
			if tt.wantErr {
				if err == nil {
					t.Fatal("expected an error, got nil")
				}
				if !strings.Contains(err.Error(), "OTHERLODE_COLLECTOR_SERVICE_NAMESPACE") {
					t.Fatalf("error %q does not name the variable", err)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got != tt.want {
				t.Fatalf("config = %+v, want %+v", got, tt.want)
			}
		})
	}
}

func TestResolveForwardConfig_KeyFileReread_BadKeyKeepsLastGoodKey(t *testing.T) {
	path := writeTokenFile(t, "key-1\n")
	cfg, file, err := resolveForwardConfig(envFrom(map[string]string{
		"OTHERLODE_COLLECTOR_FORWARD_URL":             "http://backend.example.com",
		"OTHERLODE_COLLECTOR_FORWARD_AUTH_TOKEN_FILE": path,
	}), nil)
	if err != nil {
		t.Fatalf("resolveForwardConfig: %v", err)
	}
	watchFile(t, file)

	failuresBefore := metrics.TokenReloadFailures.Value("forward")
	replaceTokenFile(t, path, "bad\x01key\n")
	waitUntil(t, 2*time.Second, func() bool { return metrics.TokenReloadFailures.Value("forward") > failuresBefore })
	if got := cfg.AuthToken.Token(); got != "key-1" {
		t.Fatalf("key after a bad re-read = %q, want %q", got, "key-1")
	}

	replaceTokenFile(t, path, "key-2\n")
	waitUntil(t, 2*time.Second, func() bool { return cfg.AuthToken.Token() == "key-2" })
}

func TestResolveForwardConfig_BadValues_Error(t *testing.T) {
	for name, value := range map[string]string{
		"OTHERLODE_COLLECTOR_FORWARD_REQUEST_TIMEOUT": "10",
		"OTHERLODE_COLLECTOR_MAX_CONCURRENT_FORWARDS": "0",
	} {
		if _, _, err := resolveForwardConfig(envFrom(map[string]string{name: value}), nil); err == nil {
			t.Errorf("%s=%q: expected an error, got nil", name, value)
		}
	}
}

func TestResolveForwardConfig_BadMaxConcurrentForwards_Error(t *testing.T) {
	for _, value := range []string{"0", "-1", "many", "1.5"} {
		_, _, err := resolveForwardConfig(envFrom(map[string]string{"OTHERLODE_COLLECTOR_MAX_CONCURRENT_FORWARDS": value}), nil)
		if err == nil || !strings.Contains(err.Error(), "OTHERLODE_COLLECTOR_MAX_CONCURRENT_FORWARDS") {
			t.Errorf("value %q: error = %v, want one naming the variable", value, err)
		}
	}
}

// TestResolveForwardConfig_RequestTimeoutNotBelowTheBudget_Error pins that
// the collector refuses to start with a forward timeout that could outlast
// the time one request has.
func TestResolveForwardConfig_RequestTimeoutNotBelowTheBudget_Error(t *testing.T) {
	for _, value := range []string{requestBudget.String(), (requestBudget + time.Second).String(), "15s", "1h"} {
		_, _, err := resolveForwardConfig(envFrom(map[string]string{"OTHERLODE_COLLECTOR_FORWARD_REQUEST_TIMEOUT": value}), nil)
		if err == nil || !strings.Contains(err.Error(), "OTHERLODE_COLLECTOR_FORWARD_REQUEST_TIMEOUT") {
			t.Errorf("value %q: error = %v, want one naming the variable", value, err)
		}
	}
	below := (requestBudget - time.Millisecond).String()
	if _, _, err := resolveForwardConfig(envFrom(map[string]string{"OTHERLODE_COLLECTOR_FORWARD_REQUEST_TIMEOUT": below}), nil); err != nil {
		t.Errorf("value %q: unexpected error %v", below, err)
	}
}

// TestResolveForwardConfig_RemovedQueueSettings_AreIgnored pins that the
// settings of the removed forward queue are not read, as with any
// OTHERLODE_COLLECTOR_ variable the collector does not know, so an
// existing deployment that still sets them starts.
func TestResolveForwardConfig_RemovedQueueSettings_AreIgnored(t *testing.T) {
	cfg, _, err := resolveForwardConfig(envFrom(map[string]string{
		"OTHERLODE_COLLECTOR_FORWARD_URL":                    "https://backend.example.com",
		"OTHERLODE_COLLECTOR_FORWARD_SHARDS":                 "0",
		"OTHERLODE_COLLECTOR_FORWARD_QUEUE_SIZE":             "-1",
		"OTHERLODE_COLLECTOR_FORWARD_QUEUE_BYTES":            "many",
		"OTHERLODE_COLLECTOR_FORWARD_RETRY_INITIAL_INTERVAL": "soon",
		"OTHERLODE_COLLECTOR_FORWARD_RETRY_MAX_INTERVAL":     "0s",
		"OTHERLODE_COLLECTOR_FORWARD_RETRY_MAX_ELAPSED_TIME": "-5m",
	}), nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if want := (forward.Config{URL: "https://backend.example.com"}); cfg != want {
		t.Fatalf("config = %+v, want %+v", cfg, want)
	}
}

func TestResolveMaxConcurrentDecodes(t *testing.T) {
	cases := []struct {
		name    string
		value   string
		want    int
		wantErr bool
	}{
		{name: "unset defaults to GOMAXPROCS", value: "", want: runtime.GOMAXPROCS(0)},
		{name: "positive value", value: "3", want: 3},
		{name: "zero", value: "0", wantErr: true},
		{name: "negative", value: "-2", wantErr: true},
		{name: "not a number", value: "many", wantErr: true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, err := resolveMaxConcurrentDecodes(envFrom(map[string]string{"OTHERLODE_COLLECTOR_MAX_CONCURRENT_DECODES": c.value}))
			if (err != nil) != c.wantErr {
				t.Fatalf("error = %v, wantErr %v", err, c.wantErr)
			}
			if got != c.want {
				t.Errorf("got %d, want %d", got, c.want)
			}
		})
	}
}

func TestResolveRedaction_Unset_Off(t *testing.T) {
	cfg, err := resolveRedaction("", "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.Enabled() {
		t.Fatalf("config = %+v, want redaction off", cfg)
	}
}

func TestResolveRedaction_PatternsOnePerLine_BlankLinesIgnored(t *testing.T) {
	cfg, err := resolveRedaction("\nLEGACY_[A-Z]+\n   \r\n(?i)secret,token\r\n\n", "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	var got []string
	for _, re := range cfg.BlockedValues {
		got = append(got, re.String())
	}
	want := []string{"LEGACY_[A-Z]+", "(?i)secret,token"}
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Fatalf("patterns = %q, want %q", got, want)
	}
	if cfg.AllLiterals {
		t.Fatal("AllLiterals = true, want false")
	}
}

func TestResolveRedaction_InvalidPattern_ErrorNamesVariableAndLine(t *testing.T) {
	_, err := resolveRedaction("ok\n\nbad(", "")
	if err == nil {
		t.Fatal("expected an error, got nil")
	}
	for _, want := range []string{"OTHERLODE_COLLECTOR_REDACT_BLOCKED_VALUES", "line 3"} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("error %q does not name %q", err, want)
		}
	}
}

func TestResolveRedaction_AllLiterals(t *testing.T) {
	for raw, want := range map[string]bool{
		"":      false,
		"1":     true,
		"true":  true,
		"TRUE":  true,
		"0":     false,
		"false": false,
	} {
		cfg, err := resolveRedaction("", raw)
		if err != nil {
			t.Errorf("all literals %q: unexpected error: %v", raw, err)
			continue
		}
		if cfg.AllLiterals != want {
			t.Errorf("all literals %q = %v, want %v", raw, cfg.AllLiterals, want)
		}
	}
}

func TestResolveRedaction_AllLiteralsGarbage_Errors(t *testing.T) {
	_, err := resolveRedaction("", "yes please")
	if err == nil {
		t.Fatal("expected an error for an unparsable boolean, got nil")
	}
	if !strings.Contains(err.Error(), "OTHERLODE_COLLECTOR_REDACT_ALL_LITERALS") {
		t.Fatalf("error %q does not name the variable", err)
	}
}

// testRedactSecret is a redaction secret long enough to pass
// processor.CheckSecret.
const testRedactSecret = "redact-secret-0123456789abcdef-0123456789"

func TestReadRedactionSecret(t *testing.T) {
	oneSecret := writeTokenFile(t, "# redaction secret\n  "+testRedactSecret+"  \n\n")
	twoSecrets := writeTokenFile(t, testRedactSecret+"\n"+testRedactSecret+"x\n")
	shortSecret := writeTokenFile(t, "short\n")
	missing := filepath.Join(t.TempDir(), "missing")

	tests := map[string]struct {
		env       map[string]string
		redacting bool
		want      string
		errHas    string
	}{
		"redaction off, nothing set": {},
		"redaction off, secret set": {
			env:    map[string]string{"OTHERLODE_COLLECTOR_REDACT_SECRET": testRedactSecret},
			errHas: "redaction is off",
		},
		"redaction off, secret file set": {
			env:    map[string]string{"OTHERLODE_COLLECTOR_REDACT_SECRET_FILE": oneSecret},
			errHas: "redaction is off",
		},
		"redaction on, nothing set": {
			redacting: true,
			errHas:    "neither OTHERLODE_COLLECTOR_REDACT_SECRET nor OTHERLODE_COLLECTOR_REDACT_SECRET_FILE",
		},
		"secret from variable, spaces trimmed": {
			env:       map[string]string{"OTHERLODE_COLLECTOR_REDACT_SECRET": " " + testRedactSecret + "\n"},
			redacting: true,
			want:      testRedactSecret,
		},
		"secret from file": {
			env:       map[string]string{"OTHERLODE_COLLECTOR_REDACT_SECRET_FILE": oneSecret},
			redacting: true,
			want:      testRedactSecret,
		},
		"both set": {
			env:       map[string]string{"OTHERLODE_COLLECTOR_REDACT_SECRET": testRedactSecret, "OTHERLODE_COLLECTOR_REDACT_SECRET_FILE": oneSecret},
			redacting: true,
			errHas:    "both set",
		},
		"variable with only spaces": {
			env:       map[string]string{"OTHERLODE_COLLECTOR_REDACT_SECRET": "  "},
			redacting: true,
			errHas:    "only spaces",
		},
		"short variable": {
			env:       map[string]string{"OTHERLODE_COLLECTOR_REDACT_SECRET": "hunter2"},
			redacting: true,
			errHas:    "OTHERLODE_COLLECTOR_REDACT_SECRET: the secret is 7 bytes, want at least 32",
		},
		"short file": {
			env:       map[string]string{"OTHERLODE_COLLECTOR_REDACT_SECRET_FILE": shortSecret},
			redacting: true,
			errHas:    "OTHERLODE_COLLECTOR_REDACT_SECRET_FILE: the secret is 5 bytes",
		},
		"file with two secrets": {
			env:       map[string]string{"OTHERLODE_COLLECTOR_REDACT_SECRET_FILE": twoSecrets},
			redacting: true,
			errHas:    "want exactly one",
		},
		"missing file": {
			env:       map[string]string{"OTHERLODE_COLLECTOR_REDACT_SECRET_FILE": missing},
			redacting: true,
			errHas:    "OTHERLODE_COLLECTOR_REDACT_SECRET_FILE",
		},
		"control character": {
			env:       map[string]string{"OTHERLODE_COLLECTOR_REDACT_SECRET": testRedactSecret[:10] + "\x01" + testRedactSecret[10:]},
			redacting: true,
			errHas:    "control character at byte 10",
		},
	}
	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			got, err := readRedactionSecret(envFrom(tt.env), tt.redacting)
			if tt.errHas != "" {
				if err == nil {
					t.Fatalf("got secret %q, want an error containing %q", got, tt.errHas)
				}
				if !strings.Contains(err.Error(), tt.errHas) {
					t.Fatalf("error %q does not contain %q", err, tt.errHas)
				}
				if secret := tt.env["OTHERLODE_COLLECTOR_REDACT_SECRET"]; len(strings.TrimSpace(secret)) > 3 && strings.Contains(err.Error(), strings.TrimSpace(secret)) {
					t.Fatalf("error %q quotes the secret", err)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if string(got) != tt.want {
				t.Fatalf("secret = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestResolveAuthTokens_SecretEqualsToken_StartupRefused(t *testing.T) {
	secret := []byte(testRedactSecret)
	other := strings.Repeat("a", processor.MinSecretBytes)
	for name, tt := range map[string]struct {
		env    map[string]string
		secret []byte
		errHas string
	}{
		"variable list holds the secret": {
			env:    map[string]string{"OTHERLODE_COLLECTOR_AUTH_TOKEN": other + ", " + testRedactSecret},
			secret: secret,
			errHas: "OTHERLODE_COLLECTOR_AUTH_TOKEN: the list holds the redaction secret, which equals an agent auth token",
		},
		"file holds the secret": {
			env:    map[string]string{"OTHERLODE_COLLECTOR_AUTH_TOKEN_FILE": writeTokenFile(t, other+"\n"+testRedactSecret+"\n")},
			secret: secret,
			errHas: "OTHERLODE_COLLECTOR_AUTH_TOKEN_FILE: ",
		},
		"variable without the secret": {
			env:    map[string]string{"OTHERLODE_COLLECTOR_AUTH_TOKEN": other},
			secret: secret,
		},
		"redaction off": {
			env: map[string]string{"OTHERLODE_COLLECTOR_AUTH_TOKEN": testRedactSecret},
		},
	} {
		t.Run(name, func(t *testing.T) {
			_, _, err := resolveAuthTokens(envFrom(tt.env), tt.secret)
			if tt.errHas == "" {
				if err != nil {
					t.Fatalf("unexpected error: %v", err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), tt.errHas) || !strings.Contains(err.Error(), "equals an agent auth token") {
				t.Fatalf("error = %v, want one containing %q and the agent auth token wording", err, tt.errHas)
			}
			if strings.Contains(err.Error(), testRedactSecret) {
				t.Fatalf("error %q quotes the secret", err)
			}
		})
	}
}

func TestResolveForwardConfig_SecretEqualsKey_StartupRefused(t *testing.T) {
	secret := []byte(testRedactSecret)
	for name, tt := range map[string]struct {
		env    map[string]string
		secret []byte
		errHas string
	}{
		"variable holds the secret": {
			env:    map[string]string{"OTHERLODE_COLLECTOR_FORWARD_AUTH_TOKEN": " " + testRedactSecret + " "},
			secret: secret,
			errHas: "OTHERLODE_COLLECTOR_FORWARD_AUTH_TOKEN: ",
		},
		"file holds the secret": {
			env:    map[string]string{"OTHERLODE_COLLECTOR_FORWARD_AUTH_TOKEN_FILE": writeTokenFile(t, testRedactSecret+"\n")},
			secret: secret,
			errHas: "OTHERLODE_COLLECTOR_FORWARD_AUTH_TOKEN_FILE: ",
		},
		"variable without the secret": {
			env:    map[string]string{"OTHERLODE_COLLECTOR_FORWARD_AUTH_TOKEN": "backend-key"},
			secret: secret,
		},
		"redaction off": {
			env: map[string]string{"OTHERLODE_COLLECTOR_FORWARD_AUTH_TOKEN": testRedactSecret},
		},
	} {
		t.Run(name, func(t *testing.T) {
			env := map[string]string{"OTHERLODE_COLLECTOR_FORWARD_URL": "http://backend.example.com"}
			for k, v := range tt.env {
				env[k] = v
			}
			_, _, err := resolveForwardConfig(envFrom(env), tt.secret)
			if tt.errHas == "" {
				if err != nil {
					t.Fatalf("unexpected error: %v", err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), tt.errHas) || !strings.Contains(err.Error(), "equals the forward key") {
				t.Fatalf("error = %v, want one containing %q and the forward key wording", err, tt.errHas)
			}
			if strings.Contains(err.Error(), testRedactSecret) {
				t.Fatalf("error %q quotes the secret", err)
			}
		})
	}
}

func TestAuthTokenFileReread_HoldsRedactionSecret_KeepsLastGoodTokens(t *testing.T) {
	path := writeTokenFile(t, "old-token\n")
	tokens, file, err := resolveAuthTokens(envFrom(map[string]string{"OTHERLODE_COLLECTOR_AUTH_TOKEN_FILE": path}), []byte(testRedactSecret))
	if err != nil {
		t.Fatalf("resolveAuthTokens: %v", err)
	}
	watchFile(t, file)

	failuresBefore := metrics.TokenReloadFailures.Value("auth")
	replaceTokenFile(t, path, "new-token\n"+testRedactSecret+"\n")
	waitUntil(t, 2*time.Second, func() bool { return metrics.TokenReloadFailures.Value("auth") > failuresBefore })
	if tokens.Matches(testRedactSecret) || tokens.Matches("new-token") || !tokens.Matches("old-token") {
		t.Fatal("the reload that held the secret changed the accepted tokens")
	}

	replaceTokenFile(t, path, "new-token\n")
	waitUntil(t, 2*time.Second, func() bool { return tokens.Matches("new-token") })
	if tokens.Matches("old-token") {
		t.Fatal("a reload without the secret did not replace the tokens")
	}
}

func TestAuthTokenFileReread_RedactionOff_AppliesAnySet(t *testing.T) {
	path := writeTokenFile(t, "old-token\n")
	tokens, file, err := resolveAuthTokens(envFrom(map[string]string{"OTHERLODE_COLLECTOR_AUTH_TOKEN_FILE": path}), nil)
	if err != nil {
		t.Fatalf("resolveAuthTokens: %v", err)
	}
	watchFile(t, file)

	replaceTokenFile(t, path, testRedactSecret+"\n")
	waitUntil(t, 2*time.Second, func() bool { return tokens.Matches(testRedactSecret) })
}

func TestForwardKeyFileReread_HoldsRedactionSecret_KeepsLastGoodKey(t *testing.T) {
	path := writeTokenFile(t, "key-1\n")
	cfg, file, err := resolveForwardConfig(envFrom(map[string]string{
		"OTHERLODE_COLLECTOR_FORWARD_URL":             "http://backend.example.com",
		"OTHERLODE_COLLECTOR_FORWARD_AUTH_TOKEN_FILE": path,
	}), []byte(testRedactSecret))
	if err != nil {
		t.Fatalf("resolveForwardConfig: %v", err)
	}
	watchFile(t, file)

	failuresBefore := metrics.TokenReloadFailures.Value("forward")
	replaceTokenFile(t, path, testRedactSecret+"\n")
	waitUntil(t, 2*time.Second, func() bool { return metrics.TokenReloadFailures.Value("forward") > failuresBefore })
	if got := cfg.AuthToken.Token(); got != "key-1" {
		t.Fatalf("key after the reload that held the secret = %q, want key-1", got)
	}

	replaceTokenFile(t, path, "key-2\n")
	waitUntil(t, 2*time.Second, func() bool { return cfg.AuthToken.Token() == "key-2" })
}

func TestForwardKeyFileReread_RedactionOff_AppliesAnyKey(t *testing.T) {
	path := writeTokenFile(t, "key-1\n")
	cfg, file, err := resolveForwardConfig(envFrom(map[string]string{
		"OTHERLODE_COLLECTOR_FORWARD_URL":             "http://backend.example.com",
		"OTHERLODE_COLLECTOR_FORWARD_AUTH_TOKEN_FILE": path,
	}), nil)
	if err != nil {
		t.Fatalf("resolveForwardConfig: %v", err)
	}
	watchFile(t, file)

	replaceTokenFile(t, path, testRedactSecret+"\n")
	waitUntil(t, 2*time.Second, func() bool { return cfg.AuthToken.Token() == testRedactSecret })
}

func TestResolveForwardConfig_BadURL_ErrorNamesTheVariable(t *testing.T) {
	const prefix = "OTHERLODE_COLLECTOR_FORWARD_URL: "
	tests := []struct {
		name string
		url  string
		want string
	}{
		{"bad scheme", "ftp://x", prefix + `forward URL "ftp://x": scheme must be http or https`},
		{"missing host", "https://", prefix + `forward URL "https://": missing host`},
		{"user info", "https://user:s3cret@backend.example.com", prefix + "forward URL must not contain user info"},
		{"query", "https://backend.example.com/?a=b", prefix + "forward URL must not contain a query"},
		{"fragment", "https://backend.example.com/#f", prefix + "forward URL must not contain a fragment"},
		{"unparseable", "http://[::1", prefix + `forward URL "http://[::1": missing ']' in host`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, _, err := resolveForwardConfig(envFrom(map[string]string{
				"OTHERLODE_COLLECTOR_FORWARD_URL": tt.url,
			}), nil)
			if err == nil {
				t.Fatal("expected an error, got nil")
			}
			if err.Error() != tt.want {
				t.Fatalf("error = %q, want %q", err.Error(), tt.want)
			}
			if tt.name == "user info" && (strings.Contains(err.Error(), "s3cret") || strings.Contains(err.Error(), tt.url)) {
				t.Fatalf("error %q echoes the URL", err.Error())
			}
		})
	}
}

func TestResolveForwardConfig_EmptyURL_NoError(t *testing.T) {
	if _, _, err := resolveForwardConfig(envFrom(map[string]string{
		"OTHERLODE_COLLECTOR_FORWARD_URL": "",
	}), nil); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestResolveForwardConfig_GoodURLWithPath_KeepsURL(t *testing.T) {
	cfg, _, err := resolveForwardConfig(envFrom(map[string]string{
		"OTHERLODE_COLLECTOR_FORWARD_URL": "https://backend.example.com/v1/",
	}), nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.URL != "https://backend.example.com/v1/" {
		t.Fatalf("URL = %q, want the value as set", cfg.URL)
	}
}

func TestListen_MissingPort_ErrorNamesTheVariable(t *testing.T) {
	ln, err := listen("nonsense", slog.New(slog.NewJSONHandler(&bytes.Buffer{}, nil)))
	if err == nil {
		ln.Close()
		t.Fatal("expected an error for a missing port, got nil")
	}
	want := "OTHERLODE_COLLECTOR_ADDR: listen tcp: address nonsense: missing port in address"
	if err.Error() != want {
		t.Fatalf("error = %q, want %q", err.Error(), want)
	}
}

func TestListen_PortInUse_ErrorNamesTheVariable(t *testing.T) {
	taken, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	defer taken.Close()

	ln, err := listen(taken.Addr().String(), slog.New(slog.NewJSONHandler(&bytes.Buffer{}, nil)))
	if err == nil {
		ln.Close()
		t.Fatal("expected an error for a port in use, got nil")
	}
	want := "OTHERLODE_COLLECTOR_ADDR: listen tcp " + taken.Addr().String() + ": bind: address already in use"
	if err.Error() != want {
		t.Fatalf("error = %q, want %q", err.Error(), want)
	}
}
