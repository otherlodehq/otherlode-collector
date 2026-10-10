---
title: Forwarding
description: How the collector forwards payloads after it answers 202, including the forward URL rules, the headers it sends, per-instance queues and order, retried statuses and the retry budget, the backend's error code in a drop, the 503 for a full queue, the shutdown drain, the counters, the tuning variables and the proxy variables.
order: 40
---

Forwarding is on when you set `OTHERLODE_COLLECTOR_FORWARD_URL`. Without it, the collector logs the payloads it receives and forwards nothing. At startup it logs this warning:

```text
{"time":"...","level":"WARN","msg":"OTHERLODE_COLLECTOR_FORWARD_URL not set; ingest payloads are only logged, not forwarded"}
```

To forward to Otherlode, see [send data from your collector](/docs/server/connect) for the URL and the ingest key. This page covers what the collector does with a payload after it accepts it.

## The forward URL

The collector reads the URL once, at startup. Any invalid value stops startup with an ERROR line that names the problem.

| Rule | If the URL breaks it |
|---|---|
| The scheme is `http` or `https` | Startup stops with `forward URL "<url>": scheme must be http or https` |
| The URL has a host | Startup stops with `forward URL "<url>": missing host` |
| The URL has no user info | Startup stops with `forward URL must not contain user info` |
| The URL has no query | Startup stops with `forward URL must not contain a query` |
| The URL has no fragment | Startup stops with `forward URL must not contain a fragment` |

The collector removes trailing slashes. A path is allowed and the collector keeps it. It appends the three ingest paths to the URL. With `https://gateway.example.com/otherlode`, the collector posts to `https://gateway.example.com/otherlode/v1/otherlode/deltas`.

| Payload | Path appended to the URL |
|---|---|
| Delta batch | `/v1/otherlode/deltas` |
| Probe manifest | `/v1/otherlode/manifest` |
| Static baseline chunk | `/v1/otherlode/static-baseline` |

Each request is a `POST`. The body is the same protobuf message the agent sent, after the collector's processing. The `Content-Type` is `application/x-protobuf`.

### The key header

If you set a forward key, the collector sends `Authorization: Bearer <key>` on every request. It reads the key again before each request, so a rotated key file applies without a restart. The agent token is a separate secret and is never sent to the backend. See [set and rotate tokens](tokens) for the key and its file.

Setting a key without `OTHERLODE_COLLECTOR_FORWARD_URL` stops startup. So does a key that holds a control character.

### The redaction header

While redaction is on, the collector sends `Otherlode-Redaction: <fingerprint>` on every request. The fingerprint is the `secret_fingerprint` value from the startup log, 16 lowercase hex characters that name the redaction secret without giving it away. With redaction off, the collector sends no such header. A tenant can have Otherlode refuse payloads without the header, or with another fingerprint. See [let Otherlode refuse payloads that were not blanked](redaction#let-otherlode-refuse-payloads-that-were-not-blanked).

### Use https

The hop to the backend carries the key. Use `https` for any backend you reach across a network you do not control. If the URL uses plain `http` and a key is set, the collector logs this warning at startup:

```text
{"time":"...","level":"WARN","msg":"forward URL uses plain http; the backend auth token is sent unencrypted","url":"http://gateway.internal:8080"}
```

## What happens after the 202

The collector answers the agent with `202` as soon as it has queued the payload. Delivery runs in the background. The agent never waits for the backend, and it never learns whether delivery worked.

### Queues and order

The collector has a fixed number of queues, 8 by default, and each queue has one worker. A payload goes to a queue by its service namespace, service name and instance ID.

- One instance always uses the same queue, so its payloads leave in the order they arrived.
- A restarted instance gets a new run ID but keeps its queue. Its order holds across the restart.
- A worker sends one payload at a time. The collector has at most one request in flight per queue.
- Instances that share a queue wait on each other. If the backend stalls on one instance's payload, only the other instances on that queue wait. Instances on other queues are not held up.

### Retried statuses

The collector retries only when a retry can help.

| Backend answer | What the collector does |
|---|---|
| Any `2xx` | Counts the payload as delivered |
| `429`, `502`, `503` or `504` | Retries |
| No response (connection error or request timeout) | Retries |
| Any other status, including `500`, `400`, `401`, `403`, `404`, `413` and every `3xx` | Drops the payload at once |

The collector does not follow redirects. A `3xx` answer is a drop.

### Backoff and the budget

The wait before the first retry is 5 seconds. Each later wait is 1.5 times the one before, up to 30 seconds. The collector then adds jitter, so each wait is a random length between half and one and a half times the interval. The first wait is therefore between 2.5 and 7.5 seconds.

The budget limits retrying by time, not by attempt count. It starts at the first attempt and runs 5 minutes by default. Time spent inside the attempts counts against it. When the budget is spent, the collector drops the payload. The last wait is cut short so the final attempt lands at the end of the budget.

If the backend sends a `Retry-After` header with a retried status, the collector waits for the longer of the jittered interval and the header. The header can be a number of seconds or an HTTP date. A missing, malformed or past value is ignored. The collector does not cap the header, except that a wait never runs past the budget. A long `Retry-After` therefore leaves one last attempt at the end of the budget.

### Dropped payloads

A payload the collector drops after the 202 is lost. The agent already has its `202`, so it does not send that payload again. The collector logs one WARN line for each drop, with the instance it belonged to and the backend's status.

```text
{"time":"...","level":"WARN","msg":"dropping payload: permanent failure","namespace":"","service":"checkout","instance":"3f1c9a","path":"/v1/otherlode/deltas","error":"backend returned 401 Unauthorized"}
```

| Message | Cause |
|---|---|
| `dropping payload: permanent failure` | The backend answered with a status the collector does not retry |
| `dropping payload: retry budget exhausted` | Retries ran for the whole budget |
| `dropping payload: shutdown drain attempt failed` | The one final attempt at shutdown failed |
| `dropping payload: shutdown deadline exceeded` | The shutdown time ran out before the attempt |
| `dropping payload: marshal failed` | The collector could not encode the payload. It logs `payload` and `error` instead of the fields above, and the agent still got its `202` |

The `error` field ends with the status. When the backend answers with a JSON body that has an `error` string, such as `{"error":"redaction_required"}`, the line adds that string as `backend_error`. The collector keeps at most 64 bytes of it and replaces each byte outside printable ASCII with `?`. A body that is not such an object adds nothing.

```text
{"time":"...","level":"WARN","msg":"dropping payload: permanent failure","namespace":"","service":"checkout","instance":"3f1c9a","path":"/v1/otherlode/deltas","error":"backend returned 403 Forbidden","backend_error":"redaction_secret_mismatch"}
```

A `403` with `redaction_required` or `redaction_secret_mismatch` means the tenant requires redaction and this collector does not match. [Troubleshooting](troubleshooting#403-with-redaction_required-or-redaction_secret_mismatch) has the fix. For what else Otherlode's ingest answers and what to fix, see [troubleshooting on the server](/docs/server/troubleshooting#the-collector-logs-a-dropped-payload).

## When the queue is full

Each queue holds up to 64 payloads and up to 64 MiB of encoded payload bytes by default, counting the payload in flight. A queue that holds nothing accepts any payload, even one larger than the byte limit.

If the payload's queue is full by count or by bytes, or the collector is shutting down, the collector does not take the payload. It answers the agent `503` with `Retry-After: 5` and no body, and logs an ERROR line that names the instance:

```text
{"time":"...","level":"ERROR","msg":"sink rejected delta batch","error":"forward: shard queue is full (service \"checkout\" instance \"3f1c9a\")"}
```

The agent loses nothing. It keeps its counts and builds the payload again on its next flush. See [when the collector is down or refuses a payload](/docs/agent/data-sent#when-the-collector-is-down-or-refuses-a-payload).

A full queue means the backend is slower than the agents, or it is failing and the retries have not run out.

## Shutdown

On `SIGTERM` or `SIGINT`, the collector stops the HTTP server and drains the forwarder at the same time. Both share one 10-second deadline, so a slow request to the HTTP server cannot use up the drain's time. Give the container a stop timeout longer than 10 seconds, or the runtime may kill the collector first.

Once the drain starts, the forwarder takes no new payloads. A payload that reaches it while the HTTP server finishes its open requests gets `503` with `Retry-After: 5`, and the agent sends it again later. Every payload the forwarder still holds gets one final attempt. That includes queued payloads and a payload waiting between retries. The final attempt does not retry.

The queues drain in parallel, and each queue drains in order. The attempt in flight is cancelled when the deadline passes, and the payloads not yet attempted are dropped. This is a best-effort drain and does not guarantee delivery.

## Counters

Each outcome increments these counters on `/metrics`. The `payload` label is `deltas`, `manifest` or `static_baseline`. See [monitoring](monitoring) for the full table.

| Outcome | Counter |
|---|---|
| Backend accepted the payload | `otherlode_collector_forward_delivered_total` |
| A wait ended and the collector retried | `otherlode_collector_forward_retries_total` |
| Dropped after the 202 | `otherlode_collector_forward_dropped_total` with `reason` of `permanent`, `retry_exhausted`, `shutdown_deadline`, `shutdown_attempt_failed` or `marshal` |
| Refused with 503 | `otherlode_collector_forward_refused_total` with `reason` of `queue_full` or `shutting_down`, and `otherlode_collector_ingest_rejected_total` with `reason` `sink` |

## Tuning

The defaults suit most deployments. Set a variable only when you have a reason. All values are read once at startup. A value that is not a number, not a duration or not above zero stops startup with an ERROR line that names the variable, such as `OTHERLODE_COLLECTOR_FORWARD_SHARDS must be positive`. An empty value uses the default. Durations use Go syntax, such as `30s` or `5m`.

| Variable | Default | Meaning |
|---|---|---|
| `OTHERLODE_COLLECTOR_FORWARD_URL` | None | The backend's base URL. Forwarding is on only when it is set |
| `OTHERLODE_COLLECTOR_FORWARD_AUTH_TOKEN` | None | The forward key. Set this or the file variable, not both |
| `OTHERLODE_COLLECTOR_FORWARD_AUTH_TOKEN_FILE` | None | Path to a file that holds the forward key |
| `OTHERLODE_COLLECTOR_FORWARD_SHARDS` | `8` | Number of queues, each with one worker |
| `OTHERLODE_COLLECTOR_FORWARD_QUEUE_SIZE` | `64` | Payloads one queue holds before it refuses new ones |
| `OTHERLODE_COLLECTOR_FORWARD_QUEUE_BYTES` | `67108864` (64 MiB) | Encoded bytes one queue holds, including the payload in flight, before it refuses new ones |
| `OTHERLODE_COLLECTOR_FORWARD_REQUEST_TIMEOUT` | `10s` | Time limit for one attempt |
| `OTHERLODE_COLLECTOR_FORWARD_RETRY_INITIAL_INTERVAL` | `5s` | The first retry wait, before jitter |
| `OTHERLODE_COLLECTOR_FORWARD_RETRY_MAX_INTERVAL` | `30s` | The longest retry wait, before jitter |
| `OTHERLODE_COLLECTOR_FORWARD_RETRY_MAX_ELAPSED_TIME` | `5m` | How long the collector retries one payload before it drops it |

Queued payloads can use up to the number of queues times the larger of the byte limit and the largest payload. With the defaults that is 512 MiB. Count that memory when you size the container. See [limits](limits) for the other memory bounds.

An initial interval above the maximum interval is lowered to the maximum.

## Proxies

The forwarder honours `HTTPS_PROXY`, `HTTP_PROXY` and `NO_PROXY`, and their lowercase forms. Set them in the collector's environment before it starts. The collector reads them when it first forwards a request and does not pick up a later change. Requests to `localhost` and loopback addresses never use a proxy.

The `healthcheck` subcommand never uses a proxy.

## Related pages

- [Send data from your collector](/docs/server/connect) has Otherlode's URL and the ingest key.
- [Set and rotate tokens](tokens) covers the forward key and its file.
- [Health checks, metrics and logs](monitoring) lists every counter.
- [Troubleshooting](troubleshooting) covers dropped payloads and 503 answers.
