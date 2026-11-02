# Changelog

All notable changes to the collector are recorded here in the
[Keep a Changelog](https://keepachangelog.com/en/1.1.0/) format.

`scripts/release.sh` and the release workflow both require a `## [X.Y.Z]` section with a body for each release.
Keep link reference definitions (`[0.1.0]: https://...`) at the bottom of the file: the first one ends
the section being read.

## [Unreleased]

## [0.1.0] - 2026-11-01

The first release of the Otherlode collector. It receives what Otherlode agents send, checks each payload, can redact it, and forwards it to Otherlode. It stores nothing. The docs are at https://otherlode.dev/docs.

### Added

- **The image**, `ghcr.io/otherlodehq/otherlode-collector`, tagged `0.1.0`, `0.1` and `latest`, for `linux/amd64` and `linux/arm64`. It runs as a non-root user on a distroless base and listens on port 4319. Each image carries a build provenance attestation, which `gh attestation verify` checks.
- **Ingest** of the agent's four payloads, as protobuf over HTTP: delta batches, manifests, static baselines and class listings. The collector checks each payload's shape and size before it answers 202, so a malformed or oversized payload is refused here first.
- **Agent tokens.** Each agent sends a bearer token. Set one or more tokens in `OTHERLODE_COLLECTOR_AUTH_TOKEN`, or in a file the collector reads again every 30 seconds, so you can rotate a token without a restart.
- **Forwarding** to Otherlode with your ingest key, while the agent's request waits. The collector makes one attempt and answers `202` only when Otherlode answered `202`. It passes on a `429` with Otherlode's `Retry-After` and any other `4xx` status, and answers `503` with `Retry-After: 5` for a `5xx`, a timeout, no connection, a full forward limit or a shutdown. It keeps no queue, retries nothing and drops nothing: a payload is either stored or the agent keeps it and sends it again. A request has 9 seconds, and a stop lets requests in flight finish within 10 seconds. Every failed forward is counted by reason in `otherlode_collector_forward_failed_total` and logged at most once a minute for each cause.
- **Redaction.** The collector can blank the string literals in branch conditions and case labels, all of them or those a pattern matches. It can replace branch and site keys with keys made under a secret you set, and it clears the case keys that could give a literal away. With redaction on, it drops any field it does not know and marks the payload.
- **Environment and namespace.** The collector can set the environment and the service namespace on each payload that has none, or on every payload.
- **Limits.** A 16 MiB body cap, an element limit for each kind of payload, a request rate limit for each client address, a cap on concurrent decodes, and a cap on concurrent forwards.
- **Monitoring.** `/healthz`, Prometheus counters at `/metrics`, a `healthcheck` subcommand for container health checks, and JSON logs.
- **Configuration** through 23 `OTHERLODE_COLLECTOR_*` environment variables. A value the collector cannot use stops it at startup with a log line that names the variable.
- **The `ingest` Go package**, the collector's own handler, for a Go backend that receives agent payloads itself. It implements `ingest.Sink`.

[Unreleased]: https://github.com/otherlodehq/otherlode-collector/compare/v0.1.0...HEAD
[0.1.0]: https://github.com/otherlodehq/otherlode-collector/releases/tag/v0.1.0
