---
title: Rate and memory limits
description: The per-address rate limit, the client IP header, the IPv6 prefix, the 16 MiB body limit, the decode cap, and how to bound the collector's worst-case memory.
order: 70
---

The collector limits how fast one client address can send, how large a request can be, and how many requests it decodes at once. This page lists each limit, what the collector answers when a request hits it, and how to bound memory. For the settings in one table, see [the configuration](configuration).

## Rate limit

The collector keeps a token bucket for each client address. A request takes one token. The bucket holds `OTHERLODE_COLLECTOR_RATE_LIMIT_BURST` tokens and refills at `OTHERLODE_COLLECTOR_RATE_LIMIT_RPS` tokens per second. A new bucket starts full.

| Variable | Default | Accepted values |
|---|---|---|
| `OTHERLODE_COLLECTOR_RATE_LIMIT_RPS` | `5` | A number of 0 or more. `0` turns the limit off. |
| `OTHERLODE_COLLECTOR_RATE_LIMIT_BURST` | `20` | An integer of 1 or more when the rate is above 0. |
| `OTHERLODE_COLLECTOR_RATE_LIMIT_IPV6_PREFIX` | `128` | An integer from 1 to 128. |
| `OTHERLODE_COLLECTOR_CLIENT_IP_HEADER` | unset | A header name. |

A value that is not a number, a negative or non-finite rate, a burst below 1 while the rate is above 0, or a prefix outside 1 to 128 stops the collector at startup. The error names the variable. A burst must parse as an integer even when the rate is `0`. With the limit off, the collector logs this warning at startup and ignores the other three variables:

```text
{"time":"...","level":"WARN","msg":"rate limiting disabled; ingest endpoints accept requests unthrottled"}
```

The limit covers every path under `/v1/otherlode/`. That includes the three ingest paths, wrong methods, and unknown paths. `/healthz` and `/metrics` are never limited.

The limit runs before the token check. A request over the limit gets a 429 and never reaches authentication, so a flood is capped whether or not it carries a valid token. A request with a wrong agent token still counts against its address's bucket.

### What a limited request gets

A request over the limit gets status 429, the body `rate limit exceeded`, and a `Retry-After` header. The header holds the seconds until the bucket has a token again, rounded up to a whole second, and never less than 1. At the default rate it is always `1`. A refused request does not use a token. The collector counts each one in `otherlode_collector_rate_limited_total`.

The defaults leave room for the agent's schedule. An agent flushes every 60 seconds by default, so many instances behind one address stay far below 5 requests per second.

### Startup requests

An instance sends its manifest and its static baseline chunks back to back at startup. Several instances that start together behind one address can use up the burst of 20. The agent retries a 429 a few times within one send. If the chunk is still refused, the agent keeps it and sends the rest later, one chunk per flush, so a 429 does not lose the scan. See [after a failed send](/docs/agent/data-sent#after-a-failed-send) and [retries inside one send](/docs/agent/data-sent#retries-inside-one-send). If many instances share an address and you see 429s, raise `OTHERLODE_COLLECTOR_RATE_LIMIT_BURST`.

### Which address counts

By default the collector keys on the connection's remote address with the port removed. Behind a reverse proxy or load balancer, every request then comes from the proxy and all agents share one bucket.

To key on the real client address, set `OTHERLODE_COLLECTOR_CLIENT_IP_HEADER` to the header your proxy writes it into.

```bash
OTHERLODE_COLLECTOR_CLIENT_IP_HEADER=X-Forwarded-For
```

The collector reads the header like this.

- It joins all lines of the header, so a proxy that adds its own line instead of appending is handled.
- It takes the last non-empty comma-separated entry, which is the one written by the proxy directly in front of the collector.
- It removes a port from that entry.
- If the header is absent or empty, it uses the remote address.
- If the entry is not an IP address, it uses the entry text as the key.

The collector trusts the header as written. Set it only when the collector cannot be reached except through that proxy, and the proxy overwrites or appends to the header. A client that reaches the collector directly can send any value and pick its own bucket.

### Address forms

An IPv4 address and an IPv4-mapped IPv6 address both key on the IPv4 address.

By default each IPv6 address has its own bucket. One sender can cycle through the 2^64 addresses of a `/64`. If the collector faces the internet directly, set `OTHERLODE_COLLECTOR_RATE_LIMIT_IPV6_PREFIX=64` so all addresses in one `/64` share a bucket. Keep the default when many agents share a `/64`, as VPC subnets and per-node pod ranges often do.

### Tracked clients

The collector tracks at most 100,000 client addresses. This number is fixed. When the table is full, a request from a new address evicts the least recently used one. The evicted address loses only its bucket and gets a full one on its next request. A flood of new addresses pushes out idle clients first and never locks out a new one.

Every 10 minutes the collector also drops addresses that sent nothing for 10 minutes.

## Request size and content

The collector reads the whole body before it decodes anything. These checks run first.

| Condition | Status | Metric reason |
|---|---|---|
| `Content-Type` is not `application/x-protobuf`. Parameters such as `charset` are allowed. | 415 | `content_type` |
| `Content-Encoding` names anything but `identity`. The collector does not decompress. | 415 | `content_type` |
| Body is larger than 16 MiB. | 413 | `too_large` |
| The body cannot be read to the end. | 400 | `read` |

The 16 MiB limit is fixed. It protects the collector's memory from a bad or hostile sender. The agent does not retry a 413 within a send. See [after a failed send](/docs/agent/data-sent#after-a-failed-send) for what it does next.

The metric is `otherlode_collector_ingest_rejected_total` with the labels `payload` and `reason`.

### Server timeouts

The timeouts are fixed.

| Timeout | Value |
|---|---|
| Read the request headers | 5 seconds |
| Read the whole request, body included | 10 seconds |
| Write the response, counted from the end of the headers | 10 seconds |
| Idle connection | 60 seconds |

A 16 MiB body has to arrive within the 10-second read timeout. A client on a slow link can fail here before it hits any other limit.

## Decode cap

Decoding a payload can take far more memory than the body. A 16 MiB body of empty messages decodes to gigabytes. The decoded message also stays in memory until the collector hands it on.

`OTHERLODE_COLLECTOR_MAX_CONCURRENT_DECODES` caps how many requests the collector decodes at one time. It defaults to the number of CPUs the Go runtime uses, because decoding is CPU-bound and more parallel decodes add memory but no speed. The value must be an integer above 0. Anything else stops the collector at startup. You cannot turn the cap off.

A request reads its body before it asks for a slot. A slow client therefore holds no slot while it uploads. A request that finds no free slot waits up to 5 seconds, then gets a 503 with `Retry-After: 1`. The collector counts it under reason `busy`. It also logs a warning, at most one a minute:

```text
{"time":"...","level":"WARN","msg":"refusing delta batch: no decode slot came free","wait":"5s","max_concurrent_decodes":4,"unlogged_busy_refusals":17}
```

`unlogged_busy_refusals` counts the refusals since the previous warning that got no line of their own. The counter counts every refusal. If the client disconnects while it waits, the collector writes no response and counts reason `canceled`. The agent retries a 503 within the send and again on the next flush.

The cap does not limit how many requests wait. Each waiting request holds its body, up to 16 MiB.

## Worst-case memory

Three parts add up.

- **Decoding.** The decode cap times the memory of the largest decode.
- **Waiting bodies.** The number of requests waiting for a slot, times 16 MiB each.
- **Forwarding queues.** The number of shards times the larger of the queue byte limit and the largest payload.

Each forwarding shard holds at most `OTHERLODE_COLLECTOR_FORWARD_QUEUE_BYTES` of payloads, counted from the moment a payload is queued until delivery ends. The defaults are 8 shards and 64 MiB, so queues take up to 512 MiB. A shard that holds nothing accepts any payload, so one payload above the byte limit can still pass through an idle shard. See [forwarding](forwarding) for the shard and queue settings.

The decode and waiting-body terms depend on traffic and payload content. Two steps keep them in bounds.

1. Set `GOMEMLIMIT`, which the Go runtime reads, and set a container memory limit above it.
2. Keep authentication on. Without a token check, anyone who can reach the collector can send large bodies and fill the waiting-body term.

The rate limit also caps how fast one address can send, so keep it on unless a proxy in front already limits requests.

The code gives no rule for how many collectors to run or what values to use. Start from the defaults and watch `otherlode_collector_ingest_rejected_total` for the `busy` reason and `otherlode_collector_rate_limited_total`. See [health checks, metrics and logs](monitoring).

## Related pages

- [Configuration](configuration) lists every setting.
- [Forwarding](forwarding) covers the queues that the memory estimate includes.
- [Troubleshooting](troubleshooting) covers agents that get 413, 415, 429 or 503.
