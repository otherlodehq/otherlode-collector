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
| `2` | You passed any argument list other than `healthcheck` alone. The collector prints a usage line and does not start |

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

The `payload` label takes one of four values, named for the path the agent posted to:

| `payload` | Agent path |
|---|---|
| `deltas` | `/v1/otherlode/deltas` |
| `manifest` | `/v1/otherlode/manifest` |
| `static_baseline` | `/v1/otherlode/static-baseline` |
| `class_listing` | `/v1/otherlode/class-listing` |

One request carries one payload.

### Ingest counters

| Counter | Labels | Goes up when |
|---|---|---|
| `otherlode_collector_ingest_accepted_total` | `payload` | The collector answered `202`. The payload decoded, passed validation and the sink took it. With forwarding on, Otherlode had stored it |
| `otherlode_collector_ingest_rejected_total` | `payload`, `reason` | The collector turned a request away after the token and rate limit checks. See the reasons below |
| `otherlode_collector_auth_rejected_total` | none | A request to an ingest path got `401` for a missing or wrong bearer token |
| `otherlode_collector_rate_limited_total` | none | A request to an ingest path got `429` because its client IP was over the limit |

The `reason` label on `ingest_rejected_total` takes these values:

| `reason` | Answer | Cause |
|---|---|---|
| `content_type` | `415` | The `Content-Type` is not `application/x-protobuf`, or a `Content-Encoding` other than `identity` is set |
| `too_large` | `413` | The body is over 16 MiB |
| `read` | `400` | The body could not be read |
| `too_many_elements` | `413` | The payload holds more elements than the limit for its kind |
| `malformed` | `400` | The body is not valid protobuf |
| `invalid` | `400` | The body decoded but failed validation |
| `sink` | `503`, `429` or another `4xx` | Otherlode did not store the payload, so the collector did not answer `202`. It answers `429` or another `4xx` when Otherlode did, and `503` with `Retry-After: 5` for every other failure. `forward_failed_total` has the cause |
| `busy` | `503` | No decode slot came free within 1 second. The response carries `Retry-After: 1` |
| `canceled` | `503` | The client disconnected while it waited for a decode slot |
| `timeout` | `503` | The request used its 9-second budget before decoding started, usually because the body arrived slowly. The response carries `Retry-After: 1` |

A `401` or `429` never reaches the ingest counters. They count in `auth_rejected_total` and `rate_limited_total` only. For the limits behind `too_large`, `too_many_elements`, `busy` and `rate_limited_total`, see [rate and memory limits](limits).

### Forwarding counters

These have no series when `OTHERLODE_COLLECTOR_FORWARD_URL` is not set.

| Counter | Labels | Goes up when |
|---|---|---|
| `otherlode_collector_forward_delivered_total` | `payload` | Otherlode answered a payload `202`, and the collector answered the agent `202` |
| `otherlode_collector_forward_failed_total` | `payload`, `reason` | Otherlode did not store a payload, so the collector did not answer `202`. The agent keeps the payload and sends it again. See the reasons below |
| `otherlode_collector_forward_notices_total` | `notice` | Otherlode answered a payload `202` with an `Otherlode-Notice` header. It adds one for each header value |

Each failure also adds one to `ingest_rejected_total` with `reason="sink"`.

The `reason` label on `forward_failed_total` takes these values:

| `reason` | Answer to the agent | Cause |
|---|---|---|
| `rate_limited` | `429`, with Otherlode's `Retry-After` | Otherlode answered `429` |
| `rejected` | Otherlode's status | Otherlode answered another `4xx`, such as `401`, `403` or `413` |
| `server_error` | `503` | Otherlode answered with any other status that is not `2xx`, including `5xx` and every `3xx`. The collector does not follow redirects |
| `timeout` | `503` | Otherlode did not answer within `OTHERLODE_COLLECTOR_FORWARD_REQUEST_TIMEOUT` |
| `transport` | `503` | The connection failed, so there was no answer |
| `unexpected_status` | `503` | Otherlode answered a `2xx` other than `202`. This comes from a wrong forward URL or a proxy |
| `busy` | `503` | `OTHERLODE_COLLECTOR_MAX_CONCURRENT_FORWARDS` forwards were already in flight, and none finished in time |
| `canceled` | `503` | The agent's request ended, or the shutdown deadline passed, before Otherlode answered |
| `shutting_down` | `503` | The collector had begun to shut down |
| `marshal` | `400` | The collector could not encode the payload to forward it |
| `request` | `503` | The collector could not build the request to Otherlode |

A `503` carries `Retry-After: 5`. The `notice` label on `forward_notices_total` is `service-over-cap`, `environment-not-tracked` or `unknown`. `unknown` stands for every other value, so a backend cannot add series. A notice does not mean a payload was lost, since Otherlode stored it. `service-over-cap` and `environment-not-tracked` mean Otherlode does not track the service or the environment. The collector does not know what an `unknown` notice means. See [Otherlode stores a payload but does not track it](troubleshooting#otherlode-stores-a-payload-but-does-not-track-it).

A failed payload is still in the agent's hands. The agent keeps its counts and sends them again on its next flush. [Forwarding](forwarding) covers the attempt, the time limits and shutdown.

### Token counter

| Counter | Labels | Goes up when |
|---|---|---|
| `otherlode_collector_token_reload_failures_total` | `file` | The collector re-read a token file every 30 seconds and the read failed or found no usable token. It keeps the last good value |

`file` is `auth` for `OTHERLODE_COLLECTOR_AUTH_TOKEN_FILE` and `forward` for `OTHERLODE_COLLECTOR_FORWARD_AUTH_TOKEN_FILE`. The `forward` series exists only when `OTHERLODE_COLLECTOR_FORWARD_AUTH_TOKEN_FILE` is set. See [set and rotate tokens](tokens).

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

- **`forward_failed_total`.** A rise means Otherlode is not storing payloads, and the `reason` says why. `server_error`, `timeout` and `transport` mean Otherlode is down, slow or unreachable. `rejected` and `unexpected_status` mean the forward URL, the forward key or the redaction setting is wrong, and no retry fixes them. `busy` means forwards queue behind the forward limit. Nothing is lost while the agents keep running and keep sending, but a service reads as behind until its payloads arrive. Alert on a rate that stays above zero for several minutes, since one failure during a deploy of Otherlode is normal.
- **`forward_delivered_total`.** A rate of zero while agents are running means no payload reaches Otherlode.
- **`forward_notices_total`.** A rise in `service-over-cap` or `environment-not-tracked` means the backend stores payloads it does not track. Nothing is lost, but the service or environment gets no findings. Alert on it if you want to hear before a team asks why a service shows none.
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
| `WARN` | `rejecting invalid delta batch`, `rejecting invalid manifest`, `rejecting invalid static baseline` or `rejecting invalid class listing` | A payload failed validation, with the reason in `error` |
| `WARN` | `rejecting malformed delta batch`, `rejecting malformed manifest`, `rejecting malformed static baseline` or `rejecting malformed class listing` | A body was not valid protobuf |
| `WARN` | `token file re-read failed; keeping the last good value` | A token file re-read failed, with `file` and `error` |
| `WARN` | `sink refused delta batch`, or the same with `manifest`, `static baseline` or `class listing` | Otherlode answered a `4xx`, and the collector answered the agent with that status. The line has the `error`, the `status`, `unlogged_sink_refusals` and, when Otherlode's response names an error code, `backend_error`. At most one line a minute for each payload type, status and cause |
| `WARN` | `backend stores the service's payloads but does not track the service, ...`, `backend stores the environment's payloads but does not track the environment, ...` or `backend sent a notice this collector does not know` | The backend accepted a payload and sent a notice with it. The line has the `notice`, `namespace` and `service`. At most one line an hour for each notice, namespace and service, while the collector remembers fewer than 10,000 of them |
| `WARN` | `dropped fields this collector does not know; ...` | Redaction dropped unknown fields. It logs once per run |
| `WARN` | `refusing delta batch with too many elements`, or the same with `manifest`, `static baseline` or `class listing` | A payload held more elements than the limit for its kind and got `413`. The line has the `limit` and the body size in `bytes` |
| `WARN` | `refusing delta batch: no decode slot came free`, or the same with `manifest`, `static baseline` or `class listing` | A request waited 1 second for a decode slot and got `503`. At most one line a minute. `unlogged_busy_refusals` counts the refusals since the previous line that got no line of their own |
| `WARN` | `refusing delta batch: the request used its time budget before decoding started`, or the same with `manifest`, `static baseline` or `class listing` | The body took the whole request budget to arrive and the request got `503`. The line has the `budget`. At most one line a minute. `unlogged_timeout_refusals` counts the refusals since the previous line |
| `WARN` | `agent environment does not match the collector's configured environment` | The first payload of a service that names a given environment other than the collector's. Later payloads of that service with that environment log at `DEBUG` |
| `WARN` | `agent namespace does not match the collector's configured namespace` | The same, for the service namespace |
| `ERROR` | `sink rejected delta batch`, `sink rejected manifest`, `sink rejected static baseline` or `sink rejected class listing` | The collector answered `503` because Otherlode did not store the payload. The line has the `error`, the `reason` (a value of `forward_failed_total`) and `unlogged_sink_failures`. At most one line a minute for each payload type and reason, while the counter counts every failure |
| `ERROR` | `backend answered 2xx other than 202, so the payload is not counted as stored; check OTHERLODE_COLLECTOR_FORWARD_URL` | Otherlode, or something in front of it, answered a `2xx` other than `202`. The line has the `status`, `path` and `owner`. At most one line a minute for each status |
| `ERROR` | `listen tcp ...` | The collector could not open its listen address at startup, and the process exits |
| `ERROR` | `server stopped` or `graceful shutdown failed` | The server stopped with an error after it started, or shutdown did not finish, and the process exits |

At `info` the collector also logs startup, shutdown, the stamping and redaction settings it applied, and the token file events `token file changed` and `token file re-read recovered`. The token file line gives a count of tokens, never a token. The redaction line gives the secret's fingerprint as `secret_fingerprint`, never the secret.

Startup errors name the setting and the invalid value. [Troubleshooting](troubleshooting) gives the cause and the fix for each line above.

## Related pages

- [Configuration](configuration) lists `OTHERLODE_COLLECTOR_ADDR` and `OTHERLODE_COLLECTOR_LOG_LEVEL`.
- [Forwarding](forwarding) explains the attempt, the time limits and the shutdown behind the forwarding counters.
- [Troubleshooting](troubleshooting) covers the failing health check and each log line.
