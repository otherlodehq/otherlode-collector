---
status: accepted
---

# The collector is versioned on its own and released from its tag

Decided on 2026-10-05 in a grilling session, as agent `STATUS.md`'s pre-release checklist item 6 (agent ADRs 0062 and 0063). This replaces the lockstep release agent ADR 0057 set out.

Nothing reads the collector's version but its startup log, and a newer collector serves every older agent, so the collector carries its own version: `v0.1.0` first, cut just before the agent's `0.1.0`, then a release whenever its code or its bindings change. The rule an adopter follows is "run the latest collector". The version lives in `cmd/otherlode-collector/version.go` and a release is a matching `vX.Y.Z` tag.

`scripts/release.sh X.Y.Z`, run locally, refuses without a `## [X.Y.Z]` section in `CHANGELOG.md`, then commits the version, tags it, commits the next `-SNAPSHOT` and pushes. The tag workflow refuses a tag that differs from `version.go` or names a SNAPSHOT, runs the tests, and pushes an image for `linux/amd64` and `linux/arm64` to GHCR tagged `X.Y.Z`, `X.Y` and `latest`, with a build-provenance attestation, then publishes a GitHub release whose body is the changelog section. No `X` tag while the version is `0.x`, which promises no compatibility, and no binaries: `go install` covers running it outside a container until someone asks for more.

## Considered options

- **One number with the agent (agent ADR 0057).** Rejected: an empty image and Go tag for every agent-only release, a release spanning two repositories, and at agent `1.0` and `2.0` a Go compatibility promise and a `/v2` module path for `ingest` and `metrics` that their own API had not earned.
- **Prebuilt binaries with GoReleaser.** Deferred: nobody has asked, and adding them is additive.

## Consequences

- Tags are permanent once the Go proxy has seen them; a bad release is withdrawn with `retract`, never moved.
- An agent release that changed the schema waits for a collector release carrying its bindings; the agent's release script checks it (agent ADR 0063).

Amended 2026-10-24 in a grilling session after a security audit: the image job and the docs job run in a `release` environment whose deployment rule allows only `v*` tags, as the agent's publish job does. The docs token is a secret of that environment, so a workflow on another ref cannot read it. A workflow on any ref can still ask for `packages: write` and `id-token: write` on its own token, so the environment does not bind the image push. `docs/site/deploy.md` therefore shows how to verify an image's build provenance with `gh attestation verify`, pinned to `release.yml` on a `v*` tag as the certificate identity. A personal access token for the push was rejected: it would put a long-lived secret where the workflow's own token serves.
