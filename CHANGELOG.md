# Changelog

All notable changes to the collector are recorded here in the
[Keep a Changelog](https://keepachangelog.com/en/1.1.0/) format.

`scripts/release.sh` and the release workflow both require a `## [X.Y.Z]` section with a body for each release.
Keep link reference definitions (`[0.1.0]: https://...`) at the bottom of the file: the first one ends
the section being read.

## [Unreleased]

### Changed

- The collector knows the agent's `manifest_confirmed` resource attribute. With redaction on, an older collector drops the field once the agent sets it, which is after the run's first confirmed manifest, and marks each payload `fields_stripped` from then on. Run this collector before an agent that sends the field.

## [0.1.0] - 2026-11-01

The first release of the Otherlode collector, an optional service that receives what Otherlode agents send. It checks each payload, can redact it, and forwards it to Otherlode. It stores nothing. Docs: https://otherlode.dev/docs.

### Added

- **The image**, `ghcr.io/otherlodehq/otherlode-collector:0.1.0` (also `0.1` and `latest`), for `linux/amd64` and `linux/arm64`. It runs as non-root on a distroless base, listens on port 4319, and carries a build provenance attestation.
- **No queue.** It answers `202` only once Otherlode has stored a payload. When Otherlode is busy, down or unreachable it answers `429` or `503` with `Retry-After`, and the agent sends again. Any other `4xx` passes through. Without `OTHERLODE_COLLECTOR_FORWARD_URL` it only logs payloads.
- **Agent tokens.** It refuses to start without one unless you set `OTHERLODE_COLLECTOR_INSECURE_NO_AUTH`. A token file is re-read every 30 seconds.
- **Redaction.** It blanks string literals in branch conditions and case labels, all of them or those a pattern matches, and re-keys branch and site keys with a secret of at least 32 bytes. With redaction on it drops fields it doesn't know and marks the payload. Otherlode can refuse any payload that didn't pass through your redacting collector.
- **Checks and limits** on body size, element counts, request rate and concurrency, applied before a payload is forwarded.
- **Monitoring** through `/healthz`, Prometheus metrics at `/metrics`, a `healthcheck` subcommand and JSON logs.
- **Configuration** through `OTHERLODE_COLLECTOR_*` environment variables. A value it can't use stops startup with an error that names the variable.
- **The `ingest` Go package**, with the collector's own `ingest.Handler` and the `ingest.Sink` interface your backend implements.

[Unreleased]: https://github.com/otherlodehq/otherlode-collector/compare/v0.1.0...HEAD
[0.1.0]: https://github.com/otherlodehq/otherlode-collector/releases/tag/v0.1.0
