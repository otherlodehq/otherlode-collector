---
title: Troubleshooting
description: Find the cause of a collector that will not start, an agent that gets a 401, 404, 405, 413, 415, 429, 400 or 503, a payload that Otherlode did not store, a 403 for redaction, a payload that Otherlode stores but does not track, branch history that starts over, the stripped-fields warning, a token rotation that did not apply, a shared rate limit or a failing healthcheck.
order: 110
---

Every entry starts from something you see, either a log line or an HTTP status. Each gives the text as the collector writes it, the cause, and the fix. Each fix links the page that covers the detail.

## Read the collector's log

The collector writes one JSON object per line to standard output. Read it with `docker logs`, `kubectl logs` or your log shipper. The `msg` field holds the message and the `level` field holds `INFO`, `WARN` or `ERROR`.

```text
{"time":"2026-10-10T16:31:59.20978+01:00","level":"ERROR","msg":"OTHERLODE_COLLECTOR_AUTH_TOKEN and OTHERLODE_COLLECTOR_AUTH_TOKEN_FILE are both set; set one"}
```

The default level is `info`, and this page quotes only what you see at that level. To see more, set `OTHERLODE_COLLECTOR_LOG_LEVEL` to `debug`, `info`, `warn` or `error`, in any letter case, and restart the collector. [Health checks, metrics and logs](monitoring) covers the log format and the counters that this page refers to.

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

A token file that the collector cannot read stops startup even with the opt-out set, because you asked for that file.

### The forward URL is not usable

When you set `OTHERLODE_COLLECTOR_FORWARD_URL`, it must be an `http` or `https` URL with a host and no user info, query or fragment. The collector removes a trailing slash and keeps a path. Each bad value has its own line.

| Log line | Fix |
|---|---|
| `OTHERLODE_COLLECTOR_FORWARD_URL: forward URL "collector.example.com": scheme must be http or https` | Add the scheme. The line quotes the value you set, unless it holds an `@`. |
| `OTHERLODE_COLLECTOR_FORWARD_URL: forward URL "https://": missing host` | Add the host. |
| `OTHERLODE_COLLECTOR_FORWARD_URL: forward URL must not contain user info` | Remove `user:password@`. Use the forward key instead. |
| `OTHERLODE_COLLECTOR_FORWARD_URL: forward URL: cannot parse` | The value holds an `@` and is not a URL the collector can read. Remove any `user:password@` and use the forward key instead. |
| `OTHERLODE_COLLECTOR_FORWARD_URL: forward URL must not contain a query` | Remove everything from `?`. |
| `OTHERLODE_COLLECTOR_FORWARD_URL: forward URL must not contain a fragment` | Remove everything from `#`. |

The URL to use for Otherlode is on Otherlode's [connect page](/docs/server/connect). [Forwarding](forwarding) describes what the collector does with a payload once it has processed it.

### The forward key settings are wrong

| Log line | Cause and fix |
|---|---|
| `OTHERLODE_COLLECTOR_FORWARD_AUTH_TOKEN or OTHERLODE_COLLECTOR_FORWARD_AUTH_TOKEN_FILE is set but OTHERLODE_COLLECTOR_FORWARD_URL is empty; set OTHERLODE_COLLECTOR_FORWARD_URL or unset the key variable` | You set a key but no URL. Set the URL, or unset the key to run log-only. |
| `OTHERLODE_COLLECTOR_FORWARD_AUTH_TOKEN and OTHERLODE_COLLECTOR_FORWARD_AUTH_TOKEN_FILE are both set; set one` | Unset one of the two. |
| `OTHERLODE_COLLECTOR_FORWARD_AUTH_TOKEN holds only spaces` | The variable is set but blank. Set the key or unset the variable. |
| `OTHERLODE_COLLECTOR_FORWARD_AUTH_TOKEN: the key holds a control character at byte 12` | The key has a tab, newline or other control character inside it. The collector trims spaces, tabs and newlines at each end, so the character sits between other characters of the key. Copy the key again. A key file with such a character gives the same message after the file path. |
| `OTHERLODE_COLLECTOR_FORWARD_AUTH_TOKEN_FILE: /run/secrets/forward-key: found 2 tokens, want exactly one` | The key file must hold exactly one key. Remove the extra lines. |

The key file can also fail with the first three file errors in the token table above, under the name `OTHERLODE_COLLECTOR_FORWARD_AUTH_TOKEN_FILE`. An empty key file gives `found 0 tokens, want exactly one`. [Set and rotate tokens](tokens) describes the key and its file.

### A number, a duration or a switch does not parse

A value that does not parse, or a number that is zero or negative where the setting needs a positive one, gives a line that starts with the variable name.

| Log line | Cause and fix |
|---|---|
| `OTHERLODE_COLLECTOR_MAX_CONCURRENT_FORWARDS must be positive` | The same line applies to `OTHERLODE_COLLECTOR_MAX_CONCURRENT_DECODES`, and to `OTHERLODE_COLLECTOR_FORWARD_REQUEST_TIMEOUT` at `0s` or below. Use a value above 0. |
| `OTHERLODE_COLLECTOR_FORWARD_REQUEST_TIMEOUT: time: invalid duration "abc"` | Use Go syntax such as `5s` or `8s`. |
| `OTHERLODE_COLLECTOR_FORWARD_REQUEST_TIMEOUT is 9s, which is not below the 9s time budget of one request; set it lower` | A request has 9 seconds in all, so the forward timeout must be below that. Use `8s`, the default, or less. The line quotes the value you set. `10s` gives the same line. |
| `OTHERLODE_COLLECTOR_MAX_CONCURRENT_FORWARDS: strconv.Atoi: parsing "many": invalid syntax` | Use digits only. |
| `OTHERLODE_COLLECTOR_RATE_LIMIT_RPS: strconv.ParseFloat: parsing "fast": invalid syntax` | Use a number. `0` turns the limit off. |
| `OTHERLODE_COLLECTOR_RATE_LIMIT_RPS must not be negative` | Use 0 or more. Infinity and NaN give `must be a finite number`. |
| `OTHERLODE_COLLECTOR_RATE_LIMIT_BURST must be positive when rate limiting is enabled` | Use 1 or more, or set the rate to 0. |
| `OTHERLODE_COLLECTOR_RATE_LIMIT_IPV6_PREFIX must be from 1 to 128` | Use a whole number from 1 to 128. |
| `OTHERLODE_COLLECTOR_LOG_LEVEL: slog: level string "loud": unknown name` | Use `debug`, `info`, `warn` or `error`, in any letter case, with an optional offset such as `warn+1`. `warning` is not a level. |

[Rate and memory limits](limits) and [forwarding](forwarding) explain what each of these settings does, and [health checks, metrics and logs](monitoring) covers the log level.

### The environment, namespace or redaction settings are wrong

| Log line | Cause and fix |
|---|---|
| `OTHERLODE_COLLECTOR_ENVIRONMENT_ACTION is set but OTHERLODE_COLLECTOR_ENVIRONMENT is empty; set OTHERLODE_COLLECTOR_ENVIRONMENT or unset OTHERLODE_COLLECTOR_ENVIRONMENT_ACTION` | You set an action but no value. Set the value or unset the action. The namespace pair gives the same line with `SERVICE_NAMESPACE`. |
| `OTHERLODE_COLLECTOR_ENVIRONMENT_ACTION: invalid action "replace": must be "insert" or "upsert"` | Use `insert` or `upsert`, in any letter case. The namespace action gives the same line under its own name. |
| `OTHERLODE_COLLECTOR_SERVICE_NAMESPACE is "..", which no URL path can name` | The collector refuses `.` and `..`. Pick another namespace. |
| `OTHERLODE_COLLECTOR_ENVIRONMENT is not valid UTF-8` | The namespace gives the same line under its own name. Re-enter the value as UTF-8. |
| `OTHERLODE_COLLECTOR_ENVIRONMENT is 2000 bytes, over the 1024-byte limit` | The namespace gives the same line under its own name. Use a value of at most 1024 bytes. |
| `OTHERLODE_COLLECTOR_REDACT_BLOCKED_VALUES line 2: error parsing regexp: missing closing ): ` followed by the pattern | The line number counts from 1. Fix that pattern. Each line is one regular expression. |
| `OTHERLODE_COLLECTOR_REDACT_ALL_LITERALS: strconv.ParseBool: parsing "maybe": invalid syntax` | Use `true` or `false`. A typo stops startup, so it cannot leave blanking off. |

[Set the environment and namespace](environment-and-namespace) and [blank out string literals](redaction) cover these settings.

### The redaction secret is missing or unusable

While blanking is on, the collector needs a secret in `OTHERLODE_COLLECTOR_REDACT_SECRET` or `OTHERLODE_COLLECTOR_REDACT_SECRET_FILE`. No line quotes the secret.

| Log line | Cause and fix |
|---|---|
| `redaction is on but neither OTHERLODE_COLLECTOR_REDACT_SECRET nor OTHERLODE_COLLECTOR_REDACT_SECRET_FILE is set; refusing to start, since plain branch and site keys let anyone who holds them test guesses at a redacted literal` | Set one of the two. Use the secret the tenant's other collectors use. If this is the tenant's first collector, make one with `openssl rand -hex 32`. |
| `OTHERLODE_COLLECTOR_REDACT_SECRET and OTHERLODE_COLLECTOR_REDACT_SECRET_FILE are both set; set one` | Unset one of the two. |
| `OTHERLODE_COLLECTOR_REDACT_SECRET: the secret is 5 bytes, want at least 32` | Use a secret of at least 32 bytes. |
| `OTHERLODE_COLLECTOR_REDACT_SECRET: the secret holds a control character at byte 16` | Remove the tab, newline or other control character. |
| `OTHERLODE_COLLECTOR_REDACT_SECRET holds only spaces` | Set the secret or unset the variable. |
| `OTHERLODE_COLLECTOR_AUTH_TOKEN: the list holds the redaction secret, which equals an agent auth token that every agent holds; use a secret of its own` | Use a separate secret. |
| `OTHERLODE_COLLECTOR_AUTH_TOKEN_FILE: /run/secrets/collector-tokens: the file holds the redaction secret, which equals an agent auth token that every agent holds; use a secret of its own` | Use a separate secret. |
| `OTHERLODE_COLLECTOR_FORWARD_AUTH_TOKEN: the value is the redaction secret, which equals the forward key that the backend holds; use a secret of its own` | Use a separate secret. |
| `OTHERLODE_COLLECTOR_FORWARD_AUTH_TOKEN_FILE: /run/secrets/forward-key: the file holds the redaction secret, which equals the forward key that the backend holds; use a secret of its own` | Use a separate secret. |
| `OTHERLODE_COLLECTOR_REDACT_SECRET or OTHERLODE_COLLECTOR_REDACT_SECRET_FILE is set but redaction is off; set OTHERLODE_COLLECTOR_REDACT_BLOCKED_VALUES or OTHERLODE_COLLECTOR_REDACT_ALL_LITERALS, or unset the secret variable` | Turn blanking on, or unset the secret variable. `OTHERLODE_COLLECTOR_REDACT_ALL_LITERALS=false` counts as off. |

A secret file gives the same `OTHERLODE_COLLECTOR_REDACT_SECRET` lines under `OTHERLODE_COLLECTOR_REDACT_SECRET_FILE`, plus the first three file errors in the token table above. A file with no secret gives `found 0 tokens, want exactly one`, and a file with two gives `found 2 tokens, want exactly one`. [Replace branch and site keys](redaction#replace-branch-and-site-keys) has the rules.

### The collector cannot listen

```text
{"time":"2026-10-10T17:32:27.534316+01:00","level":"ERROR","msg":"OTHERLODE_COLLECTOR_ADDR: listen tcp :4319: bind: address already in use"}
```

The collector opens the port after it has checked every other setting. If it cannot, it logs the cause and exits with code 1. It does not log `otherlode-collector listening`, which appears only once the port is open.

| Log line | Cause and fix |
|---|---|
| `OTHERLODE_COLLECTOR_ADDR: listen tcp :4319: bind: address already in use` | Another process holds the port. Stop it, or set `OTHERLODE_COLLECTOR_ADDR` to a free port. |
| `OTHERLODE_COLLECTOR_ADDR: listen tcp: address bad: missing port in address` | `OTHERLODE_COLLECTOR_ADDR` has no port. Use `host:port` or `:port`. |
| `OTHERLODE_COLLECTOR_ADDR: listen tcp: address 99999: invalid port` | The port is out of range. |

The image runs as `nonroot` and listens on `:4319` by default. [Deploy the collector](deploy) covers the listen address.

## Agents get a 401, 404, 405, 413, 415, 429, 400 or 503

This section covers what the collector answers. For what the agent logs and does about each status, see the agent's [Nothing reaches Otherlode or the collector](/docs/agent/troubleshooting#nothing-reaches-otherlode-or-the-collector).

The collector checks a request in this order. A path outside `/v1/otherlode/` gets a 404 or 405 at once. Inside it, the rate limit comes first, then the token, then the path, method, content type, size and body. So a request with a wrong token to a wrong path gets a 401, not a 404.

The collector writes no log line for a 401, 404, 405, 415 or 429, or for a 413 about body size. It does log a `WARN` or `ERROR` line for a 400, a 503 and a 413 about element count. The counter `otherlode_collector_ingest_rejected_total`, labelled by `payload` and `reason`, counts the rejections that reach the ingest handler. The 401 and the 429 have their own counters, `otherlode_collector_auth_rejected_total` and `otherlode_collector_rate_limited_total`.

### 401

The response carries `WWW-Authenticate: Bearer realm="otherlode-collector"` and the body `unauthorized`. The request had no `Authorization` header, used a scheme other than `Bearer`, or sent a token that is not in the collector's list. The collector does not say which.

1. Compare the token the agent has with the one the collector holds. The agent's is `OTHERLODE_AUTH_TOKEN`.
2. If you just rotated a token, look for `token file changed` in the log. See [A token rotation has not taken effect](#a-token-rotation-has-not-taken-effect).
3. Test from the agent's host with `curl -i -X POST -H "Authorization: Bearer $TOKEN" -H "Content-Type: application/x-protobuf" http://collector:4319/v1/otherlode/deltas`. A 400 with `invalid delta batch: missing resource` means the collector accepted the token.

With `OTHERLODE_COLLECTOR_INSECURE_NO_AUTH=1` the collector never answers 401. [Set and rotate tokens](tokens) covers the list, and the agent's [authToken](/docs/agent/configuration#authtoken) is the other half.

### 404

The body is `404 page not found`. The collector serves exactly four paths for agents, and an agent posts to them under its `exportUrl`.

| Path | Carries |
|---|---|
| `/v1/otherlode/deltas` | Delta batches |
| `/v1/otherlode/manifest` | The probe manifest |
| `/v1/otherlode/static-baseline` | The static baseline |
| `/v1/otherlode/class-listing` | The class listing |

Any other path is a 404, including a trailing slash and any prefix. The usual cause is a path in the agent's `exportUrl` that the collector does not expect. The agent appends the four paths to whatever `exportUrl` holds, so `https://gateway.example.com/otherlode` posts to `/otherlode/v1/otherlode/deltas`. Either strip the prefix at the proxy before the request reaches the collector, or remove the path from `exportUrl`. [The agent's `exportUrl`](/docs/agent/configuration#exporturl) says how it builds the URL.

### 405

The body is `Method Not Allowed`, with an `Allow: POST` header on the ingest paths. The request used another method, such as `GET`, on an ingest path. The agent sends only `POST`. Check any proxy rule that changes the method. `/healthz` and `/metrics` accept only `GET`, so a `POST` there gets a 405 as well.

### 413

The body is `request body too large` or `payload has too many elements`.

`request body too large` means the request body is over 16 MiB. The limit is fixed and no setting changes it. [Rate and memory limits](limits) explains the 16 MiB limit, and the agent's [data sent](/docs/agent/data-sent#chunk-caps) lists the caps that keep its payloads under it.

`payload has too many elements` means the payload holds more elements than the limit for its kind. The collector logs a `WARN` line such as `refusing manifest with too many elements`, with the `limit` and the body size in `bytes`. The counter `otherlode_collector_ingest_rejected_total{reason="too_many_elements"}` counts each one. The limits are fixed. [Rate and memory limits](limits#element-limit) lists them.

If the body text is neither, the 413 comes from a proxy or gateway in front of the collector. Raise its request-size limit to at least 16 MiB.

### 415

The body is `unsupported content type` or `unsupported content encoding`. The ingest paths accept `Content-Type: application/x-protobuf`, with parameters such as a charset allowed. They accept no `Content-Encoding` other than `identity`, because the collector does not decompress. A proxy that compresses request bodies with `gzip` causes `unsupported content encoding`. Turn request compression off for these paths. A client that sends another type, such as a browser or a probe, causes `unsupported content type`.

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

The collector says `manifest`, `static baseline` or `class listing` in place of `delta batch` for the other three paths. The text after `invalid` is one of these.

| Detail | Cause and fix |
|---|---|
| `missing resource` | The payload has no identity block. Something other than an Otherlode agent sent it. |
| `resource.service_name is empty` | The agent has no service name. Set it in the agent. The collector does not invent one. |
| `resource.service_name is ".", which no URL path can name` | The name is `.` or `..`. Rename the service. The same rule applies to `resource.service_namespace`. |
| `resource.service_instance_id is 5000 bytes, over the 1024-byte limit` | An identity value is longer than 1024 bytes. The message names the field, which is one of `service_name`, `service_version`, `service_instance_id`, `environment`, `run_id`, `service_namespace` or `agent_version`. Shorten the value in the agent. The agent makes `run_id` and `agent_version` itself, so a long one means something other than an Otherlode agent sent it. |
| `resource.service_instance_id is empty` or `resource.run_id is empty` | The agent sent no instance or run identity. Run a supported agent. |
| `resource.service_name holds a NUL byte` | An identity value holds a NUL character. The message names the field, as above. The agent skips such a value from its configuration, so something other than an Otherlode agent sent it. |
| `deltas[3].first_seen_at is out of range` | A time in a delta batch or manifest is before 1970 or after the year 9999. The message names the list, the entry and the field. The agent takes these times from the host's clock, so check that the clock is set. |
| `classes_failed_to_initialize[0].reason is not set` | A manifest names a class that failed to initialise and gives no reason. Something other than an Otherlode agent sent it. |
| `dependencies[0].identities is empty` | A manifest lists a dependency with no identity. The agent gives every dependency at least one, so something other than an Otherlode agent sent it. |
| `scanned_at is not set`, `chunk_count must be at least 1`, `chunk_index is negative` or `chunk_index is out of range for chunk_count` | A static baseline chunk is malformed. Something other than an Otherlode agent sent it. |
| `listed_at is not set`, `chunk_count must be at least 1`, `chunk_index is negative` or `chunk_index is out of range for chunk_count` | A class listing chunk is malformed. Something other than an Otherlode agent sent it. |

The agent's [configuration](/docs/agent/configuration) lists its service name and namespace options.

### 503

A 503 means the collector did not take a valid payload. The agent keeps its counts and builds the payload again on its next flush, so nothing is lost. The log line shows which cause applies.

| Sign | Cause and fix |
|---|---|
| `ERROR` line `sink rejected delta batch` with a `reason`, `Retry-After: 5`, empty response body | Otherlode did not store the payload, or the collector could not forward it. The `reason` says why. [Otherlode did not store a payload](#otherlode-did-not-store-a-payload) has one entry for each. |
| `ERROR` line `sink rejected delta batch` with `reason` `shutting_down` | The collector is stopping, for example during a rollout. This is expected and clears when the new collector is up. |
| `WARN` line `refusing delta batch: no decode slot came free`, `Retry-After: 1`, empty body | No decode slot came free within 1 second. Too many large requests arrived at once, or forwards are slow and hold their decode slots. The collector logs at most one such line a minute, and its `unlogged_busy_refusals` field counts the refusals since the last line that got no line of their own. The counter `otherlode_collector_ingest_rejected_total{reason="busy"}` counts every one. If `otherlode_collector_forward_failed_total{reason="timeout"}` rises too, fix the route to Otherlode first. Otherwise raise `OTHERLODE_COLLECTOR_MAX_CONCURRENT_DECODES` or give the collector more CPU and memory. [Rate and memory limits](limits) has the trade-off. |
| `WARN` line `refusing delta batch: the request used its time budget before decoding started`, `Retry-After: 1`, empty body | The body took the whole of the request's 9-second budget to arrive, so none was left to decode it. The cause is a slow upload or a slow network path from the agent. Raising `OTHERLODE_COLLECTOR_MAX_CONCURRENT_DECODES` does not help. The line carries the `budget`. The collector logs at most one such line a minute, and its `unlogged_timeout_refusals` field counts the refusals since the last line. The counter `otherlode_collector_ingest_rejected_total{reason="timeout"}` counts every one. Check the network between the agent and the collector. |

The `ERROR` and `WARN` lines name `manifest`, `static baseline` or `class listing` for the other payloads. The collector counts a request whose client has already disconnected under `canceled` and answers it with the same 503.

## Otherlode did not store a payload

The collector answers `202` only after Otherlode has answered `202`. When Otherlode does not store a payload, the collector logs one line, counts it in `otherlode_collector_forward_failed_total` with a `reason`, and answers the agent without a `202`. The agent keeps the payload's counts and sends them again on its next flush, so nothing is dropped. A service reads as behind until Otherlode stores a payload.

```text
{"time":"...","level":"ERROR","msg":"sink rejected delta batch","error":"backend returned 503 Service Unavailable for service \"demo\" instance \"i1\"","reason":"server_error","unlogged_sink_failures":0}
```

The `error` field names the service and instance, and ends with Otherlode's status or the network error. The collector writes at most one line a minute for each payload type and cause. `unlogged_sink_failures` counts the failures since the previous line that got no line of their own, and the counter counts every one. A `4xx` from Otherlode logs a `WARN` line with `msg` `sink refused delta batch`, the `status` and `unlogged_sink_refusals` instead.

When Otherlode's response body names an error code, the `WARN` line adds it as `backend_error`, such as `"backend_error":"ingest_key_invalid"`. The collector cuts the code to 64 bytes and replaces each byte outside printable ASCII with `?`.

| `reason` | Answer to the agent | Meaning and fix |
|---|---|---|
| `server_error` | `503` | Otherlode answered a `5xx`, or a redirect or another status that is not `2xx`. The `error` field ends with the status. For a `5xx`, check Otherlode's status. For a redirect, point `OTHERLODE_COLLECTOR_FORWARD_URL` at the final URL, since the collector follows no redirect. |
| `timeout` | `503` | Otherlode did not answer within `OTHERLODE_COLLECTOR_FORWARD_REQUEST_TIMEOUT`, 8 seconds by default. The `error` field ends with `context deadline exceeded`. Check the route from the collector to Otherlode, including `HTTPS_PROXY` and `NO_PROXY`. |
| `transport` | `503` | The connection to Otherlode failed, for example on a DNS failure, a refused connection or a TLS error. The `error` field holds the network error. Check the URL and the route. |
| `unexpected_status` | `503` | Otherlode, or something in front of it, answered a `2xx` other than `202`. A line `backend answered 2xx other than 202, so the payload is not counted as stored; check OTHERLODE_COLLECTOR_FORWARD_URL` comes first. The URL points at the wrong service, or a proxy answers for it. |
| `busy` | `503` | `OTHERLODE_COLLECTOR_MAX_CONCURRENT_FORWARDS` forwards were in flight and none finished within the forward timeout. If Otherlode is slow, fix that first. If it answers quickly, raise the limit. |
| `canceled` | `503` | The agent closed its connection, or the shutdown deadline passed, before Otherlode answered. |
| `shutting_down` | `503` | The collector had begun to shut down. |
| `rate_limited` | `429` | Otherlode answered `429`. The agent gets Otherlode's `Retry-After`. |
| `rejected` | Otherlode's status | Otherlode answered another `4xx`. A `401` means Otherlode did not accept the forward key. Check `OTHERLODE_COLLECTOR_FORWARD_AUTH_TOKEN`. A `403` carries the code in `backend_error`. A `413` means Otherlode refused the size. |
| `marshal` or `request` | `400` or `503` | The collector could not encode the payload or build the request. It means a collector bug, so keep the line and report it. |

A `400` or `422` from Otherlode for a static baseline or class listing chunk makes the agent drop the chunks it has left, as [the agent's data sent page](/docs/agent/data-sent#after-a-failed-send) describes. Any other failure keeps them.

If Otherlode stays unreachable, every agent reads as behind in Otherlode, and Otherlode makes no claim about the hits that agent has not delivered. When Otherlode answers again, the next flush of each agent catches up from its own totals. An agent whose process stops during the outage loses what it had not delivered, and reads in Otherlode as one that went silent. For what Otherlode's ingest answers and why, see Otherlode's [refused payload entries](/docs/server/troubleshooting#the-collector-logs-a-refused-payload).

One more `WARN` line shows at startup and is not a failure.

```text
{"time":"...","level":"WARN","msg":"forward URL uses plain http; the backend auth token is sent unencrypted","url":"http://..."}
```

The forward URL starts with `http://` and a key is set. Use `https://`.

### 403 with redaction_required or redaction_secret_mismatch

```text
{"time":"...","level":"WARN","msg":"sink refused delta batch","error":"sink answered Forbidden: backend returned 403 Forbidden for service \"demo\" instance \"i1\"","status":403,"unlogged_sink_refusals":0,"backend_error":"redaction_required"}
```

The tenant has Otherlode require redaction, and this collector's payloads do not match. Otherlode reads the `Otherlode-Redaction` header that a collector sends while blanking is on.

- `redaction_required` means the payload had no header. This collector has blanking off. Turn it on and give it the tenant's secret.
- `redaction_secret_mismatch` means the header holds another fingerprint. This collector's secret differs from the tenant's. Compare the `secret_fingerprint` in its startup line with another collector's, and set the same secret.

Otherlode refuses every payload from such a collector with `403` until you fix it and restart it, and the agent gets that `403` each time. [Let Otherlode refuse payloads that were not blanked](redaction#let-otherlode-refuse-payloads-that-were-not-blanked) covers the header. Any other `403` comes from the key. See Otherlode's [backend returned 403](/docs/server/troubleshooting#backend-returned-403).

## Otherlode stores a payload but does not track it

Otherlode answers a payload with `202`, so the collector counts it as delivered. When Otherlode stores the payload and does not judge it, the response carries an `Otherlode-Notice` header, and the collector logs a `WARN` line for it.

```text
{"time":"2026-10-29T08:28:22.418Z","level":"WARN","msg":"backend stores the service's payloads but does not track the service, because the tenant's plan tracks no more services","notice":"service-over-cap","namespace":"","service":"checkout"}
```

The `notice` field names the notice. The `namespace` and `service` fields say whose payload it was. The collector logs a line at most once an hour for each notice, namespace and service, so a service that stays in this state logs once an hour. It remembers 10,000 notice, namespace and service combinations at a time. Past that, a service it has no room for logs a line for every payload, so the lines repeat rather than stop. It counts every notice in `otherlode_collector_forward_notices_total`, labelled `notice`.

| `notice` | `msg` | Meaning and fix |
|---|---|---|
| `service-over-cap` | `backend stores the service's payloads but does not track the service, because the tenant's plan tracks no more services` | The tenant already has as many tracked services as its plan allows, and this service is not one of them. Otherlode keeps what the service sends and makes no findings for it. The plan and the services it tracks are set in Otherlode, not in the collector, so changing a collector setting does not clear the line. Otherlode's [scope](/docs/server/scope) page says how a service gets tracked. |
| `environment-not-tracked` | `backend stores the environment's payloads but does not track the environment, because the service already watches as many environments as its cap allows` | The service already has as many tracked environments as its cap allows, and this payload's environment is not one of them. Otherlode keeps what that environment sends and makes no findings for it. Otherlode's [scope](/docs/server/scope) page says how an environment gets tracked. |

A notice that this collector does not know logs this line instead:

```text
{"time":"2026-10-29T08:28:43.069Z","level":"WARN","msg":"backend sent a notice this collector does not know","notice":"quota-soon","namespace":"","service":"checkout"}
```

The `notice` field shows the value when it is 1 to 64 lowercase letters and hyphens. Any other value shows as `"notice":"unknown"`. The counter labels both as `unknown`. Run the latest collector image, because a newer Otherlode can send a notice that an older collector has no name for.

None of these lines means Otherlode failed to store a payload. `otherlode_collector_forward_failed_total` does not change. [Health checks, metrics and logs](monitoring) lists the counter.

## The collector warns that it dropped fields it does not know

```text
{"time":"...","level":"WARN","msg":"dropped fields this collector does not know; its bindings are older than the agent's, so the server withholds findings from this run; upgrade to the latest collector","namespace":"...","service":"...","instance":"...","run":"...","agent_version":"...","payload":"..."}
```

It appears once for each run, and only while blanking is on. The agent sent a field that this collector release predates. While blanking is on, the collector removes fields it does not know and marks the run with `fields_stripped`. The line says Otherlode then withholds findings for that run.

Run the latest collector image. Pull the new tag and restart. [Blank out string literals](redaction) explains the stripping and the mark. The counter `otherlode_collector_fields_stripped_total` counts affected payloads.

## Branch history starts over or splits in two

Otherlode shows every branch and site of a service as new, or keeps two histories for one branch. While blanking is on, the collector replaces each branch key and site key with an HMAC under its secret. A key changes when the secret changes.

- Every branch looks new after you turn blanking on or change the secret. This is expected. The history under the old keys stops, and no setting carries it over.
- One branch has two histories, or the history changes between environments. Two collectors of the tenant use different secrets, or one has blanking off. Compare the `secret_fingerprint` in each collector's startup line. Give every collector the same secret, including the one your test runs report through.

[Replace branch and site keys](redaction#replace-branch-and-site-keys) explains why.

## A token rotation has not taken effect

The collector re-reads a token file every 30 seconds. A change logs an `INFO` line, and the next request uses the new value.

```text
{"time":"2026-10-10T09:30:02.1Z","level":"INFO","msg":"token file changed","file":"auth","tokens":2}
```

The `file` field is `auth` for the agent token file and `forward` for the key file. The `tokens` field gives how many tokens the file holds, never the tokens themselves.

If the read fails, the collector keeps the last good value and logs a `WARN` line every 30 seconds until it recovers.

```text
{"time":"2026-10-10T09:30:32.1Z","level":"WARN","msg":"token file re-read failed; keeping the last good value","file":"auth","error":"/run/secrets/agent-tokens: no token found"}
```

Each failure adds to `otherlode_collector_token_reload_failures_total{file="auth"}` or `{file="forward"}`. The `error` field uses the same texts as the startup table. Typical causes are a file replaced with an empty one, a forward key file with two keys, and a key with a control character. When the file is good again, the collector logs `token file re-read recovered`.

If neither line appears after a minute, the collector read the same content again. Check these in order.

- The token came from `OTHERLODE_COLLECTOR_AUTH_TOKEN` or `OTHERLODE_COLLECTOR_FORWARD_AUTH_TOKEN`. The collector reads a variable once at startup, so restart it.
- The file is a Kubernetes secret mounted with `subPath`, which never updates, or a single bind-mounted file that a rename replaced. Mount the directory instead.
- The new content is the old content.

[Set and rotate tokens](tokens) describes how each kind of mount behaves and the order to rotate in.

## All agents share one rate limit behind a proxy

Behind a reverse proxy or load balancer, every request arrives from the proxy's address. The collector gives that address one bucket, so agents that never exceed the limit alone get `429` together.

Set `OTHERLODE_COLLECTOR_CLIENT_IP_HEADER` to the header your proxy writes, such as `X-Forwarded-For`. The collector reads the last entry of that header. Set it only when the collector is reachable solely through that proxy, since a client that reaches the collector directly could pick its own bucket. [Rate and memory limits](limits) covers the header, the IPv6 prefix and the tracked-client cap.

## The healthcheck fails

The image runs `otherlode-collector healthcheck` every 10 seconds, with a 3 second timeout, a 10 second start period and 3 retries. It sends one `GET /healthz` and waits up to 2 seconds. It exits with code 0 on a 200, with code 1 on any failure and with code 2 on an unknown argument. It prints the failure to standard error, so the text shows in `docker inspect`.

| Output | Cause and fix |
|---|---|
| `healthz: Get "http://127.0.0.1:4319/healthz": dial tcp 127.0.0.1:4319: connect: connection refused` | Nothing listens on that address. The collector is down or still starting, or it failed to start. Read its log. |
| `healthz: 503 Service Unavailable` | Another service answered on that port. The collector itself always answers `/healthz` with a 200. |
| `healthz url: address bad: missing port in address` | `OTHERLODE_COLLECTOR_ADDR` has no port in the environment where the probe runs. |
| `usage: otherlode-collector [healthcheck]` | The probe command has an extra or misspelled argument. |

The probe reads `OTHERLODE_COLLECTOR_ADDR` and checks that address. For an empty or unspecified host, such as `:4319` or `0.0.0.0:4319`, the probe checks `127.0.0.1`. A probe that runs where the variable is unset checks `:4319`, so set it for the probe as well as for the collector. The probe never uses a proxy, so `HTTPS_PROXY` cannot capture it. [Health checks, metrics and logs](monitoring) covers `/healthz`.

## Related pages

- [Deploy the collector](deploy) for the first start and the listen address.
- [Configuration](configuration) for every setting and what an invalid value does.
- [Set and rotate tokens](tokens) for token lists, files and mounts.
- [Blank out string literals](redaction) for the redaction settings, the secret and the header.
- [Forwarding](forwarding) for the forward, its time limits and shutdown.
- [Rate and memory limits](limits) for the limits behind 413, 429 and the busy 503.
- [Health checks, metrics and logs](monitoring) for the counters and what to alert on.
- [Troubleshooting the agent](/docs/agent/troubleshooting) and [troubleshooting Otherlode](/docs/server/troubleshooting) for the other two ends.
