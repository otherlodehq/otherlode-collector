---
title: Configuration
description: Every collector setting with its default, accepted values and what an invalid value does, plus the command line, the healthcheck subcommand and the proxy and Go variables that change its behaviour.
order: 20
---

You configure the collector with environment variables only. It has 28 of them, all starting with `OTHERLODE_COLLECTOR_`. It reads them once, at startup. The two token files are the exception, because the collector re-reads them every 30 seconds. The redaction secret file is not one of them. The collector reads it once. Any other change needs a restart.

Every invalid value stops startup. The collector writes one `ERROR` line to standard output, then exits with code 1. It never falls back to a default for a value you set wrongly, and this is the opposite of the [agent](/docs/agent/configuration), which warns and keeps running. The collector sits between your agents and Otherlode. A silent fallback could turn off redaction or authentication without anyone noticing.

For a first run, you only need an agent token. See [deploy](deploy).

## Rules that apply to every setting

- An unset variable and a variable set to an empty string are the same thing. Both give the default.
- The collector does not trim spaces unless a row below says so. A value of only spaces is therefore a value, and usually an invalid one.
- A variable that ends in `_FILE` names a file inside the container. Mount the file there.
- Boolean values use Go's `strconv.ParseBool` forms, which are `1`, `t`, `T`, `TRUE`, `true`, `True`, `0`, `f`, `F`, `FALSE`, `false` and `False`. `yes`, `no`, `on` and `off` are invalid.
- Duration values use Go's syntax, such as `500ms`, `30s`, `5m` or `1h30m`. A bare number such as `10` is invalid because it has no unit. A duration must be greater than zero.
- Integer values are plain decimal numbers with no spaces and no unit.

An invalid value logs a line like these. The `msg` field carries the text.

```text
{"time":"...","level":"ERROR","msg":"OTHERLODE_COLLECTOR_FORWARD_REQUEST_TIMEOUT: time: missing unit in duration \"10\""}
{"time":"...","level":"ERROR","msg":"OTHERLODE_COLLECTOR_FORWARD_SHARDS must be positive"}
{"time":"...","level":"ERROR","msg":"OTHERLODE_COLLECTOR_REDACT_ALL_LITERALS: strconv.ParseBool: parsing \"yes\": invalid syntax"}
```

## Listening and logs

| Variable | Default | Accepted values | If the value is invalid |
|---|---|---|---|
| `OTHERLODE_COLLECTOR_ADDR` | `:4319` | A `host:port` the collector can listen on. The host can be empty. It serves plain HTTP, with no TLS | Startup logs the listen error and exits with code 1 |
| `OTHERLODE_COLLECTOR_LOG_LEVEL` | `info` | `debug`, `info`, `warn` or `error`, in any letter case. A level can take a signed offset, such as `warn+1` or `info-2`, to set a threshold between two named levels, which sit 4 apart | Startup fails. `warning` is invalid |

The collector checks the listen address when it opens the port, after it has checked every other setting. If the port is taken or the address is malformed, it logs the failure and exits. It logs the `otherlode-collector listening` line only after it has opened the port.

```text
{"time":"...","level":"ERROR","msg":"listen tcp: address bad: missing port in address"}
{"time":"...","level":"ERROR","msg":"listen tcp :4319: bind: address already in use"}
```

The collector writes logs as JSON lines to standard output. See [monitoring](monitoring) for the log format and the metrics.

## Agent authentication

These settings control which bearer tokens the collector accepts from agents. See [tokens](tokens) for how to rotate them.

| Variable | Default | Accepted values | If the value is invalid |
|---|---|---|---|
| `OTHERLODE_COLLECTOR_AUTH_TOKEN` | None | One token, or a comma-separated list. The collector trims spaces around each token | Startup fails if any entry is empty, including a value of only spaces. The error names the entry, such as `entry 2 of 3 is empty` |
| `OTHERLODE_COLLECTOR_AUTH_TOKEN_FILE` | None | The path of a file with one token per line. The collector skips blank lines and lines that start with `#`, after trimming spaces | Startup fails if the file is missing, is not a regular file, is larger than 1 MiB, or holds no token |
| `OTHERLODE_COLLECTOR_INSECURE_NO_AUTH` | `false` | A boolean. `true` lets the collector start with no token | Startup fails, even when a token is set |

The collector needs one of the first two variables. With neither, and with `OTHERLODE_COLLECTOR_INSECURE_NO_AUTH` not true, startup fails.

```text
{"time":"...","level":"ERROR","msg":"neither OTHERLODE_COLLECTOR_AUTH_TOKEN nor OTHERLODE_COLLECTOR_AUTH_TOKEN_FILE is set; refusing to start without auth (set OTHERLODE_COLLECTOR_INSECURE_NO_AUTH=1 to run unauthenticated)"}
```

Setting both `OTHERLODE_COLLECTOR_AUTH_TOKEN` and `OTHERLODE_COLLECTOR_AUTH_TOKEN_FILE` also fails startup, with `OTHERLODE_COLLECTOR_AUTH_TOKEN and OTHERLODE_COLLECTOR_AUTH_TOKEN_FILE are both set; set one`.

`OTHERLODE_COLLECTOR_INSECURE_NO_AUTH` takes effect only when neither token variable is set. A token or a token file always turns authentication on, whatever this variable says. A value that does not parse stops startup in either case, so a typo cannot pass for `false`:

```text
{"time":"...","level":"ERROR","msg":"OTHERLODE_COLLECTOR_INSECURE_NO_AUTH: strconv.ParseBool: parsing \"yes\": invalid syntax"}
```

A token file that cannot be read stops startup even when `OTHERLODE_COLLECTOR_INSECURE_NO_AUTH` is true. With the opt-out, the collector logs this at `WARN` and accepts any caller:

```text
{"time":"...","level":"WARN","msg":"OTHERLODE_COLLECTOR_AUTH_TOKEN and OTHERLODE_COLLECTOR_AUTH_TOKEN_FILE not set; ingest endpoints are unauthenticated"}
```

The agent sends its token as `authToken`. See [authToken](/docs/agent/configuration#authtoken) in the agent's configuration.

## Forwarding

These settings control where the collector sends payloads and how it queues and retries them. See [forwarding](forwarding) for how the queues, retries and shutdown work.

| Variable | Default | Accepted values | If the value is invalid |
|---|---|---|---|
| `OTHERLODE_COLLECTOR_FORWARD_URL` | None, so the collector only logs payloads | An absolute `http` or `https` URL with a host, no user info, no query and no fragment. A path is allowed. The collector removes trailing slashes | Startup fails with a `forward URL` error that names the problem |
| `OTHERLODE_COLLECTOR_FORWARD_AUTH_TOKEN` | None | The key the collector sends to the forward URL. The collector trims spaces around it. It cannot hold a control character | Startup fails for a value of only spaces or a control character |
| `OTHERLODE_COLLECTOR_FORWARD_AUTH_TOKEN_FILE` | None | The path of a file that holds exactly one key. Blank lines and `#` lines do not count | Startup fails if the file is missing, is not a regular file, is larger than 1 MiB, holds no key or more than one, or holds a control character |
| `OTHERLODE_COLLECTOR_FORWARD_SHARDS` | `8` | A positive integer | Startup fails |
| `OTHERLODE_COLLECTOR_FORWARD_QUEUE_SIZE` | `64` | A positive integer. Payloads per shard | Startup fails |
| `OTHERLODE_COLLECTOR_FORWARD_QUEUE_BYTES` | `67108864` (64 MiB) | A positive integer. Bytes per shard | Startup fails |
| `OTHERLODE_COLLECTOR_FORWARD_REQUEST_TIMEOUT` | `10s` | A positive duration. The limit for one attempt | Startup fails |
| `OTHERLODE_COLLECTOR_FORWARD_RETRY_INITIAL_INTERVAL` | `5s` | A positive duration. The first wait between attempts | Startup fails |
| `OTHERLODE_COLLECTOR_FORWARD_RETRY_MAX_INTERVAL` | `30s` | A positive duration. The longest wait between attempts | Startup fails |
| `OTHERLODE_COLLECTOR_FORWARD_RETRY_MAX_ELAPSED_TIME` | `5m` | A positive duration. How long the collector keeps retrying one payload | Startup fails |

Without `OTHERLODE_COLLECTOR_FORWARD_URL`, the collector starts and logs this at `WARN`. Payloads are decoded and logged, never sent.

```text
{"time":"...","level":"WARN","msg":"OTHERLODE_COLLECTOR_FORWARD_URL not set; ingest payloads are only logged, not forwarded"}
```

The tuning variables are read only to build the forwarder. With no forward URL they are still checked, so a bad value stops startup, but they do nothing.

Two combinations fail startup on purpose.

- A forward key variable with no forward URL. The error is `OTHERLODE_COLLECTOR_FORWARD_AUTH_TOKEN or OTHERLODE_COLLECTOR_FORWARD_AUTH_TOKEN_FILE is set but OTHERLODE_COLLECTOR_FORWARD_URL is empty; set OTHERLODE_COLLECTOR_FORWARD_URL or unset the key variable`.
- Both `OTHERLODE_COLLECTOR_FORWARD_AUTH_TOKEN` and `OTHERLODE_COLLECTOR_FORWARD_AUTH_TOKEN_FILE`. The error ends with `are both set; set one`.

A retry initial interval above the maximum interval is allowed. The collector then uses the maximum as the first wait.

A forward URL that starts with `http://` and comes with a key gets a `WARN` at startup, because the key travels unencrypted.

```text
{"time":"...","level":"WARN","msg":"forward URL uses plain http; the backend auth token is sent unencrypted","url":"http://..."}
```

For the Otherlode URL and key to use, see [Send data from your collector](/docs/server/connect). The collector sends the key as `Authorization: Bearer <key>`. A forward URL with no key sends no `Authorization` header.

## Environment and namespace

These settings stamp one environment name and one service namespace on every payload. See [environment and namespace](environment-and-namespace) for the effects and the mismatch counters.

| Variable | Default | Accepted values | If the value is invalid |
|---|---|---|---|
| `OTHERLODE_COLLECTOR_ENVIRONMENT` | None, so the collector leaves the agent's environment alone | Any text. The collector trims spaces. A value that is empty after trimming turns stamping off | Startup fails if the text is not valid UTF-8 |
| `OTHERLODE_COLLECTOR_ENVIRONMENT_ACTION` | `insert` | `insert` or `upsert`, in any letter case. The collector trims spaces. A value that is empty after trimming counts as not set | Startup fails. A set action with no environment also fails |
| `OTHERLODE_COLLECTOR_SERVICE_NAMESPACE` | None, so each agent's namespace passes through | Any text except `.` and `..`. The collector trims spaces. A value that is empty after trimming turns stamping off | Startup fails for `.`, `..` or text that is not valid UTF-8 |
| `OTHERLODE_COLLECTOR_SERVICE_NAMESPACE_ACTION` | `insert` | `insert` or `upsert`, in any letter case. The collector trims spaces. A value that is empty after trimming counts as not set | Startup fails. A set action with no namespace also fails |

`insert` fills in a missing value and keeps the agent's own. `upsert` always writes the collector's value.

An action variable that is set, even to `insert`, with a blank value variable is an error. The collector refuses to guess that you meant to stamp. For the environment, the error reads `OTHERLODE_COLLECTOR_ENVIRONMENT_ACTION is set but OTHERLODE_COLLECTOR_ENVIRONMENT is empty; set OTHERLODE_COLLECTOR_ENVIRONMENT or unset OTHERLODE_COLLECTOR_ENVIRONMENT_ACTION`. The namespace has the same error with its own names. An invalid action reads `invalid action "replace": must be "insert" or "upsert"`.

When stamping is on, the collector logs `stamping environment on ingested payloads` or `stamping service namespace on ingested payloads` at `INFO`, with the value and the action.

## Redaction

These settings blank string literals in branch conditions before payloads leave your network, and replace branch and site keys with keys made under a secret. See [redaction](redaction) for what the collector blanks and what it never changes.

| Variable | Default | Accepted values | If the value is invalid |
|---|---|---|---|
| `OTHERLODE_COLLECTOR_REDACT_BLOCKED_VALUES` | None | One regular expression per line, in Go's RE2 syntax. The collector blanks a literal when any pattern matches part of its text. Matching is case-sensitive unless the pattern starts with `(?i)` | Startup fails if a pattern does not compile. The error names the line, such as `line 2` |
| `OTHERLODE_COLLECTOR_REDACT_ALL_LITERALS` | `false` | A boolean. `true` blanks every string literal | Startup fails |
| `OTHERLODE_COLLECTOR_REDACT_SECRET` | None | The secret that keys the HMAC over branch and site keys. The collector trims spaces around it | Startup fails. See the rules below |
| `OTHERLODE_COLLECTOR_REDACT_SECRET_FILE` | None | The path of a file that holds exactly one secret. Blank lines and `#` lines do not count | Startup fails if the file is missing, is not a regular file, is larger than 1 MiB, or does not hold exactly one secret, and for the rules below |

For `OTHERLODE_COLLECTOR_REDACT_BLOCKED_VALUES`, the collector skips lines that are empty after trimming spaces, and drops a trailing carriage return. It uses every other line as written, so leading and trailing spaces become part of the pattern. Line numbers count from 1 and include the skipped lines. A comma is part of a pattern, which is why each pattern gets its own line. Patterns are not anchored, so `secret` matches any literal that contains `secret`.

```text
{"time":"...","level":"ERROR","msg":"OTHERLODE_COLLECTOR_REDACT_BLOCKED_VALUES line 2: error parsing regexp: missing closing ]: `[`"}
```

You can set both blanking variables. Either one is enough to blank a literal. With neither set, redaction is off.

While redaction is on, the collector needs a secret. It replaces each branch key and site key with an HMAC of the key under that secret, so that a key no longer lets anyone test a guess at a blanked literal. Startup fails in each of these cases:

- Redaction is on and neither secret variable is set.
- Both secret variables are set.
- The secret is shorter than 32 bytes, or holds a control character.
- The secret equals an agent token or the forward key.
- A secret variable is set while redaction is off.

Make a secret with `openssl rand -hex 32`. Give every collector that sends to one Otherlode tenant the same secret, including the collector your test runs report through. The collector reads the secret once, at startup, and logs its fingerprint as `secret_fingerprint`. Changing the secret, or turning redaction on later, makes Otherlode treat every branch as new. [Redaction](redaction#replace-branch-and-site-keys) explains each of these rules and lists the error messages.

## Limits

These settings protect the collector from a flood of requests and from decode memory. See [limits](limits) for the numbers behind them.

| Variable | Default | Accepted values | If the value is invalid |
|---|---|---|---|
| `OTHERLODE_COLLECTOR_RATE_LIMIT_RPS` | `5` | A number of requests per second per client address. `0` turns the limit off. Decimals such as `0.5` work | Startup fails for text that is not a number, a negative number, `inf` or `NaN` |
| `OTHERLODE_COLLECTOR_RATE_LIMIT_BURST` | `20` | An integer. The largest spike one client can send | Startup fails for text that is not an integer. It also fails for `0` or a negative number when the rate is above `0`. With the rate at `0`, the burst is ignored |
| `OTHERLODE_COLLECTOR_RATE_LIMIT_IPV6_PREFIX` | `128` | An integer from `1` to `128`. IPv6 addresses that share these leading bits share one bucket | Startup fails |
| `OTHERLODE_COLLECTOR_CLIENT_IP_HEADER` | None, so the limit uses the connection's address | A request header name, such as `X-Forwarded-For`. The collector reads the last entry. A request without the header, or with an empty one, falls back to the connection's address | No check. A name that no request carries has no effect |
| `OTHERLODE_COLLECTOR_MAX_CONCURRENT_DECODES` | The Go runtime's `GOMAXPROCS`, which is the number of CPUs available | A positive integer | Startup fails |

Only set `OTHERLODE_COLLECTOR_CLIENT_IP_HEADER` when a proxy in front of the collector sets or appends to that header, and nothing else can reach the collector. Otherwise a client can choose its own bucket. See [limits](limits) for the details. The rate limit settings do nothing when the rate is `0`, including `OTHERLODE_COLLECTOR_RATE_LIMIT_IPV6_PREFIX` and `OTHERLODE_COLLECTOR_CLIENT_IP_HEADER`, but their parse rules still apply. A rate of `0` logs this at `WARN`.

```text
{"time":"...","level":"WARN","msg":"rate limiting disabled; ingest endpoints accept requests unthrottled"}
```

## Command line

The collector takes no flags. With no arguments, it serves.

| Command | What it does | Exit code |
|---|---|---|
| `otherlode-collector` | Starts the collector | `1` if startup fails or the server stops with an error, `0` after a clean shutdown |
| `otherlode-collector healthcheck` | Sends one `GET /healthz` to the collector's own address and exits | `0` for a 200 response, `1` for anything else |
| Any other argument list | Prints `usage: otherlode-collector [healthcheck]` to standard error | `2` |

The container image runs the healthcheck for you. It sets `HEALTHCHECK --interval=10s --timeout=3s --start-period=10s --retries=3`. The subcommand gives up after 2 seconds.

The subcommand reads only `OTHERLODE_COLLECTOR_ADDR`, so it needs no token and no other setting. It probes the same port the collector listens on. If the host is empty or unspecified, such as `:4319` or `0.0.0.0:4319`, it connects to `127.0.0.1`. An address that is not a valid `host:port` makes it print `healthz url: ...` and exit with code 1. If the collector listens on a name or IP, run the subcommand with the same `OTHERLODE_COLLECTOR_ADDR` value. See [monitoring](monitoring).

## Variables the collector does not own

Some variables are not part of the collector's own settings but change what it does.

| Variable | Effect |
|---|---|
| `HTTPS_PROXY`, `HTTP_PROXY`, `NO_PROXY` | The forwarder sends requests through the proxy these name, using Go's standard rules. Go also reads lowercase names. `NO_PROXY` lists hosts that skip the proxy. A request to `localhost` or a loopback address never goes through a proxy. The `healthcheck` subcommand ignores all three, so a proxy never catches the probe. Only forwarding uses them |
| `GOMAXPROCS` | Sets how many CPUs the Go runtime uses. It is the default for `OTHERLODE_COLLECTOR_MAX_CONCURRENT_DECODES` |
| `GOMEMLIMIT` | A soft memory limit that the Go runtime reads. See [limits](limits) |

The collector reads the proxy variables once, when the forwarder first sends. Restart the collector after changing them. See [forwarding](forwarding).

## Related pages

- [Deploy the collector](deploy) to run the image with the minimum settings.
- [Set and rotate tokens](tokens) for the token variables and files.
- [Forwarding](forwarding) for queues, retries and shutdown.
- [Monitoring](monitoring) for `/healthz`, `/metrics` and logs.
- [Troubleshooting](troubleshooting) when the collector will not start.
