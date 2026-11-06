# Changelog

All notable changes to the Cairn Strava worker are documented here. The format
follows [Keep a Changelog](https://keepachangelog.com/), and the project aims
to follow [Semantic Versioning](https://semver.org/). Dates are ISO-8601.

## [Unreleased]

## [0.7.1] — 2026-11-06

Built on [cairn-provider-sdk-go](https://github.com/opencairn-org/cairn-provider-sdk-go)
v0.7.1, released in lockstep with cairn-core 0.7.1. Works with any core 0.7.

### Added
- `CAIRN_STRAVA_API_BASE_URL` and `CAIRN_STRAVA_OAUTH_BASE_URL` point the API
  client and the authorize/token endpoints at a stand-in (mock, e2e runs);
  `CAIRN_STRAVA_TOKEN_URL` keeps precedence for the token endpoint alone.
- `cmd/mock-strava` (`internal/mockstrava`): a standalone mock Strava for
  end-to-end runs — auto-approving OAuth with refresh, seeded rides with
  laps, streams and gear, Strava-shaped rate-limit headers, and a `/_mock/*`
  control API (add activities, emit webhooks, inject 429s, stats).

### Changed
- The worker is built on
  [cairn-provider-sdk-go](https://github.com/opencairn-org/cairn-provider-sdk-go):
  `internal/{workersdk,nats,capability,port,inmem,config}`, the committed
  proto stubs and `scripts/sync-proto.sh` are gone, and the generic parts of
  `cmd/worker-strava` (NATS connect, rate limiter, claim-check, failure
  envelope, archive upload/download, job payloads, parser guard, reconcile
  window, discover) call the SDK. Behaviour on the wire is unchanged.

### Fixed
- Rate limits (from the SDK): a deleted or purged rate-limit key (`nats kv
  del`, a reset) no longer denies every reservation of that window until the
  bucket's TTL runs out.
- Presence and manifest are published only once every job consumer and the
  webhook subscriber exist, so a heartbeat means the worker takes work.

### Removed
- The weekly mutation-testing job and `make mutate`: the SDK package it
  targeted now lives in the SDK repository.

## [0.7.0] — 2026-11-05

Contract change: needs cairn-core 0.7 (docs/adr/0005).

### Added
- The manifest declares how Strava is connected (`provider_meta`: display
  name, authorize endpoint, scopes, `approval_prompt=force`, credential
  labels, activity link, setup docs); core builds the connect flow from it.
- The pool answers core's authorization-code exchange on
  `cairn.oauth.strava.<name>.exchange` (`StravaAuthHandler.Exchange`); the
  reply carries the tokens and the athlete id.

### Removed
- The pre-0.5 drain of `cairn.jobs.<job>.strava`; core 0.7 no longer grants
  those subjects (`HandleLegacy` is gone from the SDK).

### Changed
- Core 0.7 scopes `cairn.tokens.strava.*` and the presign subjects to the
  account's/blob's provider and checks blob keys; no change needed here, the
  worker already stays within its own keys.

## [0.6.0] — 2026-10-29

Released alongside cairn-core 0.6.0. The worker contract (`cairn.worker.v1`)
is unchanged since 0.5.0, so 0.5.0 workers keep working against a 0.6.0 core
and this release needs no core upgrade first.

### Added
- A v* tag now also publishes the GitHub release: the tag's CHANGELOG.md
  section as notes plus the image digest and verify commands, with an SPDX
  SBOM of that digest (syft) attached. Before, the release object was created
  by hand (and skipped for 0.5.0).

### Changed
- Dependency updates (fleetlint 0.8.1, CodeQL action).

## [0.5.0] — 2026-10-25

Needs cairn-core 0.5.0 (per-pool subjects and the release check; the Core
must be upgraded first). Upgrade the worker in place: it drains jobs queued
on the 0.4 subjects once.

### Added
- Parser version `1.1` (gear per activity, added in 0.4.0 without a bump) on
  dataset 1; `TestParserGolden` pins the parsed output per parser version.
  `parse_blob` refuses archives of another dataset and hands back jobs whose
  `min_parser_version` is newer, backing off for about an hour before
  reporting `parser_too_old`.
- The release is stamped at build time and sent in presence and results; the
  worker refuses to run against an incompatible core (same major.minor before
  1.0, same major and minor ≤ core's from 1.0).
- Rate-limit buckets per Strava app (`app_id` from the token reply,
  `strava:<app>:<window>`); a token without an app id uses the pool's bucket.

### Changed
- Jobs, discovery, results and the account lookup are per pool:
  `cairn.jobs.<job>.strava.<name>`, `cairn.discover.strava.<name>`,
  `cairn.results.<job>.strava.<name>`, `cairn.accounts.lookup.strava.<name>`.
  Sub-jobs from reconcile and webhooks go to the own pool.

### Fixed
- 4xx Strava responses other than 429 (e.g. 403 application inactive) fail
  the job instead of being retried forever.

## [0.4.0] — 2026-10-21

Needs cairn-core 0.4.0 or later (gear events and the `import_gear` job are
new in the contract; the Core must be upgraded first).

### Added
- Gear. Every fetched activity carries its Strava `gear_id` and gear name,
  preceded by a GEAR event built from the activity's gear summary, so the
  Core can map the record to the user's gear (or ask them to). The
  `import_gear` job answers with the athlete's full bike and shoe list,
  retired included, with brand, model and description from `GET /gear/{id}`.
  Archived activities carry the gear summary, so a reparse backfills gear
  without API calls. The manifest now declares `Gear` with backfill.

## [0.3.2] — 2026-10-19

### Added
- `make mutate`: gremlins mutation testing of `internal/workersdk` with an efficacy floor, run weekly in CI.
- Code of conduct (Contributor Covenant) and an ADR log under `docs/adr`.

### Changed
- The project moved to the `opencairn-org` GitHub organization. The image is
  published as `ghcr.io/opencairn-org/cairn-provider-strava`; the
  `ghcr.io/johnnycube` namespace keeps the tags published so far as a frozen
  mirror and receives no new versions, so image pins should move. The Go
  module path is `github.com/opencairn-org/cairn-provider-strava`.

## [0.3.1] — 2026-10-15

### Changed
- With `CAIRN_WORKER_ENROLLMENT_TOKEN` set, the worker presents only the token
  on CONNECT and no longer generates an ephemeral nkey: nats.conf no longer
  needs a placeholder nkey user. Needs cairn-core 0.3.1 or later for the
  auth-callout, which binds the user JWT to the nkey nats-server assigns.

## [0.3.0] — 2026-10-15

Released alongside Cairn v0.3.0; no worker code changes since 0.2.6.

### Changed
- Under NATS auth (enrolled workers), this worker needs cairn-core 0.3.0 or
  later: earlier worker permission templates refused its job consumers.

## [0.2.6] — 2026-10-15

### Fixed
- Publishing to a subject outside any JetStream stream that has a core NATS
  subscriber no longer times out after five seconds (and gets delivered twice
  on retry): the bus looks up whether a stream covers the subject, caches the
  answer, and picks JetStream or core publish up front.
- The webhook event loop waits a second after a failed fetch instead of
  retrying in a tight loop; the job pull loop's wait now ends on shutdown.

### Changed
- The repository is onboarded to fleetlint: `make check` (golangci-lint, race
  tests with a 90% coverage floor, govulncheck, go-licenses, gitleaks, build,
  fleetlint) gates CI and the image build, CodeQL runs on every change, and
  workflow actions are pinned by SHA. Release tags must be on main.
- Backfill and reconcile share their listing and queueing helpers, large
  handlers are split, and int32 conversions saturate instead of wrapping.
- The image is built with `-trimpath` and an empty build id, and the alpine
  base is pinned by digest. go.mod pins `toolchain go1.26.9`.
- Dependency updates: klauspost/compress 1.20.1 (indirect); nats-server is
  added as a test-only dependency.

## [0.2.5] — 2026-09-29

### Changed
- Dependency updates: nats.go 1.54.0, which lifts the indirect
  golang.org/x/crypto to 0.57.0, golang.org/x/sys to 0.48.0 and
  klauspost/compress to 1.20.0. No changes to the worker code.

## [0.2.4] — 2026-09-14

### Fixed
- `CAIRN_LOG_LEVEL` is honoured; the logger was hard-wired to info.
- Strava `invalid_client` and `unauthorized_client` token errors are reported
  as such instead of being classified as `invalid_grant`.

### Changed
- Webhook events with an unknown `aspect_type`, failed account lookups, and a
  missing rate-limiter KV bucket are logged instead of passing silently;
  backfill and reconcile log how many fetches they enqueued.
- Worker instance IDs default to a random hex id instead of a timestamp.
- The job and webhook loops share one ack/term/nak path, and photo mirroring
  reuses the presigned-upload helper.

### Removed
- Adapter code carried over from cairn-core that the worker never called:
  connection and TLS setup, stream bootstrap, the push-consumer path, the
  object store, unused KV and capability helpers, and the `CAIRN_NATS_*`
  cluster, credential, TLS, reconnect and retention config fields.
- `tokenCache.Invalidate`: a 401 already terminates the job as
  `needs_reauth`, and a re-fetch would return the same revoked token.

## [0.2.3] — 2026-09-11

### Changed
- Release images are signed keyless with cosign (Fulcio certificate bound to
  this repository's release workflow) and ship SLSA provenance and SBOM
  attestations; verify with `cosign verify` as documented in the workflow
  header. No functional change to the worker.
- Dependencies: `nats.go` 1.53.1, `protobuf` 1.36.12; GitHub Actions moved to
  the current majors of checkout, cache, and the docker build actions.

### Removed
- The Gitea mirror workflow — the repository is published from GitHub only.

## [0.2.2] — 2026-07-20

### Fixed
- GitHub Actions: the containerized test job now marks the checkout as a git
  `safe.directory`, fixing the "error obtaining VCS status" build failure that
  broke the v0.2.1 release pipeline. No code changes since 0.2.1 — this release
  exists to publish the images that release never produced.

## [0.2.1] — 2026-07-20

### Changed
- The worker re-publishes its manifest to `cairn_worker_manifests` every 5
  minutes (previously only at startup), keeping it fresh within the manifest
  bucket's new TTL so a live worker's manifest is never expired or reaped.
  Requires cairn-core ≥ v0.2.1.

## [0.2.0] — 2026-07-16

**First public release.** A standalone Go worker that imports Strava
activities into [Cairn](https://github.com/johnnycube/cairn-core) — read-only, no
write-back.

### Added
- Full activity import: activities with streams, laps, segment efforts,
  segment definitions, gear, and provider-reported personal bests.
- Cairn NATS control plane: enrollment-token registration, durable consumers
  for `fetch_source` / `parse_blob` / `backfill` / `reconcile` jobs, a
  `discover` request/reply endpoint, capability-manifest heartbeats to the
  worker-presence KV.
- Claim-checked results: event payloads upload to the blob store via presigned
  PUT and publish as small envelopes; terminal failures publish a fail-fast
  failure envelope with the true reason.
- Strava rate-limit handling driven by authoritative usage headers, with a
  reserved daily budget so reconcile can't starve user-driven imports.
- Webhook support and blob re-parse (`parse_blob`) so stored raw responses can
  be re-imported without spending API quota.
- No Go-code dependency on cairn-core: the shared contract is the committed
  proto stubs under `proto/` plus the NATS subjects.

## [0.1.0] — 2026-06-26

Initial development (internal pre-release iterations v0.1.0–v0.1.5).

[0.3.2]: https://github.com/opencairn-org/cairn-provider-strava/releases/tag/v0.3.2
[0.2.1]: https://github.com/johnnycube/cairn-provider-strava/releases/tag/v0.2.1
[0.2.0]: https://github.com/johnnycube/cairn-provider-strava/releases/tag/v0.2.0
