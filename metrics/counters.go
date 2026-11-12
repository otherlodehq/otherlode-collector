package metrics

// These are the collector's counters. A "payload" label is "deltas",
// "manifest", "static_baseline" or "class_listing", unless the counter says
// otherwise.
var (
	// IngestAccepted counts payloads that decoded, passed validation and
	// that the sink accepted.
	IngestAccepted = NewCounter("otherlode_collector_ingest_accepted_total",
		"Payloads decoded, validated, and handed to the sink.", "payload")

	// IngestRejected counts requests turned away before or by the sink. Its
	// reasons are content_type, too_large, read, malformed, invalid, sink,
	// canceled, busy, timeout and too_many_elements. A canceled request's
	// context ended while it waited for a decode slot. A busy request found
	// no free decode slot in time. A timeout request's budget ended before
	// the wait for a decode slot began. These three got 503. A
	// too_many_elements request held more elements than its payload's limit
	// and got 413.
	IngestRejected = NewCounter("otherlode_collector_ingest_rejected_total",
		"Requests rejected before or by the sink.", "payload", "reason")

	// AuthRejected counts requests refused with 401.
	AuthRejected = NewCounter("otherlode_collector_auth_rejected_total",
		"Requests refused for a missing or wrong bearer token.")

	// TokenReloadFailures counts token file re-reads that failed, so the
	// collector kept the last good value. The "file" label is "auth" or
	// "forward".
	TokenReloadFailures = NewCounter("otherlode_collector_token_reload_failures_total",
		"Token file re-reads that failed or found no usable token, so the last good value stayed.", "file")

	// RateLimited counts requests refused with 429.
	RateLimited = NewCounter("otherlode_collector_rate_limited_total",
		"Requests refused because the client's rate limit was exceeded.")

	// ForwardDelivered counts payloads the backend answered with 202 and
	// the collector answered 202.
	ForwardDelivered = NewCounter("otherlode_collector_forward_delivered_total",
		"Payloads the backend stored, answered 202.", "payload")

	// ForwardFailed counts payloads the backend did not store and the
	// collector did not answer 202. Its reasons are rate_limited (the backend
	// answered 429), rejected (another 4xx), server_error (any other non-2xx
	// status), timeout (no answer within the forward request timeout),
	// transport (the connection failed), canceled (the agent's request or the
	// shutdown deadline ended first), busy (no forward slot came free in
	// time), unexpected_status (a 2xx other than 202), shutting_down, marshal
	// and request.
	ForwardFailed = NewCounter("otherlode_collector_forward_failed_total",
		"Payloads the backend did not store, so the collector did not answer 202.", "payload", "reason")

	// ForwardNotices counts the notices the backend sent with an accepted
	// payload. Its "notice" label is "service-over-cap",
	// "environment-not-tracked" or "unknown", which stands for any other value.
	ForwardNotices = NewCounter("otherlode_collector_forward_notices_total",
		"Notices the backend sent with an accepted payload.", "notice")

	// EnvironmentMismatch counts payloads whose agent-set environment
	// differs from the collector's configured one.
	EnvironmentMismatch = NewCounter("otherlode_collector_environment_mismatch_total",
		"Payloads whose agent-set environment differs from the collector's configured one.", "payload")

	// NamespaceMismatch counts payloads whose agent-set service namespace
	// differs from the collector's configured one.
	NamespaceMismatch = NewCounter("otherlode_collector_namespace_mismatch_total",
		"Payloads whose agent-set service namespace differs from the collector's configured one.", "payload")

	// RedactedLiterals counts string literal parts the redaction processor
	// replaced. The "payload" label is "manifest" or "static_baseline".
	RedactedLiterals = NewCounter("otherlode_collector_redacted_literals_total",
		"String literal parts replaced by the redaction processor.", "payload")

	// RedactedCaseKeys counts case keys the redaction processor cleared:
	// every case key of a switch the agent marks string_hash_code_switch,
	// and each one that equals the Java hash code of a literal it redacted.
	// The "payload" label is "manifest" or "static_baseline".
	RedactedCaseKeys = NewCounter("otherlode_collector_redacted_case_keys_total",
		"Case keys cleared by the redaction processor, from a switch on a string's hash code or equal to the hash code of a redacted literal.", "payload")

	// FieldsStripped counts payloads from which the redaction processor
	// dropped at least one unknown field and so set fields_stripped.
	FieldsStripped = NewCounter("otherlode_collector_fields_stripped_total",
		"Payloads that lost an unknown field to the redaction processor and were marked fields_stripped.", "payload")
)
