package ingest

import (
	"errors"
	"net/http"
)

// StatusError is an error a Sink returns to make the handler answer with
// the sink's own client-error status, such as the status a backend gave.
// Status must be from 400 to 499. The handler answers any other error, and
// a StatusError outside that range, with 503 and "Retry-After: 5".
//
// RetryAfter, when not empty, goes in the Retry-After header as written.
// Code is a short reason for the log. Err is the cause.
type StatusError struct {
	Status     int
	RetryAfter string
	Code       string
	Err        error
}

func (e *StatusError) Error() string {
	text := http.StatusText(e.Status)
	if text == "" {
		text = "status"
	}
	if e.Err == nil {
		return "sink answered " + text
	}
	return "sink answered " + text + ": " + e.Err.Error()
}

// Unwrap returns the cause.
func (e *StatusError) Unwrap() error { return e.Err }

func (e *StatusError) relayed() bool {
	return e.Status >= 400 && e.Status <= 499
}

type reasonError struct {
	reason string
	err    error
}

func (e *reasonError) Error() string { return e.err.Error() }

func (e *reasonError) Unwrap() error { return e.err }

// WithReason returns err marked with reason, a short word such as "timeout"
// or "server_error". The handler rate-limits and labels its sink-failure log
// lines by reason. An empty reason becomes "error". It returns nil for a nil
// err.
func WithReason(err error, reason string) error {
	if err == nil {
		return nil
	}
	if reason == "" {
		reason = "error"
	}
	return &reasonError{reason: reason, err: err}
}

func reasonOf(err error) string {
	var r *reasonError
	if errors.As(err, &r) {
		return r.reason
	}
	return "error"
}
