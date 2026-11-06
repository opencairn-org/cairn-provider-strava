# Cairn Strava worker (Go)

Imports Strava activities (and their laps, segment efforts, segment
definitions, gear, and provider-reported personal bests) into Cairn. Strava is
read-only for Cairn — no write-back.

It speaks Cairn's NATS control plane: registers via the enrollment-token flow,
then consumes jobs on:

```
cairn.jobs.fetch_source.strava.<name>   pull one activity (backfill/webhook/reconcile)
cairn.jobs.parse_blob.strava.<name>     reparse a stored blob with new worker logic
cairn.jobs.backfill.strava.<name>       list-paginate, fan out fetch_source jobs
cairn.jobs.reconcile.strava.<name>      pull recent activities, enqueue fetch for missing
cairn.jobs.import_gear.strava.<name>    the athlete's bikes and shoes, retired included
cairn.discover.strava.<name>            (request/reply) what's importable
cairn.oauth.strava.<name>.exchange      (request/reply) trade a connect-flow code for tokens
```

`<name>` is the pool name (`CAIRN_WORKER_NAME`). It publishes
`cairn.worker.v1.JobResult`, heartbeats its capability manifest to the
`cairn_worker_presence` KV and declares how Strava is connected (authorize
endpoint, scopes, credential labels) in the `cairn_worker_manifests` KV, so
core needs no Strava knowledge of its own.

## Built on the provider SDK

The worker is built on
[cairn-provider-sdk-go](https://github.com/opencairn-org/cairn-provider-sdk-go),
which implements the contract (NATS subjects, job and result shapes, the
proto stubs) and the worker loop; this repository holds only the Strava
client, the mapping and the handlers. There is no Go-code dependency on
cairn-core and no buf / proto-generation step at build time: a proto change
is a new SDK release. The SDK's major.minor follows cairn-core's, and so
does this worker's: 0.7.x runs against core 0.7.

The Docker build uses the shared `cairn-build-base` toolchain image by
default (pinned Go 1.26, consistent with cairn-core). That's only a *toolchain*
dependency, and it's overridable — build with zero coupling to the core image
via `docker build --build-arg BUILD_BASE=golang:1.26-alpine .`.

## Layout

```
cmd/worker-strava/   the Strava worker (main, handlers, API client, mapping, auth, webhooks)
```

## Run (dev)

```bash
export CAIRN_NATS_URL=nats://localhost:4222
export CAIRN_WORKER_NAME=strava-fetcher
export CAIRN_WORKER_ENROLLMENT_TOKEN=...   # from POST /admin/worker-enrollments
go run ./cmd/worker-strava
```

Against a mock Strava (`cmd/mock-strava`, or the SDK's e2e harness) point the
worker at it instead of strava.com:

```bash
go run ./cmd/mock-strava --listen 127.0.0.1:8099 &
export CAIRN_STRAVA_API_BASE_URL=http://127.0.0.1:8099/api/v3
export CAIRN_STRAVA_OAUTH_BASE_URL=http://127.0.0.1:8099/oauth   # authorize + token
```

`CAIRN_STRAVA_TOKEN_URL` still overrides the token endpoint alone. The mock
auto-approves the authorize round-trip, serves seeded rides (laps, streams,
gear) and takes test directions on `/_mock/*` (`--help` lists the flags).

## Versioning

Three versions, defined in [core ADR 0005](https://github.com/opencairn-org/cairn-core/blob/main/docs/adr/0005-provider-versioning.md)
and the [provider contract](https://docs.opencairn.org/architecture/provider-contract#versioning):

- **Release** (`vX.Y.Z`, the git tag): before 1.0 major.minor must match
  core; from 1.0 on, same major and the worker's minor not above core's
  (minor and patch releases are backwards compatible). The worker refuses to
  start against an incompatible core.
- **Dataset** (`N`, the archive envelope `version`): bump only when the parser
  can no longer read the previous archive format.
- **Parser** (`N.M`, sent as `worker_version`): bump `M` with **every** change
  to the parsed output; a dataset bump resets it to `N.0`. Parser `N.x` must
  read every dataset `N` archive. Versions only move forward.
  `TestParserGolden` pins the parsed output of `cmd/worker-strava/testdata/archives`
  per parser version; after a bump, `go test ./cmd/worker-strava -run
  TestParserGolden -update` writes the new goldens (delete the old ones).

Jobs are routed per pool (core ADR 0006): this worker consumes
`cairn.jobs.<job>.strava.<CAIRN_WORKER_NAME>` and publishes results on
`cairn.results.<job>.strava.<CAIRN_WORKER_NAME>`. Instances sharing a name
share the work; a second name is a separate pool that core assigns accounts
to. The name is permanent: core binds it to this package, so a different
implementation needs a new name. Rate-limit budgets are per Strava app: core
names the connection's app in the token reply (`app_id`), and the buckets are
`strava:<app>:short|daily|webhook` in the shared KV (a token without an app
id uses the pool's bucket `strava:<name>:…`).

## Develop

`make check` is the definition of green (lint, race tests, coverage floor,
govulncheck, licenses, gitleaks, build, fleetlint); `make tools` installs the
pinned tools and `make help` lists the targets. Build the image with
`make image` (`CONTAINER=podman make image` without Docker). See
[CONTRIBUTING.md](CONTRIBUTING.md) and [docs/ARCHITECTURE.md](docs/ARCHITECTURE.md).
