# Cairn Strava worker image.
#
# Build context is the repo root:
#   docker build -t cairn-provider-strava .
#
# The worker builds on github.com/opencairn-org/cairn-provider-sdk-go, which
# carries the Cairn proto stubs, so the build is a plain `go build` with NO
# buf / proto-generation step.
#
# The build stage uses the shared cairn-build-base toolchain image by default
# (pinned Go, matching cairn-core). Override BUILD_BASE to decouple from the
# core toolchain entirely, e.g.:
#   docker build --build-arg BUILD_BASE=golang:1.26-alpine -t cairn-provider-strava .
#
# A worker is a standalone process that joins the NATS control plane and
# consumes cairn.jobs.*.strava. In production it authenticates with an
# enrollment token (auth-callout); for the local dev stack it connects
# without one (workersdk.Connect).

ARG BUILD_BASE=ghcr.io/opencairn-org/cairn-build-base:1

# ---- build stage ----------------------------------------------------------
FROM ${BUILD_BASE} AS build

WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .

ARG VERSION=docker
# -trimpath and an empty build id keep the binary reproducible.
RUN CGO_ENABLED=0 GOOS=linux go build -trimpath \
    -ldflags="-s -w -buildid= -X main.workerRelease=${VERSION}" \
    -o /out/worker-strava ./cmd/worker-strava

# ---- runtime stage --------------------------------------------------------
FROM alpine:3.24@sha256:f59abe656b692d901e184be3445e084786602108599d2a51d229c0f5a48c71bf

RUN apk add --no-cache ca-certificates && adduser -D -u 10002 worker

COPY --from=build /out/worker-strava /usr/local/bin/worker-strava

USER worker
ENTRYPOINT ["worker-strava"]
