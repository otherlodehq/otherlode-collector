package metrics

// The collector's counters. Label values for "payload" are "deltas",
// "manifest", or "static_baseline"; "reason" values are listed on each
// counter.
var (
	// IngestAccepted counts payloads that decoded, passed validation, and
	// reached the sink.
	IngestAccepted = NewCounter("otherlode_collector_ingest_accepted_total",
		"Payloads decoded, validated, and handed to the sink.", "payload")

	// IngestRejected counts requests turned away before or by the sink.
	// Reasons: content_type, too_large, read, malformed, invalid, sink,
	// canceled (the request context ended while it waited for a decode
	// slot), busy (no decode slot came free in time; the answer was 503).
	IngestRejected = NewCounter("otherlode_collector_ingest_rejected_total",
		"Requests rejected before or by the sink.", "payload", "reason")

	// AuthRejected counts requests refused with 401.
	AuthRejected = NewCounter("otherlode_collector_auth_rejected_total",
		"Requests refused for a missing or wrong bearer token.")

	// TokenReloadFailures counts token file re-reads that failed, so the
	// collector kept the last good value. Its "file" is "auth" for the
	// agent-facing token file or "forward" for the backend key file.
	TokenReloadFailures = NewCounter("otherlode_collector_token_reload_failures_total",
		"Token file re-reads that failed or found no usable token, so the last good value stayed.", "file")

	// RateLimited counts requests refused with 429.
	RateLimited = NewCounter("otherlode_collector_rate_limited_total",
		"Requests refused because the client's rate limit was exceeded.")

	// ForwardDelivered counts payloads the backend accepted.
	ForwardDelivered = NewCounter("otherlode_collector_forward_delivered_total",
		"Payloads the backend accepted.", "payload")

	// ForwardRetries counts delivery attempts made after a retryable failure.
	ForwardRetries = NewCounter("otherlode_collector_forward_retries_total",
		"Delivery attempts made after a retryable failure.", "payload")

	// ForwardDropped counts payloads discarded without delivery. Reasons:
	// marshal, permanent, retry_exhausted, shutdown_deadline,
	// shutdown_attempt_failed.
	ForwardDropped = NewCounter("otherlode_collector_forward_dropped_total",
		"Payloads discarded without a successful delivery.", "payload", "reason")

	// ForwardRefused counts payloads the forwarder did not take, answered
	// with 503 so the sender sends them again. Reasons: queue_full,
	// shutting_down.
	ForwardRefused = NewCounter("otherlode_collector_forward_refused_total",
		"Payloads the forwarder did not take, answered 503 so the sender sends them again.", "payload", "reason")

	// EnvironmentMismatch counts payloads whose agent-set environment
	// differs from the collector's configured one. Its "payload" is
	// "deltas", "manifest" or "static_baseline".
	EnvironmentMismatch = NewCounter("otherlode_collector_environment_mismatch_total",
		"Payloads whose agent-set environment differs from the collector's configured one.", "payload")

	// NamespaceMismatch counts payloads whose agent-set service namespace
	// differs from the collector's configured one. Its "payload" is
	// "deltas", "manifest" or "static_baseline".
	NamespaceMismatch = NewCounter("otherlode_collector_namespace_mismatch_total",
		"Payloads whose agent-set service namespace differs from the collector's configured one.", "payload")

	// RedactedLiterals counts string literal parts the redaction processor
	// replaced. Its "payload" is "manifest" or "static_baseline".
	RedactedLiterals = NewCounter("otherlode_collector_redacted_literals_total",
		"String literal parts replaced by the redaction processor.", "payload")

	// RedactedCaseKeys counts case keys the redaction processor cleared:
	// every case key of a switch the agent marks string_hash_code_switch,
	// and each one that equals the Java hash code of a literal it redacted.
	// Its "payload" is "manifest" or "static_baseline".
	RedactedCaseKeys = NewCounter("otherlode_collector_redacted_case_keys_total",
		"Case keys cleared by the redaction processor, from a switch on a string's hash code or equal to the hash code of a redacted literal.", "payload")

	// FieldsStripped counts payloads from which the redaction processor
	// dropped at least one unknown field and so set fields_stripped. Its
	// "payload" is "deltas", "manifest" or "static_baseline".
	FieldsStripped = NewCounter("otherlode_collector_fields_stripped_total",
		"Payloads that lost an unknown field to the redaction processor and were marked fields_stripped.", "payload")
)
