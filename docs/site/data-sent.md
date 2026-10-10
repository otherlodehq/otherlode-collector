---
title: What the collector sends
description: What the collector receives, what it checks and rejects, what it changes before it forwards, including re-keyed branch and site keys, the request it sends, what it never adds, what it stores, and what it logs.
order: 90
---

The collector receives the agent's three payloads, checks them, changes a few fields if you ask it to, and forwards each one to the forward URL you set. It stores nothing on disk. With no forward URL set, it sends nothing anywhere and only writes a log line for each payload.

This page lists what leaves the collector and what stays in its log. What each payload contains belongs to the agent. See [what the agent sends](/docs/agent/data-sent).

## What the collector receives

The agent sends three payloads to the collector, each as a `POST` with a protobuf body.

| Path | Payload | Contents |
|---|---|---|
| `/v1/otherlode/deltas` | Delta batch | [Delta batch](/docs/agent/data-sent#delta-batch) |
| `/v1/otherlode/manifest` | Probe manifest | [Probe manifest](/docs/agent/data-sent#probe-manifest) |
| `/v1/otherlode/static-baseline` | Static baseline chunk | [Static baseline chunk](/docs/agent/data-sent#static-baseline-chunk) |

Every payload carries the same resource attributes. See [resource attributes](/docs/agent/data-sent#resource-attributes).

## What the collector checks

The collector checks each request in this order. A request that fails a check gets the status below and nothing is forwarded. Only a payload that passes every check is queued, and the collector answers `202` only then.

| Check | Fails with |
|---|---|
| The client is over its rate limit, when limiting is on. See [rate and memory limits](limits) | `429` |
| The `Authorization` header holds no accepted token, when token checking is on. See [tokens](tokens) | `401` |
| The `Content-Type` is not `application/x-protobuf`, with or without parameters, or a `Content-Encoding` other than `identity` is present | `415` |
| The body is larger than 16 MiB | `413` |
| The body cannot be read | `400` |
| No decode slot comes free within 5 seconds. See [rate and memory limits](limits) | `503` |
| The body is not valid protobuf | `400` |
| The resource is missing, or `service_name` is blank, or `service_instance_id` or `run_id` is empty | `400` |
| `service_name` or `service_namespace` is `.` or `..` | `400` |
| A static baseline chunk has no `scanned_at`, a `chunk_count` below 1, or a `chunk_index` that is negative or not below `chunk_count` | `400` |
| The forwarder cannot take the payload because its queue is full or the collector is shutting down. See [forwarding](forwarding) | `503` |

The checks look only at identity fields and chunk numbers. They do not read the code names, counts or conditions in a payload.

## What the collector changes

The collector changes a payload only when you set a matching option. It changes nothing else. In particular it does not change any count or any name from your code.

### Environment and namespace

If you set `OTHERLODE_COLLECTOR_ENVIRONMENT`, the collector writes that value into `environment` on every delta batch, manifest and static baseline chunk. If you set `OTHERLODE_COLLECTOR_SERVICE_NAMESPACE`, it does the same for `service_namespace`. A value the agent already set is kept by default. It is replaced only when `OTHERLODE_COLLECTOR_ENVIRONMENT_ACTION` or `OTHERLODE_COLLECTOR_SERVICE_NAMESPACE_ACTION` is `upsert`. See [set the environment and namespace](environment-and-namespace).

### Blanked string literals

If you set `OTHERLODE_COLLECTOR_REDACT_BLOCKED_VALUES` or `OTHERLODE_COLLECTOR_REDACT_ALL_LITERALS`, the collector replaces the text of string literals with `…`. It does this in the conditions of branch sites and in the case labels of their outcomes, in manifests and static baseline chunks. Delta batches hold no conditions. The collector blanks no other field. See [blank out string literals](redaction).

### Dropped unknown fields

While either blanking setting is on, the collector also drops every field it does not know from every payload. It cannot see a literal inside a field it does not know. When it drops a field, it sets `fields_stripped` on the payload's resource attributes. The fix is to run the latest collector. See [blank out string literals](redaction).

With both blanking settings off, the collector drops no field and sets `fields_stripped` on nothing. A field it does not know is forwarded as it arrived.

### Delta counts

The collector never changes a count in a delta batch. What the counts mean belongs to the agent. See [delta batch](/docs/agent/data-sent#delta-batch).

### Re-keyed branch and site keys

While either blanking setting is on, the collector replaces every `branch_key` and `site_key` in manifests and static baseline chunks. The new key is HMAC-SHA256 of the agent's key under the secret in `OTHERLODE_COLLECTOR_REDACT_SECRET` or `OTHERLODE_COLLECTOR_REDACT_SECRET_FILE`, cut to 32 lowercase hex characters. A plain key is a digest that includes the site's string literals, so it would let anyone who holds it test a guess at a blanked literal. Equal keys stay equal, so Otherlode still joins a branch across builds and instances. An empty key stays empty.

Every collector of one tenant needs the same secret, or one branch gets two keys and two histories. A new secret, or blanking turned on later, makes Otherlode treat every branch and site as new. See [replace branch and site keys](redaction#replace-branch-and-site-keys).

### Cleared case keys

While either blanking setting is on, the collector also removes the `case_key` from two kinds of case. One is every case of a site that the agent marks `string_hash_code_switch`. The other is a case whose key equals the Java hash code of a literal the collector blanked in the same method. Such a key is the hash code of a string, often one of your literals. Otherlode cannot match a cleared case across builds. See [case keys that are a hash code](redaction#case-keys-that-are-a-hash-code).

With both blanking settings off, the collector keeps every key as the agent sent it.

## What the collector forwards

The collector decodes each payload and encodes it again, so the bytes it sends can differ from the bytes it received. The content is the one that passed the checks, with the changes above. It forwards nothing else from the incoming request.

### The request

Each payload becomes one `POST` to the forward URL followed by the path the agent used, such as `https://example.test/v1/otherlode/deltas`. The collector sets these headers.

| Header | Value |
|---|---|
| `Content-Type` | `application/x-protobuf` |
| `Authorization` | `Bearer <forward key>`, only when you set a forward key |
| `Otherlode-Redaction` | The fingerprint of the redaction secret, 16 lowercase hex characters, only while a blanking setting is on. It is the `secret_fingerprint` value in the startup log, never the secret. See [let Otherlode refuse payloads that were not blanked](redaction#let-otherlode-refuse-payloads-that-were-not-blanked) |

Go's HTTP client, which the collector uses, adds these headers on its own.

| Header | Value |
|---|---|
| `Host` | The host of the forward URL |
| `Content-Length` | The size of the body |
| `User-Agent` | `Go-http-client/1.1`, or `Go-http-client/2.0` when the connection uses HTTP/2 |
| `Accept-Encoding` | `gzip` |

The `User-Agent` does not name the collector or its version. Over `https`, the client uses HTTP/2 when the server offers it. If `HTTPS_PROXY` or `HTTP_PROXY` applies to the forward URL, the request goes through that proxy. See [forwarding](forwarding).

The server at the forward URL also sees the address the connection comes from, as it does for any HTTP request. That is the address of the collector, or of your proxy or NAT gateway.

### What the collector never adds

The collector adds no client IP, no host name and no other data about the machine it runs on. It copies nothing from the incoming request into the forwarded one. That covers the agent's address, the agent's `Authorization` token, the `User-Agent` and every other header the agent sent, including the header named by `OTHERLODE_COLLECTOR_CLIENT_IP_HEADER`. The agent's token is for the collector. The forward key is a separate secret.

## What the collector stores

The collector writes no file. It holds a payload in memory from the moment it arrives until the forward succeeds or the payload is dropped. A crash loses what was queued. A stop signal gives each queued payload one last attempt, within 10 seconds. It reads only the token files you point it at. See [forwarding](forwarding) for the queue and the drop rules.

## What the collector logs

The collector writes JSON lines to standard output. At the default `info` level, it logs a line for each accepted payload only when no forward URL is set. See [when no forward URL is set](#when-no-forward-url-is-set) for those lines. No line holds a literal's text, a key, or any other payload content. A line holds identity fields, counts of list entries, and error text. The startup line for redaction also holds the secret's fingerprint, never the secret.

### Warnings about payloads

| `msg` | Extra fields | When |
|---|---|---|
| `rejecting invalid delta batch`, `rejecting invalid manifest` or `rejecting invalid static baseline` | `error` | A payload failed a check in the table above. The error names the check, such as `resource.run_id is empty`. It quotes the service name or namespace only when it is `.` or `..` |
| `rejecting malformed delta batch`, `rejecting malformed manifest` or `rejecting malformed static baseline` | `error` | The body is not valid protobuf. The error names the decoding problem and holds no body content |
| `dropping payload: ...` | `namespace`, `service`, `instance`, `path`, `error`, and `backend_error` when the backend named an error code | The forwarder gave up on a payload. See [forwarding](forwarding) for the reasons. The error is the backend's status or the HTTP client's error, which can include the forward URL. `backend_error` is the `error` field of the backend's JSON response, cut to 64 bytes |
| `agent environment does not match the collector's configured environment` | `namespace`, `service`, `instance`, `run`, `payload`, `agent_environment`, `collector_environment`, `action` | The agent named another environment than `OTHERLODE_COLLECTOR_ENVIRONMENT`. A `WARN` the first time a service sends that environment, and `DEBUG` after that |
| `agent namespace does not match the collector's configured namespace` | `service`, `instance`, `run`, `payload`, `agent_namespace`, `collector_namespace`, `action` | The agent named another namespace than `OTHERLODE_COLLECTOR_SERVICE_NAMESPACE`. A `WARN` the first time a service sends that namespace, and `DEBUG` after that |
| `refusing delta batch: no decode slot came free`, or the same with `manifest` or `static baseline` | `wait`, `max_concurrent_decodes`, `unlogged_busy_refusals` | A request waited 5 seconds for a decode slot and got `503`. At most one line a minute |
| `dropped fields this collector does not know; its bindings are older than the agent's, so the server withholds findings from this run; upgrade to the latest collector` | `namespace`, `service`, `instance`, `run`, `agent_version`, `payload` | A blanking setting is on and a payload lost an unknown field. One line for each run |
| `forward URL uses plain http; the backend auth token is sent unencrypted` | `url` | At startup, when the forward URL starts with `http://` and a forward key is set |

When the forwarder refuses a payload, the collector also logs a line at `error` level. Its `msg` is `sink rejected delta batch`, `sink rejected manifest` or `sink rejected static baseline`, and its `error` field names the namespace, service and instance. See [health checks, metrics and logs](monitoring) for every level.

### When no forward URL is set

With no `OTHERLODE_COLLECTOR_FORWARD_URL`, nothing leaves the collector. The collector logs this warning at startup.

```text
{"time":"...","level":"WARN","msg":"OTHERLODE_COLLECTOR_FORWARD_URL not set; ingest payloads are only logged, not forwarded"}
```

It then answers `202` to every payload that passes the checks, and logs one `info` line for it. The agent counts that `202` as delivered, so the data is gone once the line is written.

The line for a delta batch has this shape.

```text
{"time":"...","level":"INFO","msg":"received delta batch","namespace":"","service":"checkout","instance":"checkout-7d9f","run":"...","environment":"staging","test_run":false,"agent_version":"...","deltas":412,"endpoint_deltas":18,"dependency_deltas":3}
```

The `msg` is `received delta batch`, `received probe manifest` or `received static baseline`. Every line holds `namespace`, `service`, `instance`, `run`, `environment`, `test_run` and `agent_version`. The rest are sizes.

| `msg` | Size fields |
|---|---|
| `received delta batch` | `deltas`, `endpoint_deltas`, `dependency_deltas`, and `counts_pending_since` when it is not 0 |
| `received probe manifest` | `probes`, `call_edges`, `class_locations`, `skipped_classes`, `failed_classes`, `endpoints`, `disabled_endpoint_modules`, `dependencies`, `referenced_classes`, `class_references`, `external_classes`, `references_recorded`, `dependencies_listed`, and `counts_pending_since` when it is not 0 |
| `received static baseline` | `scanned_at`, `chunk`, `chunk_count`, `declared_classes`, `declared_call_edges`, `declared_referenced_classes`, `unreadable_classes`, `unprobed_classes` |

Environment and namespace stamps, blanking and re-keying apply before the line is written, so the line shows the stamped values.

## Related pages

- [What the agent sends](/docs/agent/data-sent) lists the contents of each payload.
- [Blank out string literals](redaction) covers the blanking settings, the re-keyed keys, the cleared case keys and the dropped unknown fields.
- [Forwarding](forwarding) covers the queue, the retries and the drop rules.
- [Health checks, metrics and logs](monitoring) covers the counters and every log level.
