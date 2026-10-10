---
title: Health checks, metrics and logs
description: The /healthz endpoint and the healthcheck subcommand, every counter on /metrics with its labels and when it rises, which counters to alert on, and the log format and levels.
order: 80
---

The collector gives you three ways to see what it is doing. `/healthz` says whether it is up, `/metrics` counts what it did with each payload, and the log says what went wrong. `/healthz` and `/metrics` need no token and are never rate limited. Both are outside the agent token check and the per-client limit, so a probe or a scraper inside your network works with the defaults.

## Check health

`GET /healthz` answers `200` with an empty body once the collector is listening. It checks nothing else. It does not test the forward URL or the backend, so a collector that cannot reach the backend still reports healthy. Watch the [forwarding counters](#forwarding-counters) for that.

The image has no shell and no `curl`, so it carries a `healthcheck` subcommand:

```bash
docker exec <container> /otherlode-collector healthcheck
```

The subcommand makes one `GET` of `/healthz` on the address the collector listens on, which is `OTHERLODE_COLLECTOR_ADDR` (default `:4319`). It rewrites the host so the probe stays inside the container:

| `OTHERLODE_COLLECTOR_ADDR` | Address probed |
|---|---|
| Unset, or `:4319` | `127.0.0.1:4319` |
| `0.0.0.0:4319` or `[::]:4319` | `127.0.0.1:4319` |
| `10.0.0.5:4319` | `10.0.0.5:4319` |
| `:http-alt` | `127.0.0.1` on the port that name resolves to, as the listener resolves it |

The probe gives up after 2 seconds. It never uses a proxy, so `HTTPS_PROXY`, `HTTP_PROXY` and `NO_PROXY` do not affect it. It prints the error to standard error when it fails.

| Exit code | Meaning |
|---|---|
| `0` | `/healthz` answered `200` |
| `1` | The address is invalid, the request failed or timed out, or the status was not `200` |
| `2` | You passed any argument other than `healthcheck`. The collector prints a usage line and does not start |

The image runs the subcommand as its `HEALTHCHECK`:

| Setting | Value |
|---|---|
| Interval | 10 seconds |
| Timeout | 3 seconds |
| Start period | 10 seconds |
| Retries | 3 |

Docker marks the container unhealthy after three failed probes in a row. If the probe fails, see [troubleshooting](troubleshooting).

## Read the metrics

`GET /metrics` serves the counters in the Prometheus text format, with the content type `text/plain; version=0.0.4; charset=utf-8`. The output holds only counts. It carries no service names, instance IDs, literals or payload content.

Every metric is a counter. The counters live in memory and start again at 0 when the collector restarts, so use `increase()` or `rate()` rather than the raw value. A counter without labels shows at 0 from startup. A counter with labels adds a series the first time that label combination happens, so a series you have never seen is a series that has not happened. The one exception is the token reload counter, which shows each token file you configured at 0.

The `payload` label takes one of three values, named for the path the agent posted to:

| `payload` | Agent path |
|---|---|
| `deltas` | `/v1/otherlode/deltas` |
| `manifest` | `/v1/otherlode/manifest` |
| `static_baseline` | `/v1/otherlode/static-baseline` |

Each counter counts payloads, and one request carries one payload.

### Ingest counters

| Counter | Labels | Goes up when |
|---|---|---|
| `otherlode_collector_ingest_accepted_total` | `payload` | The collector answered `202`. The payload decoded, passed validation and was handed on |
| `otherlode_collector_ingest_rejected_total` | `payload`, `reason` | The collector turned a request away after the token and rate limit checks. See the reasons below |
| `otherlode_collector_auth_rejected_total` | none | A request to an ingest path got `401` for a missing or wrong bearer token |
| `otherlode_collector_rate_limited_total` | none | A request to an ingest path got `429` because its client IP was over the limit |

The `reason` label on `ingest_rejected_total` takes these values:

| `reason` | Answer | Cause |
|---|---|---|
| `content_type` | `415` | The `Content-Type` is not `application/x-protobuf`, or a `Content-Encoding` other than `identity` is set |
| `too_large` | `413` | The body is over 16 MiB |
| `read` | `400` | The body could not be read |
| `malformed` | `400` | The body is not valid protobuf |
| `invalid` | `400` | The body decoded but failed validation |
| `sink` | `503` | The collector could not take the payload. With forwarding on, this is a full queue or a shutdown in progress. The response carries `Retry-After: 5` |
| `busy` | `503` | No decode slot came free within 5 seconds. The response carries `Retry-After: 1` |
| `canceled` | none | The client disconnected while it waited for a decode slot |

A `401` or `429` never reaches the ingest counters. They count in `auth_rejected_total` and `rate_limited_total` only. For the limits behind `too_large`, `busy` and `rate_limited_total`, see [rate and memory limits](limits).

### Forwarding counters

These stay at 0 when `OTHERLODE_COLLECTOR_FORWARD_URL` is not set.

| Counter | Labels | Goes up when |
|---|---|---|
| `otherlode_collector_forward_delivered_total` | `payload` | The backend answered a payload with a `2xx` status |
| `otherlode_collector_forward_retries_total` | `payload` | The forwarder waited after a retryable failure and started another attempt |
| `otherlode_collector_forward_dropped_total` | `payload`, `reason` | The collector gave up on a payload it had already accepted. See the reasons below |
| `otherlode_collector_forward_refused_total` | `payload`, `reason` | The forwarder did not take a payload and the agent got `503`. See the reasons below |

The `reason` label on `forward_dropped_total` takes these values:

| `reason` | Cause |
|---|---|
| `marshal` | The collector could not re-encode the payload to forward it |
| `permanent` | The backend answered a status the collector does not retry, which is anything except `2xx`, `429`, `502`, `503` and `504`. A failure to build the request also counts |
| `retry_exhausted` | The retries ran out of time |
| `shutdown_deadline` | The collector stopped before it could try the payload again |
| `shutdown_attempt_failed` | The one attempt made during shutdown failed |

The `reason` label on `forward_refused_total` is `queue_full` or `shutting_down`. A refused payload is still in the agent's hands, because the agent got a `503`. Each refusal also adds one to `ingest_rejected_total` with `reason="sink"`. The queue, the retry budget and the shutdown drain are in [forwarding](forwarding).

### Token counter

| Counter | Labels | Goes up when |
|---|---|---|
| `otherlode_collector_token_reload_failures_total` | `file` | The collector re-read a token file every 30 seconds and the read failed or found no usable token. It keeps the last good value |

`file` is `auth` for `OTHERLODE_COLLECTOR_AUTH_TOKEN_FILE` and `forward` for `OTHERLODE_COLLECTOR_FORWARD_AUTH_TOKEN_FILE`. The `forward` series exists only when forwarding is on. See [set and rotate tokens](tokens).

### Stamping and redaction counters

| Counter | Labels | Goes up when |
|---|---|---|
| `otherlode_collector_environment_mismatch_total` | `payload` | The agent set an environment that differs from `OTHERLODE_COLLECTOR_ENVIRONMENT` |
| `otherlode_collector_namespace_mismatch_total` | `payload` | The agent set a service namespace that differs from `OTHERLODE_COLLECTOR_SERVICE_NAMESPACE` |
| `otherlode_collector_redacted_literals_total` | `payload` | Redaction replaced string literal parts. It adds the number replaced, so one payload can add many. `payload` is `manifest` or `static_baseline` |
| `otherlode_collector_redacted_case_keys_total` | `payload` | Redaction cleared case keys that are a string's hash code. It adds the number cleared. `payload` is `manifest` or `static_baseline` |
| `otherlode_collector_fields_stripped_total` | `payload` | Redaction dropped a field it does not know and marked the payload `fields_stripped` |

The mismatch counters go up whether the action is `insert` or `upsert`. An agent value that is empty or only spaces is not a mismatch. See [set the environment and namespace](environment-and-namespace) and [blank out string literals](redaction).

## Choose what to alert on

- **`forward_dropped_total`.** Alert on any increase. Every increment is a payload the agent was told arrived, and that never reached the backend. The `reason` tells you why.
- **`forward_refused_total`.** A steady rise means the backend is slower than your agents, or the queue is too small. Nothing is lost while the agents keep retrying.
- **`token_reload_failures_total`.** Alert on any increase. A token file is broken and the collector is still on the tokens it last read.
- **`fields_stripped_total`.** Alert on any increase. Your collector is older than your agents. Run the latest collector, as [troubleshooting](troubleshooting) describes.

A rise in `auth_rejected_total` or `rate_limited_total` means a client is sending the wrong token or too many requests. Whether that matters depends on your fleet, so the collector gives no fixed threshold.

## Read the logs

The collector writes one JSON object per line to standard output. Each line has `time`, `level` and `msg`, plus the fields that belong to the event. It never logs a string literal from your code or a token. Its `info` lines for received payloads name the service and instance and give counts.

```text
{"time":"2026-10-10T09:12:44.381Z","level":"INFO","msg":"otherlode-collector listening","addr":":4319","version":"0.1.0"}
```

Set the level with `OTHERLODE_COLLECTOR_LOG_LEVEL`.

| Value | Effect |
|---|---|
| Unset | `info` |
| `debug` | Logs every line, including `DEBUG` |
| `info` | Logs `INFO`, `WARN` and `ERROR` |
| `warn` | Logs `WARN` and `ERROR` |
| `error` | Logs `ERROR` only |

The names are not case sensitive. A name can take a signed offset, such as `warn+1` or `info-2`, to set a threshold between two named levels, which sit 4 apart. An empty value counts as unset. Any other value stops startup with a line like this:

```text
{"time":"2026-10-10T09:12:44.381Z","level":"ERROR","msg":"OTHERLODE_COLLECTOR_LOG_LEVEL: slog: level string \"trace\": unknown name"}
```

Without `OTHERLODE_COLLECTOR_FORWARD_URL`, the collector logs every payload it receives at `info`. On a busy collector, set `warn` to quiet it.

### Lines you may see at the default level

| Level | `msg` | When |
|---|---|---|
| `WARN` | `OTHERLODE_COLLECTOR_FORWARD_URL not set; ingest payloads are only logged, not forwarded` | At startup, with no forward URL |
| `WARN` | `OTHERLODE_COLLECTOR_AUTH_TOKEN and OTHERLODE_COLLECTOR_AUTH_TOKEN_FILE not set; ingest endpoints are unauthenticated` | At startup, with no agent token |
| `WARN` | `rate limiting disabled; ingest endpoints accept requests unthrottled` | At startup, when the rate limit is `0` |
| `WARN` | `forward URL uses plain http; the backend auth token is sent unencrypted` | At startup, with an `http://` forward URL and a forward key |
| `WARN` | `rejecting invalid delta batch`, `rejecting invalid manifest` or `rejecting invalid static baseline` | A payload failed validation, with the reason in `error` |
| `WARN` | `rejecting malformed delta batch`, `rejecting malformed manifest` or `rejecting malformed static baseline` | A body was not valid protobuf |
| `WARN` | `token file re-read failed; keeping the last good value` | A token file re-read failed, with `file` and `error` |
| `WARN` | `dropping payload: ` followed by a reason | The forwarder dropped a payload. The line names the namespace, service, instance and path, and adds `backend_error` when the backend's response names an error code |
| `WARN` | `dropped fields this collector does not know; ...` | Redaction dropped unknown fields. It logs once per run |
| `WARN` | `refusing delta batch: no decode slot came free`, or the same with `manifest` or `static baseline` | A request waited 5 seconds for a decode slot and got `503`. At most one line a minute. `unlogged_busy_refusals` counts the refusals since the previous line that got no line of their own |
| `WARN` | `agent environment does not match the collector's configured environment` | The first payload of a service that names a given environment other than the collector's. Later payloads of that service with that environment log at `DEBUG` |
| `WARN` | `agent namespace does not match the collector's configured namespace` | The same, for the service namespace |
| `ERROR` | `sink rejected delta batch`, `sink rejected manifest` or `sink rejected static baseline` | The collector answered `503`, with the reason in `error` |
| `ERROR` | `listen tcp ...` | The collector could not open its listen address at startup, and the process exits |
| `ERROR` | `server stopped` or `graceful shutdown failed` | The server stopped with an error after it started, or shutdown did not finish, and the process exits |

The `dropping payload` reasons are `marshal failed`, `permanent failure`, `retry budget exhausted`, `shutdown deadline exceeded` and `shutdown drain attempt failed`. They match the `reason` values of `forward_dropped_total`.

At `info` the collector also logs startup, shutdown, the stamping and redaction settings it applied, and the token file events `token file changed` and `token file re-read recovered`. The token file line gives a count of tokens, never a token. The redaction line gives the secret's fingerprint as `secret_fingerprint`, never the secret.

Startup errors name the setting and the invalid value. [Troubleshooting](troubleshooting) gives the cause and the fix for each line above.

## Related pages

- [Configuration](configuration) lists `OTHERLODE_COLLECTOR_ADDR` and `OTHERLODE_COLLECTOR_LOG_LEVEL`.
- [Forwarding](forwarding) explains the queue, retries and the shutdown drain behind the forwarding counters.
- [Troubleshooting](troubleshooting) covers the failing health check and each log line.
