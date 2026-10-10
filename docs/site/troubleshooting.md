---
title: Troubleshooting
description: Find the cause of a collector that will not start, an agent that gets a 401, 404, 405, 413, 415, 429, 400 or 503, a dropped payload, a 403 for redaction, branch history that starts over, the stripped-fields warning, a token rotation that did not apply, a shared rate limit or a failing healthcheck.
order: 110
---

Every entry starts from something you see, either a log line or an HTTP status. Each gives the text as the collector writes it, the cause, and the fix. Each fix links the page that owns the detail.

## Read the collector's log

The collector writes one JSON object per line to standard output. Read it with `docker logs`, `kubectl logs` or your log shipper. The `msg` field holds the message and the `level` field holds `INFO`, `WARN` or `ERROR`.

```text
{"time":"2026-10-10T16:31:59.20978+01:00","level":"ERROR","msg":"OTHERLODE_COLLECTOR_AUTH_TOKEN and OTHERLODE_COLLECTOR_AUTH_TOKEN_FILE are both set; set one"}
```

The default level is `info`, and this page quotes only what you see at that level. To see more, set `OTHERLODE_COLLECTOR_LOG_LEVEL` to `debug`, `info`, `warn` or `error`, in any case, and restart the collector. [Health checks, metrics and logs](monitoring) covers the log format and the counters that this page refers to.

## The collector does not start

A startup failure logs one `ERROR` line and exits with code 1. The collector reads every setting once and stops at the first one it cannot use. Fix that setting, start again, and read the next line if there is one. [Configuration](configuration) lists every setting with its default and accepted values.

### No agent token is set

```text
{"time":"...","level":"ERROR","msg":"neither OTHERLODE_COLLECTOR_AUTH_TOKEN nor OTHERLODE_COLLECTOR_AUTH_TOKEN_FILE is set; refusing to start without auth (set OTHERLODE_COLLECTOR_INSECURE_NO_AUTH=1 to run unauthenticated)"}
```

The collector refuses to accept agent traffic without a token. Set `OTHERLODE_COLLECTOR_AUTH_TOKEN` or `OTHERLODE_COLLECTOR_AUTH_TOKEN_FILE`. [Set and rotate tokens](tokens) shows both.

The opt-out `OTHERLODE_COLLECTOR_INSECURE_NO_AUTH=1` is for local use only. With it, anyone who can reach the port can send payloads that the collector forwards under your key.

A value of `OTHERLODE_COLLECTOR_INSECURE_NO_AUTH` that Go cannot read as a boolean, such as `yes`, stops startup with its own line, even when a token is set:

```text
{"time":"...","level":"ERROR","msg":"OTHERLODE_COLLECTOR_INSECURE_NO_AUTH: strconv.ParseBool: parsing \"yes\": invalid syntax"}
```

Use `1` or `true` to opt out, or unset the variable.

### The token settings conflict or hold nothing usable

| Log line | Cause and fix |
|---|---|
| `OTHERLODE_COLLECTOR_AUTH_TOKEN and OTHERLODE_COLLECTOR_AUTH_TOKEN_FILE are both set; set one` | Unset one of the two. |
| `OTHERLODE_COLLECTOR_AUTH_TOKEN: entry 2 of 3 is empty` | The list has an empty entry, such as `a,,b` or a trailing comma. The line gives the position. Remove the empty entry. |
| `OTHERLODE_COLLECTOR_AUTH_TOKEN_FILE: open /run/secrets/agent-tokens: no such file or directory` | The path does not exist inside the container. Check the volume or secret mount. |
| `OTHERLODE_COLLECTOR_AUTH_TOKEN_FILE: /run/secrets/agent-tokens is not a regular file` | The path names a directory or a pipe. Point it at the file. |
| `OTHERLODE_COLLECTOR_AUTH_TOKEN_FILE: /run/secrets/agent-tokens is larger than 1048576 bytes` | The file is over 1 MiB. A token file holds a few short lines. |
| `OTHERLODE_COLLECTOR_AUTH_TOKEN_FILE: /run/secrets/agent-tokens: no token found` | The file is empty, or holds only blank lines and `#` comments. Add at least one token. |

A token file that cannot be read stops startup even with the opt-out set, because you asked for that file.

### The forward URL is not usable

When `OTHERLODE_COLLECTOR_FORWARD_URL` is set, it must be an `http` or `https` URL with a host and no user info, query or fragment. A trailing slash is removed. A path is kept. Each bad value has its own line.

| Log line | Fix |
|---|---|
| `forward URL "collector.example.com": scheme must be http or https` | Add the scheme. The line quotes the value you set. |
| `forward URL "https://": missing host` | Add the host. |
| `forward URL must not contain user info` | Remove `user:password@`. Use the forward key instead. |
| `forward URL must not contain a query` | Remove everything from `?`. |
| `forward URL must not contain a fragment` | Remove everything from `#`. |

The URL to use for Otherlode is on the server's [connect page](/docs/server/connect). [Forwarding](forwarding) describes what the collector does after it accepts a payload.

### The forward key settings are wrong

| Log line | Cause and fix |
|---|---|
| `OTHERLODE_COLLECTOR_FORWARD_AUTH_TOKEN or OTHERLODE_COLLECTOR_FORWARD_AUTH_TOKEN_FILE is set but OTHERLODE_COLLECTOR_FORWARD_URL is empty; set OTHERLODE_COLLECTOR_FORWARD_URL or unset the key variable` | You set a key but no URL. Set the URL, or unset the key to run log-only. |
| `OTHERLODE_COLLECTOR_FORWARD_AUTH_TOKEN and OTHERLODE_COLLECTOR_FORWARD_AUTH_TOKEN_FILE are both set; set one` | Unset one of the two. |
| `OTHERLODE_COLLECTOR_FORWARD_AUTH_TOKEN holds only spaces` | The variable is set but blank. Set the key or unset the variable. |
| `OTHERLODE_COLLECTOR_FORWARD_AUTH_TOKEN: the key holds a control character at byte 12` | The key has a newline, tab or other control character. Copy the key again without trailing whitespace. A key file with such a character gives the same message after the file path. |
| `OTHERLODE_COLLECTOR_FORWARD_AUTH_TOKEN_FILE: /run/secrets/forward-key: found 2 tokens, want exactly one` | The key file must hold exactly one key. Remove the extra lines. |

The key file can also fail with the file errors in the table above, under the name `OTHERLODE_COLLECTOR_FORWARD_AUTH_TOKEN_FILE`. [Set and rotate tokens](tokens) describes the key and its file.

### A number, a duration or a switch does not parse

A value that does not parse, or a number that is zero or negative where the setting needs a positive one, gives a line that starts with the variable name.

| Log line | Cause and fix |
|---|---|
| `OTHERLODE_COLLECTOR_FORWARD_SHARDS must be positive` | The same line applies to `OTHERLODE_COLLECTOR_FORWARD_QUEUE_SIZE`, `OTHERLODE_COLLECTOR_FORWARD_QUEUE_BYTES` and `OTHERLODE_COLLECTOR_MAX_CONCURRENT_DECODES`. Use a whole number above 0. |
| `OTHERLODE_COLLECTOR_FORWARD_REQUEST_TIMEOUT: time: invalid duration "abc"` | Applies to the three `OTHERLODE_COLLECTOR_FORWARD_RETRY_*` durations too. Use Go syntax such as `30s` or `5m`. |
| `OTHERLODE_COLLECTOR_FORWARD_SHARDS: strconv.Atoi: parsing "eight": invalid syntax` | Use digits only. |
| `OTHERLODE_COLLECTOR_RATE_LIMIT_RPS: strconv.ParseFloat: parsing "fast": invalid syntax` | Use a number. `0` turns the limit off. |
| `OTHERLODE_COLLECTOR_RATE_LIMIT_RPS must not be negative` | Use 0 or more. Infinity and NaN give `must be a finite number`. |
| `OTHERLODE_COLLECTOR_RATE_LIMIT_BURST must be positive when rate limiting is enabled` | Use 1 or more, or set the rate to 0. |
| `OTHERLODE_COLLECTOR_RATE_LIMIT_IPV6_PREFIX must be from 1 to 128` | Use a whole number from 1 to 128. |
| `OTHERLODE_COLLECTOR_LOG_LEVEL: slog: level string "loud": unknown name` | Use `debug`, `info`, `warn` or `error`, in any case, with an optional offset such as `warn+1`. `warning` is not a level. |

[Rate and memory limits](limits) and [forwarding](forwarding) explain what each of these settings does.

### The environment, namespace or redaction settings are wrong

| Log line | Cause and fix |
|---|---|
| `OTHERLODE_COLLECTOR_ENVIRONMENT_ACTION is set but OTHERLODE_COLLECTOR_ENVIRONMENT is empty; set OTHERLODE_COLLECTOR_ENVIRONMENT or unset OTHERLODE_COLLECTOR_ENVIRONMENT_ACTION` | An action with no value. The namespace pair gives the same line with `SERVICE_NAMESPACE`. |
| `OTHERLODE_COLLECTOR_ENVIRONMENT_ACTION: invalid action "replace": must be "insert" or "upsert"` | Use `insert` or `upsert`, in any case. The namespace action gives the same line under its own name. |
| `OTHERLODE_COLLECTOR_SERVICE_NAMESPACE is "..", which no URL path can name` | `.` and `..` are refused. Pick another namespace. |
| `OTHERLODE_COLLECTOR_ENVIRONMENT is not valid UTF-8` | Applies to the namespace too. Re-enter the value as UTF-8. |
| `OTHERLODE_COLLECTOR_REDACT_BLOCKED_VALUES line 2: error parsing regexp: missing closing ): ` followed by the pattern | The line number counts from 1. Fix that pattern. Each line is one regular expression. |
| `OTHERLODE_COLLECTOR_REDACT_ALL_LITERALS: strconv.ParseBool: parsing "maybe": invalid syntax` | Use `true` or `false`. A typo stops startup, so it cannot leave redaction off. |

[Set the environment and namespace](environment-and-namespace) and [blank out string literals](redaction) own these settings.

### The redaction secret is missing or unusable

While redaction is on, the collector needs a secret in `OTHERLODE_COLLECTOR_REDACT_SECRET` or `OTHERLODE_COLLECTOR_REDACT_SECRET_FILE`. No line quotes the secret.

| Log line | Cause and fix |
|---|---|
| `redaction is on but neither OTHERLODE_COLLECTOR_REDACT_SECRET nor OTHERLODE_COLLECTOR_REDACT_SECRET_FILE is set; refusing to start, since plain branch and site keys let anyone who holds them test guesses at a redacted literal` | Set one of the two. Use the secret the tenant's other collectors use. If this is the tenant's first, make one with `openssl rand -hex 32`. |
| `OTHERLODE_COLLECTOR_REDACT_SECRET and OTHERLODE_COLLECTOR_REDACT_SECRET_FILE are both set; set one` | Unset one of the two. |
| `OTHERLODE_COLLECTOR_REDACT_SECRET: the secret is 5 bytes, want at least 32` | Use a secret of at least 32 bytes. |
| `OTHERLODE_COLLECTOR_REDACT_SECRET: the secret holds a control character at byte 16` | Remove the tab, newline or other control character. |
| `OTHERLODE_COLLECTOR_REDACT_SECRET holds only spaces` | Set the secret or unset the variable. |
| `OTHERLODE_COLLECTOR_REDACT_SECRET equals an agent auth token, which every agent holds; use a secret of its own` | Use a separate secret. |
| `OTHERLODE_COLLECTOR_REDACT_SECRET equals the forward key, which the backend holds; use a secret of its own` | Use a separate secret. |
| `OTHERLODE_COLLECTOR_REDACT_SECRET or OTHERLODE_COLLECTOR_REDACT_SECRET_FILE is set but redaction is off; set OTHERLODE_COLLECTOR_REDACT_BLOCKED_VALUES or OTHERLODE_COLLECTOR_REDACT_ALL_LITERALS, or unset the secret variable` | Turn redaction on, or unset the secret variable. `OTHERLODE_COLLECTOR_REDACT_ALL_LITERALS=false` counts as off. |

A secret file gives the same lines under `OTHERLODE_COLLECTOR_REDACT_SECRET_FILE`, plus the file errors in the token table above. A file with two lines gives `found 2 tokens, want exactly one`. [Replace branch and site keys](redaction#replace-branch-and-site-keys) has the rules.

### The collector cannot listen

```text
{"time":"2026-10-10T17:32:27.534316+01:00","level":"ERROR","msg":"listen tcp :4319: bind: address already in use"}
```

The collector opens the port after it has checked every other setting. If it cannot, it logs the cause and exits with code 1. It does not log `otherlode-collector listening`, which appears only once the port is open.

| Log line | Cause and fix |
|---|---|
| `listen tcp :4319: bind: address already in use` | Another process holds the port. Stop it, or set `OTHERLODE_COLLECTOR_ADDR` to a free port. |
| `listen tcp: address bad: missing port in address` | `OTHERLODE_COLLECTOR_ADDR` has no port. Use `host:port` or `:port`. |
| `listen tcp: address 99999: invalid port` | The port is out of range. |

The image runs as `nonroot` and listens on `:4319` by default. [Deploy the collector](deploy) covers the listen address.

## Agents get a 401, 404, 405, 413, 415, 429, 400 or 503

This section covers what the collector answers. For what the agent logs and does about each status, see the agent's [Nothing reaches the collector](/docs/agent/troubleshooting#nothing-reaches-the-collector).

The collector checks a request in this order. A path outside `/v1/otherlode/` gets a 404 or 405 at once. Inside it, the rate limit comes first, then the token, then the path, method, content type, size and body. So a request with a wrong token to a wrong path gets a 401, not a 404.

The collector writes no log line for a 401, 404, 405, 413, 415 or 429. It does log a `WARN` or `ERROR` line for a 400 or a 503, as described below. The counter `otherlode_collector_ingest_rejected_total`, labelled by `payload` and `reason`, counts the rejections that reach the ingest handler. The 401 and the 429 have their own counters, `otherlode_collector_auth_rejected_total` and `otherlode_collector_rate_limited_total`.

### 401

The response carries `WWW-Authenticate: Bearer realm="otherlode-collector"` and the body `unauthorized`. The request had no `Authorization` header, used a scheme other than `Bearer`, or sent a token that is not in the collector's list. The collector does not say which.

1. Compare the token the agent has with the one the collector holds. The agent's is `OTHERLODE_AUTH_TOKEN`.
2. If you just rotated, look for `token file changed` in the log. See [A token rotation has not taken effect](#a-token-rotation-has-not-taken-effect).
3. Test from the agent's host with `curl -i -X POST -H "Authorization: Bearer $TOKEN" -H "Content-Type: application/x-protobuf" http://collector:4319/v1/otherlode/deltas`. A 400 with `invalid delta batch: missing resource` means the collector accepted the token.

With `OTHERLODE_COLLECTOR_INSECURE_NO_AUTH=1` the collector never answers 401. [Set and rotate tokens](tokens) covers the list, and the agent's [authToken](/docs/agent/configuration#authtoken) is the other half.

### 404

The body is `404 page not found`. The collector serves exactly three paths for agents, and an agent posts to them under its `exportUrl`.

| Path | Carries |
|---|---|
| `/v1/otherlode/deltas` | Delta batches |
| `/v1/otherlode/manifest` | The probe manifest |
| `/v1/otherlode/static-baseline` | The static baseline |

Any other path is a 404, including a trailing slash and any prefix. The usual cause is a path in the agent's `exportUrl` that the collector does not expect. The agent appends the three paths to whatever `exportUrl` holds, so `https://gateway.example.com/otherlode` posts to `/otherlode/v1/otherlode/deltas`. Either strip the prefix at the proxy before the request reaches the collector, or remove the path from `exportUrl`. [The agent's `exportUrl`](/docs/agent/configuration#exporturl) says how it builds the URL.

### 405

The body is `Method Not Allowed`, with an `Allow: POST` header on the ingest paths. The request used another method, such as `GET`, on an ingest path. The agent only sends `POST`. Check any proxy rule that changes the method. `/healthz` and `/metrics` accept only `GET`, so a `POST` there gets a 405 as well.

### 413

The body is `request body too large`. The request body is over 16 MiB. The limit is fixed and no setting changes it. If the body text differs, the 413 comes from a proxy or gateway in front of the collector. Raise its request-size limit to at least 16 MiB. [Rate and memory limits](limits) explains the 16 MiB cap, and the agent's [data sent](/docs/agent/data-sent#chunk-caps) lists the caps that keep its payloads under it.

### 415

The body is `unsupported content type` or `unsupported content encoding`. The ingest paths accept `Content-Type: application/x-protobuf`, with parameters such as a charset allowed. They accept no `Content-Encoding` other than `identity`, because the collector does not decompress. A proxy that compresses request bodies with `gzip` causes the second. Turn request compression off for these paths. A client that sends another type, such as a browser or a probe, causes the first.

### 429

The body is `rate limit exceeded`, and the response carries `Retry-After` in whole seconds. One client address sent more than the limit, which defaults to 5 requests per second with a burst of 20. The counter `otherlode_collector_rate_limited_total` counts these.

An agent flushes every 60 seconds by default, so one agent rarely reaches this limit. If many agents get it, they share an address. See [All agents share one rate limit behind a proxy](#all-agents-share-one-rate-limit-behind-a-proxy). If one host sends a burst, raise the limit with `OTHERLODE_COLLECTOR_RATE_LIMIT_RPS` and `OTHERLODE_COLLECTOR_RATE_LIMIT_BURST`. [Rate and memory limits](limits) has the settings.

### 400

A 400 means the collector read the request and could not use it. The body says why, and the collector logs a `WARN` line for the first two forms.

| Response body | Log `msg` | Cause |
|---|---|---|
| `malformed delta batch` | `rejecting malformed delta batch` | The body is not valid protobuf. Something other than an Otherlode agent sent it, or a proxy changed the body. |
| `invalid delta batch: resource.service_name is empty` | `rejecting invalid delta batch` | The body decoded, but the agent's identity is missing or unusable. |
| `failed to read body` | none | The collector could not read the body to the end, for example because the client disconnected. |

The collector says `manifest` or `static baseline` in place of `delta batch` for the other two paths. The text after `invalid` is one of these.

| Detail | Cause and fix |
|---|---|
| `missing resource` | The payload has no identity block. Something other than an Otherlode agent sent it. |
| `resource.service_name is empty` | The agent has no service name. Set it in the agent. The collector does not invent one. |
| `resource.service_name is ".", which no URL path can name` | The name is `.` or `..`. Rename the service. The same rule applies to `resource.service_namespace`. |
| `resource.service_instance_id is empty` or `resource.run_id is empty` | The agent sent no instance or run identity. Run a supported agent. |
| `scanned_at is not set`, `chunk_count must be at least 1`, `chunk_index is negative` or `chunk_index is out of range for chunk_count` | A static baseline chunk is malformed. Something other than an Otherlode agent sent it. |

The agent's [configuration](/docs/agent/configuration) lists its service name and namespace options.

### 503

A 503 means the collector did not take a valid payload. The agent keeps its counts and builds the payload again on its next flush, so nothing is lost. Three causes look alike, and the log tells them apart.

| Sign | Cause and fix |
|---|---|
| `ERROR` line `sink rejected delta batch` with `error` `forward: shard queue is full (service "checkout" instance "3f1c9a")`, `Retry-After: 5`, empty response body | The queue for that instance is full, by count or by bytes, because Otherlode is slow or refusing. Check the `WARN` lines in the next section. If the backend is healthy, raise `OTHERLODE_COLLECTOR_FORWARD_QUEUE_SIZE`, `OTHERLODE_COLLECTOR_FORWARD_QUEUE_BYTES` or `OTHERLODE_COLLECTOR_FORWARD_SHARDS`. [Forwarding](forwarding) explains the queues. |
| `ERROR` line `sink rejected delta batch` with `error` `forward: sink is shutting down (service "checkout" instance "3f1c9a")`, `Retry-After: 5` | The collector is stopping, for example during a rollout. This is expected and clears when the new collector is up. |
| `WARN` line `refusing delta batch: no decode slot came free`, `Retry-After: 1`, empty body | No decode slot came free within 5 seconds. Too many large requests arrived at once. The collector logs at most one such line a minute, and its `unlogged_busy_refusals` field counts the refusals since the last line that got no line of their own. The counter `otherlode_collector_ingest_rejected_total{reason="busy"}` counts every one. Raise `OTHERLODE_COLLECTOR_MAX_CONCURRENT_DECODES` or give the collector more CPU and memory. [Rate and memory limits](limits) has the trade-off. |

The `ERROR` and `WARN` lines name `manifest` or `static baseline` for the other payloads. The counter `otherlode_collector_forward_refused_total`, labelled `queue_full` or `shutting_down`, counts the first two causes.

## Payloads are dropped after forwarding

The collector answers the agent with a 202 before it contacts Otherlode, then delivers each payload in the background. When it gives up on a payload, it logs a `WARN` line and counts it in `otherlode_collector_forward_dropped_total`.

```text
{"time":"2026-10-10T09:12:44.512Z","level":"WARN","msg":"dropping payload: permanent failure","namespace":"","service":"checkout","instance":"3f1c9a","path":"/v1/otherlode/deltas","error":"backend returned 401 Unauthorized"}
```

The `service`, `instance` and `path` fields say which payload. The `msg` gives the reason. When Otherlode's response body names an error code, the line adds it as `backend_error`, such as `"backend_error":"redaction_required"`. The code is cut to 64 bytes, and each byte outside printable ASCII becomes `?`.

| `msg` | Meaning and fix |
|---|---|
| `dropping payload: permanent failure` | Otherlode answered with a status the collector does not retry, or the request could not be built. The `error` field ends with the status. Every status except 429, 502, 503 and 504 lands here, including a redirect, because the collector follows none. Fix what the status means. |
| `dropping payload: retry budget exhausted` | The collector retried for its budget, 5 minutes by default, and every attempt failed. The `error` field holds the last failure, a status or a network error such as a timeout. Check the route from the collector to Otherlode, including `HTTPS_PROXY` and `NO_PROXY`. |
| `dropping payload: shutdown deadline exceeded` | The collector stopped and could not deliver everything within its 10 second drain. No `error` field. Give the container a stop timeout longer than 10 seconds. |
| `dropping payload: shutdown drain attempt failed` | The final attempt during shutdown failed. Read the `error` field. |
| `dropping payload: marshal failed` | The collector could not re-encode the payload. This should not happen. Keep the line and report it. |

The agent already had its 202 for a dropped payload, so it does not send that payload again. A probe in a dropped delta batch reaches Otherlode again only when its count changes. [How the collector works](how-it-works) explains why. [Forwarding](forwarding) covers the retry rules and the tuning variables. For what Otherlode's ingest answers and why, see the server's [dropped payload entries](/docs/server/troubleshooting#the-collector-logs-a-dropped-payload).

One more `WARN` shows at startup and is not a drop.

```text
{"time":"...","level":"WARN","msg":"forward URL uses plain http; the backend auth token is sent unencrypted","url":"http://..."}
```

The forward URL starts with `http://` and a key is set. Use `https://`.

### 403 with redaction_required or redaction_secret_mismatch

```text
{"time":"...","level":"WARN","msg":"dropping payload: permanent failure","namespace":"","service":"checkout","instance":"3f1c9a","path":"/v1/otherlode/deltas","error":"backend returned 403 Forbidden","backend_error":"redaction_required"}
```

The tenant has Otherlode require redaction, and this collector's payloads do not match. Otherlode reads the `Otherlode-Redaction` header that a collector sends while redaction is on.

- `redaction_required` means the payload had no header. This collector has redaction off. Turn it on and give it the tenant's secret.
- `redaction_secret_mismatch` means the header holds another fingerprint. This collector's secret differs from the tenant's. Compare the `secret_fingerprint` in its startup line with another collector's, and set the same secret.

Every payload from such a collector is dropped until you fix it and restart it. [Let Otherlode refuse payloads that were not blanked](redaction#let-otherlode-refuse-payloads-that-were-not-blanked) covers the header. Any other `403` comes from the key. See the server's [backend returned 403](/docs/server/troubleshooting#backend-returned-403).

## The collector warns that it dropped fields it does not know

```text
{"time":"...","level":"WARN","msg":"dropped fields this collector does not know; its bindings are older than the agent's, so the server withholds findings from this run; upgrade to the latest collector","namespace":"...","service":"...","instance":"...","run":"...","agent_version":"...","payload":"..."}
```

It appears once for each run, and only while blanking is on. The agent sent a field that this collector release predates. While blanking is on, the collector removes fields it does not know and marks the run with `fields_stripped`. The line says the server then withholds findings for that run.

Run the latest collector image. Pull the new tag and restart. [Blank out string literals](redaction) explains the stripping and the mark. The counter `otherlode_collector_fields_stripped_total` counts affected payloads.

## Branch history starts over or splits in two

Otherlode shows every branch and site of a service as new, or keeps two histories for one branch. While redaction is on, the collector replaces each branch key and site key with an HMAC under its secret. A key changes when the secret changes.

- Every branch looks new after you turned redaction on or changed the secret. This is expected. The history under the old keys stops, and no setting carries it over.
- One branch has two histories, or the history changes between environments. Two collectors of the tenant use different secrets, or one has redaction off. Compare the `secret_fingerprint` in each collector's startup line. Give every collector the same secret, including the one your test runs report through.

[Replace branch and site keys](redaction#replace-branch-and-site-keys) explains why.

## A token rotation has not taken effect

The collector re-reads a token file every 30 seconds. A change logs an `INFO` line, and the next request uses the new value.

```text
{"time":"2026-10-10T09:30:02.1Z","level":"INFO","msg":"token file changed","file":"auth","tokens":2}
```

The `file` field is `auth` for the agent token file and `forward` for the key file. The count is how many tokens it now holds, never the tokens.

If the read fails, the collector keeps the last good value and logs a `WARN` line every 30 seconds until it recovers.

```text
{"time":"2026-10-10T09:30:32.1Z","level":"WARN","msg":"token file re-read failed; keeping the last good value","file":"auth","error":"/run/secrets/agent-tokens: no token found"}
```

Each failure adds to `otherlode_collector_token_reload_failures_total{file="auth"}` or `{file="forward"}`. The `error` field uses the same texts as the startup table. Typical causes are a file replaced with an empty one, a forward key file with two keys, and a key with a control character. When the file is good again, the collector logs `token file re-read recovered`.

If neither line appears after a minute, the collector read the same content again. Check these in order.

- The token came from `OTHERLODE_COLLECTOR_AUTH_TOKEN` or `OTHERLODE_COLLECTOR_FORWARD_AUTH_TOKEN`. A variable is read once at startup, so restart the collector.
- The file is a Kubernetes secret mounted with `subPath`, which never updates, or a single bind-mounted file that was replaced by a rename. Mount the directory instead.
- The new content is the old content.

[Set and rotate tokens](tokens) describes how each kind of mount behaves and the order to rotate in.

## All agents share one rate limit behind a proxy

Behind a reverse proxy or load balancer, every request arrives from the proxy's address. The collector gives that address one bucket, so agents that never exceed the limit alone get `429` together.

Set `OTHERLODE_COLLECTOR_CLIENT_IP_HEADER` to the header your proxy writes, such as `X-Forwarded-For`. The collector reads the last entry of that header. Set it only when the collector is reachable solely through that proxy, since a client that reaches the collector directly could pick its own bucket. [Rate and memory limits](limits) covers the header, the IPv6 prefix and the tracked-client cap.

## The healthcheck fails

The image runs `otherlode-collector healthcheck` every 10 seconds, with a 3 second timeout, a 10 second start period and 3 retries. It sends one `GET /healthz` and waits up to 2 seconds. It exits 0 on a 200, 1 on any failure and 2 on an unknown argument. It prints the failure to standard error, so the text shows in `docker inspect`.

| Output | Cause and fix |
|---|---|
| `healthz: Get "http://127.0.0.1:4319/healthz": dial tcp 127.0.0.1:4319: connect: connection refused` | Nothing listens on that address. The collector is down or still starting, or it failed to start. Read its log. |
| `healthz: 503 Service Unavailable` | Another service answered on that port. The collector itself always answers `/healthz` with a 200. |
| `healthz url: address bad: missing port in address` | `OTHERLODE_COLLECTOR_ADDR` has no port in the environment where the probe runs. |
| `usage: otherlode-collector [healthcheck]` | The probe command has an extra or misspelled argument. |

The probe reads `OTHERLODE_COLLECTOR_ADDR` and checks that address. An empty or unspecified host, such as `:4319` or `0.0.0.0:4319`, is probed at `127.0.0.1`. A probe that runs where the variable is unset checks `:4319`, so set it for the probe as well as for the collector. The probe never uses a proxy, so `HTTPS_PROXY` cannot capture it. [Health checks, metrics and logs](monitoring) covers `/healthz`.

## Related pages

- [Deploy the collector](deploy) for the first start and the listen address.
- [Configuration](configuration) for every setting and what an invalid value does.
- [Set and rotate tokens](tokens) for token lists, files and mounts.
- [Blank out string literals](redaction) for the redaction settings, the secret and the header.
- [Forwarding](forwarding) for queues, retries and shutdown.
- [Rate and memory limits](limits) for the limits behind 413, 429 and the busy 503.
- [Health checks, metrics and logs](monitoring) for the counters and what to alert on.
- [Troubleshooting the agent](/docs/agent/troubleshooting) and [troubleshooting the server](/docs/server/troubleshooting) for the other two ends.
