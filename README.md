# otherlode-collector

The ingest/decode layer for the
[Otherlode agent](https://github.com/otherlodehq/otherlode-agent)'s
OTLP-style push export. Otherlode is a Java agent that instruments a
running JVM to find dead code paths: endpoints, methods, and branches that are
reachable but never actually exercised. Each instrumented instance pushes
periodic batches (hit counts, probe metadata) to a collector over HTTP.
This repo is that collector: it decodes the wire payloads and hands them off
for storage. It doesn't store anything itself.

## Why a separate repo

Storage and multi-tenant aggregation (the part that turns raw hit counts
into "this endpoint has been dead for six months across every instance")
is a closed-source backend, kept out of this repo by design. That mirrors
OpenTelemetry's own collector-vs-backend split: the collector is a thin,
reusable, open piece; what a vendor does with the data downstream isn't.
Decoding OTLP-style protobuf and routing it somewhere is generic enough to
be worth open-sourcing on its own, independent of any particular backend.

## How it fits together

```
Otherlode agent  --POST protobuf-->  otherlode-collector  -->  Sink
(JVM, pushes                         (this repo: decodes,       (real backend:
 every flush)                         processes, forwards)       storage, aggregation)
```

The agent's `HttpExporter` posts four payload types, matching
the paths this collector serves. Each one carries the same resource
attributes: service namespace (optional), service name, version,
instance ID, environment, a run ID, and a flag that marks a test run. A service is known by its
namespace and name together. The agent makes a fresh random run ID for each process, so an
instance restarted under a pinned instance ID still names a different run.

- `POST /v1/otherlode/deltas`, a `DeltaBatch`: resource attributes plus
  per-probe hit totals, counted from the start of the process. A batch
  carries a probe only when its total changed since the last batch the
  collector confirmed. Sent every flush interval even when empty, as a
  liveness heartbeat, so a backend can tell an idle instance from a dead
  one. It also carries endpoint hit totals, one
  entry per HTTP endpoint a web framework has matched a request to.
- `POST /v1/otherlode/manifest`, a `ProbeManifest`: resource attributes
  plus a map from probe IDs to their source location (class, method,
  line, branch index), sent incrementally: each class's entries go out once, so a manifest carries only probes the agent has not sent before.
  It also carries the endpoints a web framework serves and any endpoint
  module that switched itself off after a linkage failure against a
  framework version it does not match.
- `POST /v1/otherlode/static-baseline`, a `StaticBaseline`: an opt-in,
  once-per-process static scan of a service instance's classes, sent as
  one or more chunks. Every chunk carries the same instance and
  `scanned_at`, so `(instance, scanned_at)` identifies one scan; a
  backend must hold every chunk (`chunk_index` of `chunk_count`) before
  it can diff the scan against anything.
- `POST /v1/otherlode/class-listing`: a `ClassListing`, the names of the
  classes a run's build holds, sent once per process as one or more
  chunks. Every chunk carries the same instance, include rules and
  `listed_at`, so a backend must hold every chunk (`chunk_index` of
  `chunk_count`) before it reads a class's absence from the listing.

All four are protobuf over plain HTTP, not gRPC. The agent sends one batch per
flush interval per instance, so gRPC's multiplexing advantage doesn't apply,
and plain HTTP avoids shading grpc-java/Netty into every instrumented JVM.
See the agent's [ADR 0009](https://github.com/otherlodehq/otherlode-agent/blob/master/docs/adr/0009-http-protobuf-not-grpc.md)
for the full reasoning; the schema itself lives in the agent
repo and is published to the Buf Schema Registry as
[`buf.build/otherlode/otherlode`](https://buf.build/otherlode/otherlode).

## Architecture

- `buf.build/gen/go/otherlode/otherlode/protocolbuffers/go`: Go bindings
  for the Otherlode wire schema, generated remotely by the BSR and pulled in
  as an ordinary Go module dependency. The package to import is
  `buf.build/gen/go/otherlode/otherlode/protocolbuffers/go/otherlode/v1`.
  No local `.proto` copy or `protoc` step in this repo.
- `ingest.Handler`: decodes the four payload types above and
  hands each to a `Sink`. Rejects malformed bodies, and decoded ones
  whose resource lacks the service name, instance ID or run ID a backend
  needs to attribute them, with `400` before they reach the sink; accepts
  valid ones with `202`. All four payloads are checked the same way,
  since `class_id` and every cumulative total are only meaningful within
  one run of one instance. A service name that is blank, and a service
  name or namespace that is `.` or `..`, are rejected the same way: a
  service is read at a URL path that holds both, and browsers drop dot
  segments even when they are escaped.
- `ingest.Sink`: the seam a real backend implements. The
  collector has no storage of its own, so this interface is the entire
  contract between "decoded a payload" and "did something with it."
  Two implementations live here: `LogSink`, which only logs what it
  receives and stands in for a backend during development, and
  `forward.ForwardingSink` below. `ingest` and `metrics` are importable
  packages, not `internal`, so a backend written in Go can embed the same
  `Handler` and implement `Sink` itself; the rest of this repo stays
  internal.
- `internal/forward`: `ForwardingSink` relays each decoded payload to a
  backend over the same protobuf-over-HTTP shape the collector accepts,
  the way an OTel Collector exporter re-sends OTLP downstream. It posts
  the payload while the agent's request waits, makes one attempt, and
  returns nil only when the backend answered `202`. A `429` or another
  `4xx` comes back as an `*ingest.StatusError` with the backend's
  status. Anything else is a plain error and the handler answers `503`.
  It queues nothing and retries nothing.
- `internal/processor`: sinks that wrap another sink, changing or
  inspecting a decoded payload before passing it on: the role an OTel
  Collector processor plays. Three exist, `Environment`, `Namespace` and
  `Redaction`.
- `internal/auth`: a shared-secret bearer-token check, wrapped around the
  ingest routes as HTTP middleware. Kept separate from `ingest.Handler` so
  the decode layer stays auth-agnostic.
- `internal/ratelimit`: a per-client-IP token bucket, also wrapped around
  the ingest routes as HTTP middleware, checked before auth so a request
  flood is capped regardless of whether it carries a valid token.
- `metrics`: a handful of counters served at `GET /metrics` in
  the Prometheus text format, with no client library dependency.
- `cmd/otherlode-collector`: a minimal HTTP server wiring the sink into
  the handler and listening on `:4319` (the port in the agent's default
  `exportUrl`, `http://localhost:4319`), overridable via
  `OTHERLODE_COLLECTOR_ADDR`.

## Running it

The collector requires an auth token before it will start (see
[Authentication](#authentication) below), so the minimal way to run it
locally is:

```
OTHERLODE_COLLECTOR_INSECURE_NO_AUTH=1 go run ./cmd/otherlode-collector
```

Listens on `:4319` by default; set `OTHERLODE_COLLECTOR_ADDR` to change
that. Point the Otherlode agent's `exportUrl` at it, or send a
payload by hand. This posts a `DeltaBatch` for service `demo`, instance `i1`, run
`r1`, with no probe deltas (the same shape the agent sends as an idle
heartbeat):

```
printf '\x0a\x0e\x0a\x04demo\x1a\x02i1\x2a\x02r1' | curl -s -o /dev/null -w '%{http_code}\n' \
  -X POST http://localhost:4319/v1/otherlode/deltas \
  -H 'Content-Type: application/x-protobuf' --data-binary @-
```

Expect `202`. Without forwarding configured (see
[Forwarding](#forwarding)), the collector logs each payload it receives
and does nothing else with it.

### Logging

Logs are JSON lines on stdout at `info` and above. Set
`OTHERLODE_COLLECTOR_LOG_LEVEL` to `debug`, `info`, `warn`, or `error` to
change that. Case does not matter. A level can take a signed offset, such
as `warn+1` or `info-2`, to set a threshold between two named levels,
which sit 4 apart. Any other value stops the collector at startup.
Without forwarding configured every payload is logged at `info`, so
`warn` is the quiet setting for a busy collector.

### Authentication

Set `OTHERLODE_COLLECTOR_AUTH_TOKEN` to require a matching
`Authorization: Bearer <token>` header on `/v1/otherlode/deltas`,
`/v1/otherlode/manifest`, `/v1/otherlode/static-baseline`, and
`/v1/otherlode/class-listing`. Point the
agent's exporter at the same token so its requests carry the header.

```
OTHERLODE_COLLECTOR_AUTH_TOKEN=s3cret go run ./cmd/otherlode-collector
```

To rotate the token without refusing any agent, list more than one,
separated by commas. A request passes when its token matches any of
them, so old and new agents both get through while the fleet moves
over. Spaces around each token are trimmed, and an empty entry (`a,,b`
or a trailing comma) stops the collector at startup. A token in this
variable cannot contain a comma.

```
OTHERLODE_COLLECTOR_AUTH_TOKEN=old-token,new-token go run ./cmd/otherlode-collector
```

A token set in an environment variable shows up in process listings and
container inspection. To keep it out of them, put the tokens in a file,
one per line, and name the file in `OTHERLODE_COLLECTOR_AUTH_TOKEN_FILE`
instead. Blank lines and lines starting with `#` are skipped, so a token
in a file cannot start with `#`. Spaces around each line are trimmed.
This is the form a mounted Kubernetes or Docker secret takes. Setting
both `OTHERLODE_COLLECTOR_AUTH_TOKEN` and
`OTHERLODE_COLLECTOR_AUTH_TOKEN_FILE` stops the collector at startup.

```
# agents on the old token until the fleet has rolled
old-token
new-token
```

The collector reads the file again every 30 seconds, so a token added
to or removed from it takes effect without a restart. Kubernetes updates
a mounted secret in place and sends no signal, which is why this is a
timed re-read. If a re-read fails, or finds no token, the collector
keeps the tokens it last read, logs a warning, and counts it in
`otherlode_collector_token_reload_failures_total{file="auth"}`, so a
re-read that finds no token cannot lock every agent out. A re-read can
still catch a file that is half written and apply what it finds until
the next re-read.

How to change the file depends on how it reaches the collector:

- A Kubernetes secret mounted as a volume updates by itself after the
  kubelet next syncs, often a minute or two. A secret mounted with
  `subPath` never updates, so the collector keeps the old tokens.
- A single file bind-mounted into a container (a Compose `secrets:`
  entry with `file:`, or `-v ./tokens:/run/secrets/tokens`) pins the
  file the container saw at start. Edit it in place, or mount its
  directory instead. Renaming a new file over it leaves the container
  reading the old one, with no warning.
- A file the collector reads directly, or one inside a mounted
  directory, is safest to change by writing a new file and renaming it
  over the old one, so a re-read never sees it half written.
- A Docker Swarm secret never changes in place. Rotating one replaces
  the container, which reads the new file at startup.

At startup there is no
earlier value to fall back on: a file that cannot be read or holds no
token stops the collector, even with `OTHERLODE_COLLECTOR_INSECURE_NO_AUTH`
set.

`/healthz` never requires the token, so liveness/readiness probes keep
working unauthenticated.

The collector fails closed: if neither `OTHERLODE_COLLECTOR_AUTH_TOKEN` nor
`OTHERLODE_COLLECTOR_AUTH_TOKEN_FILE` is set, it
refuses to start rather than running unauthenticated. To run without
auth anyway (local development only, never for anything reachable
outside your machine), opt out explicitly:

```
OTHERLODE_COLLECTOR_INSECURE_NO_AUTH=1 go run ./cmd/otherlode-collector
```

`OTHERLODE_COLLECTOR_INSECURE_NO_AUTH` takes `1`, `t`, `T`, `true`, `TRUE`,
`True`, `0`, `f`, `F`, `false`, `FALSE` or `False`. Any other value
stops the collector at startup, even when a token is set.

Or as a container. The same fail-closed rule applies, so the token (or
the explicit opt-out) has to be passed in. Each release publishes an image
for `linux/amd64` and `linux/arm64`, tagged with its version, its
`major.minor` and `latest`:

```
docker run --rm -p 4319:4319 -e OTHERLODE_COLLECTOR_AUTH_TOKEN=s3cret ghcr.io/otherlodehq/otherlode-collector:latest
```

or built from this repository with `docker build -t otherlode-collector .`.
Outside a container, `go install github.com/otherlodehq/otherlode-collector/cmd/otherlode-collector@latest`
installs the binary.

The collector is versioned on its own, apart from the agent, and a newer
collector serves every older agent: run the latest release
([ADR 0006](docs/adr/0006-the-collector-is-versioned-on-its-own.md)).

### TLS

The collector serves plain HTTP only, so it must sit behind a
TLS-terminating reverse proxy or load balancer whenever agents reach it
across a network you do not control. Without one, the agent's bearer
token and every payload travel in the clear, including string literals,
which the redaction processor only sees once they reach the collector.
Point the agent's `exportUrl` at the proxy's `https` URL, and make sure
agents and anything else outside your control can reach the listener
only through the proxy; that is also the condition for setting
`OTHERLODE_COLLECTOR_CLIENT_IP_HEADER` (see below). Probes and scrapers of
`/healthz` and `/metrics` inside your network can still reach it
directly. Likewise, use an `https` `OTHERLODE_COLLECTOR_FORWARD_URL`
whenever the backend is reached across a network you do not control,
since that hop carries `OTHERLODE_COLLECTOR_FORWARD_AUTH_TOKEN`.

### Rate limiting

`/v1/otherlode/deltas`, `/v1/otherlode/manifest`,
`/v1/otherlode/static-baseline`, and `/v1/otherlode/class-listing` are
throttled per client IP: 5
requests/second with a burst of 20 by default, sized around the agent's
flush interval, which is 60 seconds by default. Override with
`OTHERLODE_COLLECTOR_RATE_LIMIT_RPS` and
`OTHERLODE_COLLECTOR_RATE_LIMIT_BURST`, or set the rate to `0` to disable
it.

```
OTHERLODE_COLLECTOR_RATE_LIMIT_RPS=10 OTHERLODE_COLLECTOR_RATE_LIMIT_BURST=50 go run ./cmd/otherlode-collector
```

A throttled request gets `429` with a `Retry-After` header.

At startup an instance sends a manifest, its class listing chunks and
its static baseline chunks, each without waiting for the others. The agent retries a `429` a few times within one
send. If a chunk still fails, the agent keeps it and the chunks after
it. After each later flush that the collector confirms, the agent sends
the next kept chunk, one chunk per flush. A `429` delays a scan but does
not lose it. Only a `400` or `422` on a chunk makes the agent drop that
chunk and the rest of the scan. Behind a proxy, set
`OTHERLODE_COLLECTOR_CLIENT_IP_HEADER` so agents do not share one bucket.
For pods behind one NAT address, raise
`OTHERLODE_COLLECTOR_RATE_LIMIT_BURST`.

The limit is keyed on the connection's remote address. Behind a reverse
proxy or load balancer every agent arrives from the proxy's address and
they all share one bucket, so set `OTHERLODE_COLLECTOR_CLIENT_IP_HEADER` to
the header the proxy writes the real client address into
(`X-Forwarded-For`, `X-Real-IP`, and so on). For a comma-separated list
the last entry is used, since that is the one the nearest proxy wrote.
If the header appears on more than one line, the collector joins all the
lines first and then takes the last entry. The header is trusted as
given: only set this when the collector cannot be reached except through
that proxy.

```
OTHERLODE_COLLECTOR_CLIENT_IP_HEADER=X-Forwarded-For go run ./cmd/otherlode-collector
```

IPv4 and IPv4-mapped IPv6 addresses are keyed on the IPv4 address. Each
IPv6 address gets its own bucket by default. A single sender can cycle
through the 2^64 addresses of a `/64`, so when the collector faces the
internet directly, set `OTHERLODE_COLLECTOR_RATE_LIMIT_IPV6_PREFIX=64`
to make every address in one `/64` share a bucket. The value is a prefix
length from 1 to 128, and anything else stops the collector at startup.
Leave it at the default when many agents share a `/64`, as VPC subnets
and per-node pod ranges often do.

The limiter tracks at most 100,000 clients. When that many are tracked,
a request from a new client evicts the least recently used one. The
evicted client loses only its bucket and gets a fresh full one on its
next request. An agent sends at least one request every flush interval,
60 seconds by default, so it stays near the front of the list.

`/healthz` is never throttled, for the same liveness/readiness reason it's
never gated on auth.

`GET /healthz` returns `200` once the server is up, for liveness/readiness
probes. The image has no shell or `curl`, so
`otherlode-collector healthcheck` probes it instead: one GET of `/healthz`
on the address
`OTHERLODE_COLLECTOR_ADDR` names (loopback when the host is empty or a
wildcard), exiting 0 or 1, and never through a proxy. The image's
`HEALTHCHECK` runs it.

### Metrics

`GET /metrics` serves counters in the Prometheus text format. Like
`/healthz` it is unauthenticated and unthrottled; it exposes only counts.

| Counter | Labels | Meaning |
| --- | --- | --- |
| `otherlode_collector_ingest_accepted_total` | `payload` | Decoded, valid, handed to the sink |
| `otherlode_collector_ingest_rejected_total` | `payload`, `reason` | Turned away before or by the sink: `content_type` (also a `Content-Encoding` other than `identity`, answered `415`), `too_large`, `read`, `malformed`, `invalid`, `sink` (the backend did not store the payload; the response was the backend's `429` or other `4xx`, or `503` with `Retry-After: 5`), `canceled` (the client went away while the request waited for a decode slot; the response was `503`), `busy` (no decode slot came free within 1 second; the response was `503` with `Retry-After: 1`), `too_many_elements` (the body held more elements than its payload's limit; the response was `413`) |
| `otherlode_collector_auth_rejected_total` | | 401 responses |
| `otherlode_collector_token_reload_failures_total` | `file` | A token file re-read that failed or found no usable token, so the last good value stayed; `file` is `auth` (`OTHERLODE_COLLECTOR_AUTH_TOKEN_FILE`) or `forward` (`OTHERLODE_COLLECTOR_FORWARD_AUTH_TOKEN_FILE`) |
| `otherlode_collector_rate_limited_total` | | 429 responses |
| `otherlode_collector_forward_delivered_total` | `payload` | The backend answered `202`, and so did the collector |
| `otherlode_collector_forward_failed_total` | `payload`, `reason` | The backend did not store the payload, so the collector did not answer `202` and the agent sends it again: `rate_limited`, `rejected`, `server_error`, `timeout`, `transport`, `unexpected_status`, `busy`, `canceled`, `shutting_down`, `marshal`, `request` |
| `otherlode_collector_forward_notices_total` | `notice` | Notices the backend sent with an accepted payload: `service-over-cap`, `environment-not-tracked`, or `unknown` for any other value |
| `otherlode_collector_environment_mismatch_total` | `payload` | The agent's environment differed from the collector's configured one (see [Environment](#environment)); `payload` is `deltas`, `manifest`, `static_baseline`, or `class_listing` |
| `otherlode_collector_namespace_mismatch_total` | `payload` | The agent's service namespace differed from the collector's configured one (see [Namespace](#namespace)); `payload` is `deltas`, `manifest`, `static_baseline`, or `class_listing` |
| `otherlode_collector_redacted_literals_total` | `payload` | String literal parts replaced by the redaction processor (see [Redaction](#redaction)); `payload` is `manifest` or `static_baseline` |
| `otherlode_collector_redacted_case_keys_total` | `payload` | Case keys cleared by the redaction processor, from a switch on a string's hash code or equal to the hash code of a redacted literal (see [Branch and site keys](#branch-and-site-keys)); `payload` is `manifest` or `static_baseline` |
| `otherlode_collector_fields_stripped_total` | `payload` | Payloads that lost an unknown field to the redaction processor and were marked `fields_stripped` (see [Redaction](#redaction)); `payload` is `deltas`, `manifest`, `static_baseline` or `class_listing` |

`payload` is `deltas`, `manifest`, `static_baseline`, or `class_listing`. A rising
failed counter means the backend is not storing payloads; its `reason` says
why. Nothing is lost while the agents keep running, since each agent keeps
its counts and sends them again on its next flush. A rising token reload
failures counter means a token file is broken and the collector is still
on the tokens it last read, so a rotation has not taken effect.

### Forwarding

By default the collector only logs what it receives. Set
`OTHERLODE_COLLECTOR_FORWARD_URL` to the base URL of a backend and every
decoded payload is relayed there instead, as a `POST` to the same
`/v1/otherlode/deltas`, `/v1/otherlode/manifest`,
`/v1/otherlode/static-baseline`, and `/v1/otherlode/class-listing` paths
with the same
`application/x-protobuf` body. The URL must not have a query, a fragment or
user info, and the collector stops at startup if it does.
`OTHERLODE_COLLECTOR_FORWARD_AUTH_TOKEN`,
if set, is sent as a bearer token on those requests. Spaces around its
value are trimmed, and a value of only spaces stops the collector at
startup. It is a separate secret from `OTHERLODE_COLLECTOR_AUTH_TOKEN`:
the agent authenticates to the collector, the collector authenticates to
the backend, and the two need not match.

The forwarder honours the standard `HTTPS_PROXY`, `HTTP_PROXY` and
`NO_PROXY` variables, so it can reach a backend through an outbound
proxy. It never sends a request for `localhost` or a loopback address
through a proxy.

```
OTHERLODE_COLLECTOR_AUTH_TOKEN=s3cret \
OTHERLODE_COLLECTOR_FORWARD_URL=https://backend.example.com \
OTHERLODE_COLLECTOR_FORWARD_AUTH_TOKEN=backend-secret \
go run ./cmd/otherlode-collector
```

To keep the key out of the environment, name a file that holds it in
`OTHERLODE_COLLECTOR_FORWARD_AUTH_TOKEN_FILE` instead. The file follows the
same rules as `OTHERLODE_COLLECTOR_AUTH_TOKEN_FILE`: blank lines and `#`
lines are skipped, and spaces are trimmed. It must hold exactly one key,
since the collector sends one key and the backend is where several keys
can overlap during a rotation. Setting both variables stops the
collector at startup, and so does a file that cannot be read or does
not hold exactly one key, a key with a control character, or a key
variable set without `OTHERLODE_COLLECTOR_FORWARD_URL`. While
forwarding is on, the collector reads the file again every 30 seconds
and sends the new key from the next request on, and the same advice on
changing the file applies. A re-read that fails, or finds no key, more
than one, or a control character, keeps the last good key, logs a
warning, and counts in
`otherlode_collector_token_reload_failures_total{file="forward"}`.

The collector forwards a payload while the agent's request waits, makes
one attempt, and answers from the backend's answer. It answers `202` only
when the backend answered `202`. A `429` stays `429`, with the backend's
`Retry-After`. Any other `4xx` keeps its status. A `5xx`, a redirect, a
timeout, no connection, an unexpected `2xx`, a full forward limit or a
shutdown becomes `503` with `Retry-After: 5`. There is no queue, no retry
and no drop: a payload is either stored or the agent keeps it and sends it
again. The agent ends a send at once when `Retry-After` is over 2 seconds,
and sends again on its next flush. It reports a probe again only when its
hit count changes, which is why the collector must not acknowledge a
payload that the backend has not stored. A process that stops during an
outage loses the counts it had not delivered.

A request has 9 seconds from the moment the handler starts on it. The wait
for a decode slot takes at most 1 second. The attempt to the backend ends
after `OTHERLODE_COLLECTOR_FORWARD_REQUEST_TIMEOUT` or when the request's
time runs out, whichever comes first. Startup refuses a request timeout
that is not below 9 seconds. A request takes a forward slot before it
frees its decode slot, and `OTHERLODE_COLLECTOR_MAX_CONCURRENT_FORWARDS`
bounds the forwards in flight.

On shutdown the collector closes its listener at once, so a new
connection is refused, and a request already in flight finishes within the
10-second deadline. A request that reaches the forwarder after shutdown
starts, and one still in flight when the deadline passes, gets `503`.

A failed forward logs once a minute for each cause, and the counters count
every one. A `4xx` from the backend logs a warning, `sink refused delta
batch`, with the `status`. Any other failure logs an error, `sink rejected
delta batch`, with a `reason`. For a backend response, `error` gives the
status. When the response body is a JSON object with an `error` string,
the warning adds it as `backend_error`, cut to 64 bytes, with each byte
outside printable ASCII replaced by `?`. A 403 with `backend_error` set to
`redaction_required` or `redaction_secret_mismatch` means the backend
wants redacted payloads, and this collector has redaction off or a
different secret. See [Branch and site keys](#branch-and-site-keys).

The two tuning variables below are this collector's own. The defaults
should rarely need changing. Durations use Go syntax (`5s`, `8s`).

| Variable | Default | Meaning |
| --- | --- | --- |
| `OTHERLODE_COLLECTOR_FORWARD_REQUEST_TIMEOUT` | `8s` | Bound on the one attempt to the backend; must be below 9 seconds |
| `OTHERLODE_COLLECTOR_MAX_CONCURRENT_FORWARDS` | `32` | Forwards in flight to the backend at one time |

Trying this out locally needs no real backend: a second
`otherlode-collector` instance is a valid one, since `ForwardingSink`
posts the same paths and body shape this collector itself accepts. Point
one instance's `OTHERLODE_COLLECTOR_FORWARD_URL` at another's address and
its `LogSink` will print what the first instance relayed. `internal/forward`'s integration
tests do the same thing without a second process, wiring `ForwardingSink`
straight into a second `ingest.Handler` to confirm a payload decoded on
one end survives the relay and decodes identically on the other.

### Resource limits

Decoding a protobuf can take much more memory than the request body. The
handler counts a body's elements before it decodes the body and refuses
one over its payload's limit with a 413. At those limits the hosted
server measured at most about 230 MiB for one decode. Set `GOMEMLIMIT` and a container memory limit, and keep
authentication on.

`OTHERLODE_COLLECTOR_MAX_CONCURRENT_DECODES` caps how many requests are
decoded and handed to the sink at one time. The default is the number of
CPUs (`GOMAXPROCS`), because decoding is CPU-bound and more parallel
decodes than CPUs gain no throughput. A request reads its body first and
takes a slot only after that, so a slow sender holds no slot. A request
that waits for a slot and whose client disconnects gets `503`. One that
waits more than 1 second gets `503` with `Retry-After: 1`. The collector
logs a warning for such a refusal at most once a minute. Its
`unlogged_busy_refusals` field counts the refusals since the previous
warning that were not logged. A request keeps its decode slot until it
holds a forward slot, and `OTHERLODE_COLLECTOR_MAX_CONCURRENT_FORWARDS`
(default `32`) bounds the forwards in flight. The worst case is roughly the
decode cap times the largest decode (the hosted server measured about
230 MiB), plus the bodies waiting for a decode slot (up to 16 MiB each,
for at most 1 second), plus the forward cap times the largest marshaled
payload.

### Environment

The agent takes `environment` from its own `environment` option, then
OpenTelemetry's `deployment.environment.name` or `deployment.environment`,
then `test` for a test run. With none of these, a payload arrives with the
field blank. UAT traffic
looks nothing like prod traffic: code that is dead in prod is often
exercised in UAT, and the reverse. A backend has to know which
environment each payload came from to keep the two apart. An operator
usually runs one collector per environment, so the collector can label
every payload that passes through it, with no change to any JVM's config.

Set `OTHERLODE_COLLECTOR_ENVIRONMENT` to the environment name to stamp onto
every delta batch, manifest, static baseline and class listing the collector
ingests:

```
OTHERLODE_COLLECTOR_ENVIRONMENT=prod go run ./cmd/otherlode-collector
```

`OTHERLODE_COLLECTOR_ENVIRONMENT_ACTION` controls what happens when a
payload already names an environment:

| Action | Behaviour |
| --- | --- |
| `insert` (default) | Fills in the environment only when the agent left it blank; an agent's explicit value wins |
| `upsert` | Always writes the collector's value, even over one the agent set; guarantees nothing passing through a prod collector is ever labelled anything else |

The action ignores case and surrounding spaces.

The collector compares the two environments as the server does. It trims
surrounding spaces and ignores case, so ` Prod` and `prod` are one
environment. An agent value made only of spaces counts as none.

When the agent's value differs from the collector's, that is a mismatch:
`otherlode_collector_environment_mismatch_total` goes up under either
action, and a log line names the service, instance and run involved. The
line is a `warn` the first time a service sends a given environment, and
`debug` for every later payload of that service with that environment. A
non-zero count means some JVM is configured for a different environment
than the collector it reports to, or that a test run reports through it.
A test run names the environment `test` when nothing else names one
(agent ADR 0050), so each of its payloads counts as a mismatch here.
Under `upsert` its environment becomes the collector's, which is
harmless, since the test-run flag travels with the payload.

Setting `OTHERLODE_COLLECTOR_ENVIRONMENT_ACTION` without also setting
`OTHERLODE_COLLECTOR_ENVIRONMENT` is a misconfiguration and stops the
collector at startup.

### Namespace

A service is known by its namespace and its name, so two teams can each
run a service called `billing` and stay apart. The agent sets
`service_namespace` on a payload only when it finds one, in its own
`serviceNamespace` option or in OpenTelemetry's settings. One
collector often serves one team, and setting that team's namespace once
on the collector is easier than setting it on every agent. See
[ADR 0002](docs/adr/0002-a-namespace-processor-stamps-a-service-namespace.md).

Set `OTHERLODE_COLLECTOR_SERVICE_NAMESPACE` to the namespace to stamp onto
every delta batch, manifest, static baseline and class listing the collector
ingests:

```
OTHERLODE_COLLECTOR_SERVICE_NAMESPACE=payments go run ./cmd/otherlode-collector
```

With no value set, the processor is off. One collector can then serve
every namespace, and each agent's namespace passes through unchanged.

`OTHERLODE_COLLECTOR_SERVICE_NAMESPACE_ACTION` controls what happens when a
payload already names a namespace:

| Action | Behaviour |
| --- | --- |
| `insert` (default) | Fills in the namespace only for an agent that sent none; an agent's explicit value wins |
| `upsert` | Always writes the collector's value, even over one the agent set |

The action ignores case and surrounding spaces.

The collector compares the two values after it trims surrounding spaces.
Case counts: `Payments` and `payments` are different namespaces. An agent
value made only of spaces counts as none. When the agent's value differs
from the collector's, `otherlode_collector_namespace_mismatch_total` goes up
under either action, and a log line names the service, instance and run
involved. The line is a `warn` the first time a service sends a given
namespace, and `debug` for every later payload of that service with that
namespace.

Setting `OTHERLODE_COLLECTOR_SERVICE_NAMESPACE_ACTION` without also setting
`OTHERLODE_COLLECTOR_SERVICE_NAMESPACE` is a misconfiguration and stops the
collector at startup.

### Redaction

A branch site's condition and a string `switch`'s case labels can hold
the adopter's own string literals, such as
`System.getenv("ENABLE_LEGACY_DISCOUNT") == "true"`. The collector runs
inside the adopter's network, so it is the last place to hide them before
they leave. The redaction processor does that. See
[ADR 0001](docs/adr/0001-a-redaction-processor-hides-literals-before-they-leave.md).

The agent sends each condition and case label as a list of parts, each
marked as code, string literal or placeholder. The processor looks only
at string literal parts, in manifests and static baselines. A redacted
part keeps its kind, and its text becomes `…`. Code parts, placeholders,
and class, method and file names are never changed, since the pipeline
needs the names.

Two settings turn it on. Both are off by default. Either one also needs
a secret, set as described under
[Branch and site keys](#branch-and-site-keys).

| Variable | Meaning |
| --- | --- |
| `OTHERLODE_COLLECTOR_REDACT_BLOCKED_VALUES` | Regular expressions in Go syntax, one per line, since a comma can appear inside a pattern. A literal that any of them matches anywhere in its text is redacted. Anchor a pattern with `^` and `$` to match the whole literal. Blank lines are ignored. |
| `OTHERLODE_COLLECTOR_REDACT_ALL_LITERALS` | `1`, `t`, `T`, `true`, `TRUE` or `True` redacts every string literal. `0`, `f`, `F`, `false`, `FALSE` or `False` leaves this setting off. |

```
OTHERLODE_COLLECTOR_REDACT_BLOCKED_VALUES='(?i)password|secret|token
^sk_live_' \
OTHERLODE_COLLECTOR_REDACT_SECRET_FILE=/run/secrets/otherlode-redact-secret \
go run ./cmd/otherlode-collector
```

A pattern that does not compile stops the collector at startup with the
variable and the pattern's line named. So does a value for
`OTHERLODE_COLLECTOR_REDACT_ALL_LITERALS` that does not parse as a boolean.
`otherlode_collector_redacted_literals_total` counts the parts replaced,
and a `debug` log line names the service, instance, run and count for
each payload that had any. Neither ever includes a literal's text.

While either setting is on, the collector also drops every field it does
not know from every payload, delta batches included. Go keeps unknown
fields when it re-encodes a message for forwarding. An agent built
against a newer schema than this collector could send a new field that
carries a literal, and the collector would forward it unseen. The cost is
that a newer agent's new fields are lost until the collector is updated.

A payload that loses at least one field this way is marked. The collector
sets `fields_stripped` on the payload's resource and never clears it. The
server keeps the data but counts a run that carried the mark in no
finding, and labels the run as sent through a collector
older than its agent (agent ADR 0054, collector
[ADR 0005](docs/adr/0005-a-collector-that-strips-unknown-fields-marks-the-payload.md)).
`otherlode_collector_fields_stripped_total` counts the marked payloads. The
collector also logs one `WARN` line per run id, naming the namespace,
service, instance, run id and `agent_version`. The fix is to upgrade to the
latest collector. With redaction off nothing is dropped
and nothing is marked.

While redaction is on, a condition part of any kind other than code or
placeholder is treated as a string literal. A newer agent's unknown
literal kind is redacted too, so redaction fails closed.

Redaction happens only here. An agent that posts straight to a backend,
with no collector in between, sends literals and plain keys in clear.
That includes a test JVM that runs the agent with `testRun`: point it at
a collector with the same redaction settings and secret as production's.

#### Branch and site keys

The agent names each branch outcome and site with a key, a digest of the
site's bytecode that includes its string literals. A plain key lets
anyone who holds it hash a guess at a redacted literal and compare. So
while redaction is on, the collector replaces each `branch_key` and
`site_key` with HMAC-SHA256 of the key under a secret, as 32 lowercase
hex characters, the shape the agent sends. Equal keys stay equal, so the
backend still joins one branch across builds and instances. See
[ADR 0007](docs/adr/0007-redaction-re-keys-branch-and-site-keys-with-a-tenant-secret.md).

| Variable | Meaning |
| --- | --- |
| `OTHERLODE_COLLECTOR_REDACT_SECRET` | The secret, at least 32 bytes. Spaces around it are trimmed. |
| `OTHERLODE_COLLECTOR_REDACT_SECRET_FILE` | A file that holds the secret on one line. Blank lines and lines that start with `#` are skipped, and spaces are trimmed, as in the token files. |

```
OTHERLODE_COLLECTOR_REDACT_ALL_LITERALS=1 \
OTHERLODE_COLLECTOR_REDACT_SECRET_FILE=/run/secrets/otherlode-redact-secret \
go run ./cmd/otherlode-collector
```

Make the secret with `openssl rand -hex 32`. Give the same secret to
every collector that sends to one Otherlode tenant: each environment's,
and the one your test runs go through. A collector with a different
secret gives one branch a different key, and the backend then keeps two
histories for it. At startup the collector logs a `secret_fingerprint`,
16 hex characters that name the secret without revealing it. Two
collectors with the same secret log the same fingerprint, so compare
them to check that a tenant's collectors agree.

While redaction is on, the collector also sends the fingerprint in an
`Otherlode-Redaction` header on every request it forwards. With
redaction off it sends no such header. A backend may require the header
for a tenant that wants only redacted payloads. It then refuses a
payload without the header, such as one an agent posted to it directly,
with 403 `redaction_required`. It refuses a payload with another
fingerprint with 403 `redaction_secret_mismatch`. The collector drops
either payload and logs a warning, as described under
[Forwarding](#forwarding). The header catches a setup mistake. It does
not prove who sent a payload, since the fingerprint travels in every
request and appears in the startup log.

With redaction on, the collector stops at startup when neither variable
is set or both are, when the secret is shorter than 32 bytes or holds a
control character, and when it equals an agent auth token or the
forward key. Every agent holds the auth token and the backend holds the
forward key, so neither can protect the keys. It also stops when either
variable is set and redaction is off. The collector reads the secret
once, at startup. A changed file takes effect at the next restart.

Turning redaction on, or changing the secret, changes every key. The
backend then treats every branch and site as new, and the history it
holds under the old keys stops there. There is no way to change the
secret without that break.

A string `switch` that the agent cannot read back reaches the collector
as a switch on the subject's `hashCode()`, and each case key is the hash
code of one case's literal. While redaction is on, the collector clears
two kinds of case key:

- Every case key of a switch the agent marks `string_hash_code_switch`.
  The collector cannot tell which literal a key hashes, so it clears the
  keys whether or not a redaction setting would block that literal.
- A case key that equals the Java hash code of a literal the collector
  redacted in the same method. This rule covers an agent that does not
  send the mark.

A cleared case has neither a key nor a label.
`otherlode_collector_redacted_case_keys_total` counts the cleared keys.
From an agent that does not send the mark, a hash code whose literal is
not in the payload, because the agent could not write that condition, is
not cleared.

## Development

```
go build ./...
go vet ./...
go test ./...
```

The wire schema is owned by the agent repo and published to the
[Buf Schema Registry](https://buf.build/otherlode/otherlode). To pick up a
schema change, bump the Go bindings:

```
go get buf.build/gen/go/otherlode/otherlode/protocolbuffers/go@latest
```

The Go module proxy can go on serving an older version as `@latest` for
a while after a push. If the bump does not pick up the new schema, run
it again with `GOPROXY=direct`.

## License

Apache-2.0. See [LICENSE](LICENSE).
