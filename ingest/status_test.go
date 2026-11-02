package ingest

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	otherlodepb "buf.build/gen/go/otherlode/otherlode/protocolbuffers/go/otherlode/v1"

	"github.com/otherlodehq/otherlode-collector/metrics"
)

// errSink fails every payload with err.
type errSink struct{ err error }

func (s errSink) AcceptDeltaBatch(context.Context, *otherlodepb.DeltaBatch) ([]Notice, error) {
	return nil, s.err
}

func (s errSink) AcceptManifest(context.Context, *otherlodepb.ProbeManifest) ([]Notice, error) {
	return nil, s.err
}

func (s errSink) AcceptStaticBaseline(context.Context, *otherlodepb.StaticBaseline) ([]Notice, error) {
	return nil, s.err
}

func (s errSink) AcceptClassListing(context.Context, *otherlodepb.ClassListing) ([]Notice, error) {
	return nil, s.err
}

func TestHandler_SinkStatusError_AnswersWithItsStatus(t *testing.T) {
	tests := []struct {
		name           string
		err            error
		wantStatus     int
		wantRetryAfter string
	}{
		{"429 with Retry-After", &StatusError{Status: 429, RetryAfter: "42"}, 429, "42"},
		{"429 without Retry-After", &StatusError{Status: 429}, 429, ""},
		{"400", &StatusError{Status: 400, Code: "bad"}, 400, ""},
		{"401", &StatusError{Status: 401}, 401, ""},
		{"404 with a Retry-After", &StatusError{Status: 404, RetryAfter: "9"}, 404, "9"},
		{"499", &StatusError{Status: 499}, 499, ""},
		{"wrapped", fmt.Errorf("sink: %w", &StatusError{Status: 409}), 409, ""},
		{"5xx status is a 503", &StatusError{Status: 500, RetryAfter: "9"}, 503, "5"},
		{"3xx status is a 503", &StatusError{Status: 302}, 503, "5"},
		{"zero status is a 503", &StatusError{}, 503, "5"},
		{"plain error is a 503", errors.New("down"), 503, "5"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mux := http.NewServeMux()
			NewHandler(errSink{err: tt.err}, nil).Register(mux)
			rejectedBefore := metrics.IngestRejected.Value("manifest", "sink")
			acceptedBefore := metrics.IngestAccepted.Value("manifest")

			rec := httptest.NewRecorder()
			serveWithin(t, mux, rec, manifestRequest(context.Background(), t))

			if rec.Code != tt.wantStatus {
				t.Errorf("status = %d, want %d", rec.Code, tt.wantStatus)
			}
			if got := rec.Header().Get("Retry-After"); got != tt.wantRetryAfter {
				t.Errorf("Retry-After = %q, want %q", got, tt.wantRetryAfter)
			}
			if rec.Body.Len() != 0 {
				t.Errorf("body = %q, want empty", rec.Body.String())
			}
			if got := metrics.IngestRejected.Value("manifest", "sink"); got != rejectedBefore+1 {
				t.Errorf("IngestRejected(manifest, sink) = %d, want %d", got, rejectedBefore+1)
			}
			if got := metrics.IngestAccepted.Value("manifest"); got != acceptedBefore {
				t.Errorf("IngestAccepted(manifest) = %d, want unchanged at %d", got, acceptedBefore)
			}
		})
	}
}

func TestStatusError_UnwrapsToItsCause(t *testing.T) {
	cause := errors.New("backend returned 429")
	err := fmt.Errorf("sink: %w", &StatusError{Status: 429, Err: cause})
	if !errors.Is(err, cause) {
		t.Fatalf("errors.Is(%v, cause) = false", err)
	}
}
