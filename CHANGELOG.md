# Changelog

All notable changes to the collector are recorded here in the
[Keep a Changelog](https://keepachangelog.com/en/1.1.0/) format.

`scripts/release.sh` and the release workflow both require a `## [X.Y.Z]` section with a body for each release.
Keep link reference definitions (`[0.1.0]: https://...`) at the bottom of the file: the first one ends
the section being read.

## [Unreleased]

### Added

- The collector accepts the agent's class listing at `/v1/otherlode/class-listing`, stamps it, strips its unknown fields while redaction is on, and forwards it like a static baseline.
- `ingest.Sink` has an `AcceptClassListing` method. A Go backend that implements `Sink` must add it.
- The bindings know `StaticBaseline.include_rules` and `ResourceAttributes.flush_interval_seconds`, so a redacting collector keeps both fields and no longer marks a payload `fields_stripped` for them.
- `ingest.MaxResourceValueBytes` is the longest identity value, in bytes, that the handler accepts.
- `ingest.MaxDeltaBatchElements`, `MaxManifestElements`, `MaxStaticBaselineElements` and `MaxClassListingElements` are the most elements a payload of each kind may hold.

### Changed

- The collector rejects a payload with a 400 when a resource string holds a NUL byte, when a time field in a delta batch or manifest is outside 0 to 253402300799999 milliseconds, or when a class that failed to initialize has no reason. `ingest.MaxTimestampMillis` is the upper bound.
- The collector rejects a payload with a 400 when `service_name`, `service_version`, `service_instance_id`, `environment`, `run_id`, `service_namespace` or `agent_version` is longer than 1024 bytes. Startup fails when `OTHERLODE_COLLECTOR_ENVIRONMENT` or `OTHERLODE_COLLECTOR_SERVICE_NAMESPACE` is longer than 1024 bytes after trimming.
- The handler counts a body's elements before it decodes it. It answers 413 `payload has too many elements` for a payload over the limit for its kind, and counts it under reason `too_many_elements`.
