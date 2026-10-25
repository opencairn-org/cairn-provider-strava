# Cairn Strava worker (Go)

Imports Strava activities (and their laps, segment efforts, segment
definitions, gear, and provider-reported personal bests) into Cairn. Strava is
read-only for Cairn — no write-back.

It speaks Cairn's NATS control plane: registers via the enrollment-token flow,
then consumes jobs on:

```
cairn.jobs.fetch_source.strava   pull one activity (backfill/webhook/reconcile)
cairn.jobs.parse_blob.strava     reparse a stored blob with new worker logic
cairn.jobs.backfill.strava       list-paginate, fan out fetch_source jobs
cairn.jobs.reconcile.strava      pull recent activities, enqueue fetch for missing
cairn.jobs.import_gear.strava    the athlete's bikes and shoes, retired included
cairn.discover.strava            (request/reply) what's importable
```

It publishes `cairn.worker.v1.JobResult` and heartbeats its capability
manifest to the `cairn_worker_presence` KV.

## Standalone

This is a **standalone repository** with no Go-code dependency on cairn-core.
The only contract it shares with the server is the proto messages (committed Go
stubs under `proto/`) and the NATS subjects — the same wire contract the Python
Garmin worker uses. There is no buf / proto-generation step at build time.

The Docker build uses the shared `cairn-build-base` toolchain image by
default (pinned Go 1.26, consistent with cairn-core). That's only a *toolchain*
dependency, and it's overridable — build with zero coupling to the core image
via `docker build --build-arg BUILD_BASE=golang:1.26-alpine .`.

When the proto contract changes: run `make proto` in cairn-core, then
`scripts/sync-proto.sh ../cairn-core` here and commit the regenerated stubs.

## Layout

```
cmd/worker-strava/   the Strava worker (main, handlers, API client, mapping…)
internal/workersdk/  the Go worker SDK (job loop, heartbeat, token cache)
internal/nats/       worker-side NATS client (bus + KV rate limiter)
internal/capability/ capability manifest + datatype taxonomy
internal/port/       the interfaces the worker consumes (JobBus, RateLimiter…)
internal/config/     NATS connection settings
internal/inmem/      in-memory bus for tests
proto/               committed generated Go proto stubs (the contract)
```

## Run (dev)

```bash
export CAIRN_NATS_URL=nats://localhost:4222
export CAIRN_WORKER_NAME=strava-fetcher
export CAIRN_WORKER_ENROLLMENT_TOKEN=...   # from POST /admin/worker-enrollments
go run ./cmd/worker-strava
```

## Versioning

Three versions, defined in [core ADR 0005](https://github.com/opencairn-org/cairn-core/blob/main/docs/adr/0005-provider-versioning.md)
and the [provider contract](https://docs.opencairn.org/architecture/provider-contract#versioning):

- **Release** (`vX.Y.Z`, the git tag): major.minor must match core.
- **Dataset** (`N`, the archive envelope `version`): bump only when the parser
  can no longer read the previous archive format.
- **Parser** (`N.M`, sent as `worker_version`): bump `M` with **every** change
  to the parsed output; a dataset bump resets it to `N.0`. Parser `N.x` must
  read every dataset `N` archive. Versions only move forward.
  `TestParserGolden` pins the parsed output of `cmd/worker-strava/testdata/archives`
  per parser version; after a bump, `go test ./cmd/worker-strava -run
  TestParserGolden -update` writes the new goldens (delete the old ones).

Jobs are routed per pool (core ADR 0006): this worker consumes
`cairn.jobs.<job>.strava.<CAIRN_WORKER_NAME>`. Instances sharing a name share
the work; a second name is a separate pool that core assigns accounts to.

## Develop

`make check` is the definition of green (lint, race tests, coverage floor,
govulncheck, licenses, gitleaks, build, fleetlint); `make tools` installs the
pinned tools and `make help` lists the targets. Build the image with
`make image` (`CONTAINER=podman make image` without Docker). See
[CONTRIBUTING.md](CONTRIBUTING.md) and [docs/ARCHITECTURE.md](docs/ARCHITECTURE.md).
