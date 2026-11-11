---
title: Forwarding
description: How the collector forwards each payload to Otherlode while the agent's request waits, including the forward URL rules, the headers it sends, the answer the agent gets for each answer from Otherlode, the time limits, the forward limit, the log lines and counters for a failed forward, shutdown, the two tuning variables and the proxy variables.
order: 40
---

Forwarding is on when you set `OTHERLODE_COLLECTOR_FORWARD_URL`. Without it, the collector logs the payloads it receives and forwards nothing. At startup it logs this warning:

```text
{"time":"...","level":"WARN","msg":"OTHERLODE_COLLECTOR_FORWARD_URL not set; ingest payloads are only logged, not forwarded"}
```

To forward to Otherlode, see [send data from your collector](/docs/server/connect) for the URL and the ingest key. This page covers what the collector does with a payload once it has decoded and processed it.

## The forward URL

The collector reads the URL once, at startup. Any invalid value stops startup with an ERROR line that names the variable and the problem. The line quotes the value, unless the value holds an `@`, since that part can be a password.

| Rule | If the URL breaks it |
|---|---|
| The scheme is `http` or `https` | Startup stops with `OTHERLODE_COLLECTOR_FORWARD_URL: forward URL "<url>": scheme must be http or https` |
| The URL has a host | Startup stops with `OTHERLODE_COLLECTOR_FORWARD_URL: forward URL "<url>": missing host` |
| The URL has no user info | Startup stops with `OTHERLODE_COLLECTOR_FORWARD_URL: forward URL must not contain user info` |
| The URL has no query | Startup stops with `OTHERLODE_COLLECTOR_FORWARD_URL: forward URL must not contain a query` |
| The URL has no fragment | Startup stops with `OTHERLODE_COLLECTOR_FORWARD_URL: forward URL must not contain a fragment` |

The collector removes trailing slashes. A path is allowed and the collector keeps it. It appends the four ingest paths to the URL. With `https://gateway.example.com/otherlode`, the collector posts to `https://gateway.example.com/otherlode/v1/otherlode/deltas`.

| Payload | Path appended to the URL |
|---|---|
| Delta batch | `/v1/otherlode/deltas` |
| Probe manifest | `/v1/otherlode/manifest` |
| Static baseline chunk | `/v1/otherlode/static-baseline` |
| Class listing chunk | `/v1/otherlode/class-listing` |

Each request is a `POST`. The body is the same protobuf message the agent sent, after the collector's processing. The `Content-Type` is `application/x-protobuf`.

### The key header

If you set a forward key, the collector sends `Authorization: Bearer <key>` on every request. It takes the current key for each request and re-reads a key file every 30 seconds, so a rotated key file applies without a restart. The agent token is a separate secret and is never sent to the backend. See [set and rotate tokens](tokens) for the key and its file.

Setting a key without `OTHERLODE_COLLECTOR_FORWARD_URL` stops startup. So does a key that holds a control character.

### The redaction header

While redaction is on, the collector sends `Otherlode-Redaction: <fingerprint>` on every request. The fingerprint is the `secret_fingerprint` value from the startup log, 16 lowercase hex characters that name the redaction secret without giving it away. With redaction off, the collector sends no such header. A tenant can have Otherlode refuse payloads without the header, or with another fingerprint. See [let Otherlode refuse payloads that were not blanked](redaction#let-otherlode-refuse-payloads-that-were-not-blanked).

### Use https

The hop to the backend carries the key. Use `https` for any backend you reach across a network you do not control. If the URL uses plain `http` and a key is set, the collector logs this warning at startup:

```text
{"time":"...","level":"WARN","msg":"forward URL uses plain http; the backend auth token is sent unencrypted","url":"http://gateway.internal:8080"}
```

## Forward while the agent waits

The collector forwards a payload while the agent's request is open, makes one attempt, and answers the agent from Otherlode's answer. It answers `202` only when Otherlode answered `202`. So every payload that an agent sees accepted is stored, and every other payload stays with the agent, which keeps its counts and sends them again on its next flush.

The collector keeps no queue, retries nothing and drops nothing. It holds a payload in memory only for the length of its request.

| Otherlode's answer | What the agent gets |
|---|---|
| `202` | `202` |
| `429` | `429`, with Otherlode's `Retry-After` when it sent one |
| Any other `4xx`, such as `400`, `401`, `403` or `413` | The same status |
| A `5xx`, a redirect or any other status that is not `2xx` | `503` with `Retry-After: 5` |
| A `2xx` other than `202` | `503` with `Retry-After: 5` |
| No answer within the forward request timeout, or no connection | `503` with `Retry-After: 5` |

Two more cases get a `503` with `Retry-After: 5` and never reach Otherlode: the forward limit is full, and the collector is shutting down.

Every answer has an empty body. The collector passes on a `Retry-After` from Otherlode only for a `429`, and only when the value is up to 64 printable ASCII characters. It does not follow redirects, so a `3xx` is a failure. A `2xx` other than `202` comes from a wrong forward URL or from a proxy, not from Otherlode, so the collector does not count the payload as stored.

The agent resends a failed payload by itself. It ends a send at once when `Retry-After` is over 2 seconds, and sends the payload again on its next flush. It keeps its counts in memory, so nothing is lost while Otherlode is down unless the agent's process stops. A process that stops during an outage reads in Otherlode as one that went silent. See [when the collector is down or refuses a payload](/docs/agent/data-sent#when-the-receiver-is-down-or-refuses-a-payload).

### Order

The collector forwards each payload as its request arrives and does not order payloads across requests. Otherlode orders a run's payloads by the sequence number the agent stamps on each one.

## Time limits

A request has a budget of 9 seconds from the moment the collector starts on it. The agent gives up on an attempt after 10 seconds, so it always gets the collector's answer first. Three limits share the budget.

- **The decode slot.** A request waits for a decode slot for at most 1 second. If none comes free, the agent gets `503` with `Retry-After: 1`. [Rate and memory limits](limits#decode-cap) covers the slot.
- **The forward slot.** A request takes one of `OTHERLODE_COLLECTOR_MAX_CONCURRENT_FORWARDS` forward slots before it frees its decode slot. See [the forward limit](#the-forward-limit).
- **The forward request.** The attempt to Otherlode ends after `OTHERLODE_COLLECTOR_FORWARD_REQUEST_TIMEOUT`, which is 8 seconds by default, or when the request's budget ends, whichever comes first.

At startup the collector refuses a request timeout that is not below the budget. A value of `9s` or more stops it with this line:

```text
{"time":"...","level":"ERROR","msg":"OTHERLODE_COLLECTOR_FORWARD_REQUEST_TIMEOUT is 9s, which is not below the 9s time budget of one request; set it lower"}
```

When Otherlode does not answer in time, the agent gets its `503` after the timeout, for example after 8 seconds with the default.

## The forward limit

`OTHERLODE_COLLECTOR_MAX_CONCURRENT_FORWARDS` bounds how many forwards are in flight to Otherlode at one time. It defaults to 32. The collector keeps that many idle connections to Otherlode.

A request takes a forward slot before it frees its decode slot. When Otherlode is slow and every forward slot is taken, a waiting request keeps its decode slot, so decoding stops taking new requests instead of holding more decoded payloads. A request that waits for a forward slot for the length of the forward request timeout, or until its budget ends, gets `503` with `Retry-After: 5`. The collector counts it under reason `busy` in `forward_failed_total`.

Raising `OTHERLODE_COLLECTOR_MAX_CONCURRENT_DECODES` does not help here, because it limits decoding and not forwarding. Each forward in flight holds its marshaled payload, which counts toward the [worst-case memory](limits#worst-case-memory).

## Failed forwards in the log and the counters

When Otherlode does not store a payload, the collector logs one line and counts the failure in `otherlode_collector_forward_failed_total` with a `reason`. The counter counts every failure. The log writes at most one line a minute for each payload type and cause, and the field `unlogged_sink_failures` or `unlogged_sink_refusals` counts the failures since the previous line that got no line of their own.

A `4xx` from Otherlode logs a `WARN` line, and the collector answers the agent with that status.

```text
{"time":"...","level":"WARN","msg":"sink refused delta batch","error":"sink answered Unauthorized: backend returned 401 Unauthorized for service \"demo\" instance \"i1\"","status":401,"unlogged_sink_refusals":0,"backend_error":"ingest_key_invalid"}
```

Any other failure logs an `ERROR` line, and the collector answers `503`.

```text
{"time":"...","level":"ERROR","msg":"sink rejected delta batch","error":"backend returned 500 Internal Server Error for service \"demo\" instance \"i1\"","reason":"server_error","unlogged_sink_failures":0}
{"time":"...","level":"ERROR","msg":"sink rejected delta batch","error":"forward for service \"demo\" instance \"i1\": Post \"http://127.0.0.1:4400/v1/otherlode/deltas\": context deadline exceeded","reason":"timeout","unlogged_sink_failures":0}
```

A `2xx` other than `202` logs a line of its own first, at most once a minute for each status, and then the `sink rejected` line.

```text
{"time":"...","level":"ERROR","msg":"backend answered 2xx other than 202, so the payload is not counted as stored; check OTHERLODE_COLLECTOR_FORWARD_URL","status":200,"path":"/v1/otherlode/deltas","owner":"service \"demo\" instance \"i1\"","unlogged_unexpected_statuses":0}
{"time":"...","level":"ERROR","msg":"sink rejected delta batch","error":"backend returned 200 OK, not 202, for service \"demo\" instance \"i1\"","reason":"unexpected_status","unlogged_sink_failures":0}
```

The `error` field names the service and instance. When Otherlode answers with a JSON body that has an `error` string, such as `{"error":"redaction_required"}`, the `WARN` line adds that string as `backend_error`. The collector keeps at most 64 bytes of it and replaces each byte outside printable ASCII with `?`. A body that is not such an object adds nothing.

A `403` with `redaction_required` or `redaction_secret_mismatch` means the tenant requires redaction and this collector does not match. [Troubleshooting](troubleshooting#403-with-redaction_required-or-redaction_secret_mismatch) has the fix. For what else Otherlode's ingest answers and what to fix, see [Otherlode's troubleshooting](/docs/server/troubleshooting#the-collector-logs-a-refused-payload).

Each failure also adds one to `otherlode_collector_ingest_rejected_total` with `reason="sink"`.

## Shutdown

On `SIGTERM` or `SIGINT`, the collector logs `shutting down` and closes its listener at once, so a new connection is refused. It stops the HTTP server and the forwarder at the same time, and both share one 10-second deadline. Give the container a stop timeout longer than 10 seconds, or the runtime may kill the collector first.

A request already in flight finishes with Otherlode's answer. If the deadline passes first, the collector cancels the forward, and the agent gets `503` with `Retry-After: 5` when its connection is still open. A request that reaches the forwarder after shutdown starts, on a connection that was already open, also gets that `503`. The collector exits as soon as the last request ends, or at the deadline.

Nothing sits in a queue at shutdown, so the stop loses no payload. A payload whose request the stop cut short stays with the agent, which sends it again.

## Counters

Each forward ends in one of these counters on `/metrics`. The `payload` label is `deltas`, `manifest`, `static_baseline` or `class_listing`. See [monitoring](monitoring) for the full table and every `reason`.

| Outcome | Counter |
|---|---|
| Otherlode answered `202` | `otherlode_collector_forward_delivered_total` |
| Otherlode did not store the payload | `otherlode_collector_forward_failed_total` with a `reason` of `rate_limited`, `rejected`, `server_error`, `timeout`, `transport`, `unexpected_status`, `busy`, `canceled`, `shutting_down`, `marshal` or `request`, and `otherlode_collector_ingest_rejected_total` with `reason` `sink` |
| Otherlode answered `202` and sent a notice | `otherlode_collector_forward_notices_total` with `notice` of `service-over-cap`, `environment-not-tracked` or `unknown`, and a `WARN` line at most once an hour for each notice, namespace and service |

## Tuning

The defaults suit most deployments. Set a variable only when you have a reason. The collector reads all values once at startup. A value that is not a number, not a duration or not above zero stops startup with an ERROR line that names the variable, such as `OTHERLODE_COLLECTOR_MAX_CONCURRENT_FORWARDS must be positive`. An empty value uses the default. Durations use Go syntax, such as `5s` or `8s`.

| Variable | Default | Meaning |
|---|---|---|
| `OTHERLODE_COLLECTOR_FORWARD_URL` | None | Otherlode's base URL. Forwarding is on only when it is set |
| `OTHERLODE_COLLECTOR_FORWARD_AUTH_TOKEN` | None | The forward key. Set this or the file variable, not both |
| `OTHERLODE_COLLECTOR_FORWARD_AUTH_TOKEN_FILE` | None | Path to a file that holds the forward key |
| `OTHERLODE_COLLECTOR_FORWARD_REQUEST_TIMEOUT` | `8s` | Time limit for the one attempt to Otherlode. It must be below `9s` |
| `OTHERLODE_COLLECTOR_MAX_CONCURRENT_FORWARDS` | `32` | Forwards in flight to Otherlode at one time |

Lower the request timeout if you want a slow Otherlode to hand the agent a `503` sooner. Raise the forward limit if `busy` appears in `forward_failed_total` while Otherlode answers quickly.

## Proxies

The forwarder honours `HTTPS_PROXY`, `HTTP_PROXY` and `NO_PROXY`, and their lowercase forms. Set them in the collector's environment before it starts. The collector reads them when it first forwards a request and does not pick up a later change. Requests to `localhost` and loopback addresses never use a proxy.

The `healthcheck` subcommand never uses a proxy.

## Related pages

- [Send data to Otherlode](/docs/server/connect) has Otherlode's URL and the ingest key.
- [Set and rotate tokens](tokens) covers the forward key and its file.
- [Health checks, metrics and logs](monitoring) lists every counter.
- [Rate and memory limits](limits) covers the decode cap and the memory bound.
- [Troubleshooting](troubleshooting) covers a payload Otherlode did not store and the agent's `503` answers.
