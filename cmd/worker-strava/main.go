// Command worker-strava is the cairn worker for Strava. It registers
// against a cairn-server via the enrollment-token flow, then consumes
// jobs on:
//
//	cairn.jobs.fetch_source.strava   — pull one activity (backfill/webhook/reconcile)
//	cairn.jobs.parse_blob.strava     — reparse a stored blob with new worker logic
//	cairn.jobs.backfill.strava       — list-paginate then fan out fetch_source jobs
//	cairn.jobs.reconcile.strava      — pull recent activities, enqueue fetch for missing
//	cairn.jobs.import_gear.strava    — the athlete's bikes and shoes, retired included
//
// Required ENV (defaults in workersdk.Config docs):
//
//	CAIRN_NATS_URL                       e.g. nats://nats:4222
//	CAIRN_NATS_USER / CAIRN_NATS_PASSWORD  optional, for authenticated servers
//	CAIRN_WORKER_NAME                    must equal the enrollment's name
//	CAIRN_WORKER_ENROLLMENT_TOKEN        from POST /api/admin/worker-enrollments
//	CAIRN_WORKER_INSTANCE_ID             optional stable id per instance
//	CAIRN_STRAVA_WEBHOOK_VERIFY_TOKEN    set on exactly one instance
//	CAIRN_LOG_LEVEL                      debug|info|warn|error (default info)
//
// Worker version + package are compile-time constants (workerVersion /
// workerPackage) — they define the worker's schema, so they are not env-driven.
package main

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"sync"
	"syscall"
	"time"

	"github.com/opencairn-org/cairn-provider-strava/internal/capability"
	"github.com/opencairn-org/cairn-provider-strava/internal/workersdk"
)

// stravaManifest declares Strava's per-data-type capabilities — the schema-
// stable surface, identical across every deployment of this worker version. It
// carries no webhook/push flag: whether THIS instance owns the Strava webhook
// subject is a per-deployment property advertised separately in the presence
// heartbeat (set by registering WebhookEvent/WebhookVerify). Strava is
// read-only for Cairn (no write-back), supplies historical data (backfill), and
// yields activities, their laps, segment efforts, the segment definitions those
// efforts reference, gear, and provider-reported personal bests.
func stravaManifest() capability.Manifest {
	return capability.Manifest{
		capability.DataTypeActivity:      {Read: true, Backfill: true, Granularity: "per-activity"},
		capability.DataTypeLap:           {Read: true, Backfill: true},
		capability.DataTypeSegmentEffort: {Read: true, Backfill: true},
		capability.DataTypeSegment:       {Read: true, Backfill: true},
		capability.DataTypePersonalBest:  {Read: true, Backfill: true},
		capability.DataTypeGear:          {Read: true, Backfill: true, Granularity: "per-account"},
	}
}

const (
	workerName     = "strava-fetcher"
	workerProvider = "strava"
	jobStream      = "CAIRN_JOBS"

	// Versions (cairn-core ADR 0005), baked into the binary, not
	// env-overridable. datasetVersion is the archive format the importer
	// writes; bump it only when the parser can no longer read the previous
	// one. workerVersion is the parser version datasetVersion.M: bump M with
	// EVERY change to the parsed output (TestParserGolden enforces it).
	datasetVersion = 1
	workerVersion  = "1.1" // 1.1: gear id per activity
	workerPackage  = "cairns.internal.strava-importer"

	// Job kinds; the subjects are per pool (w.JobSubject, ADR 0006).
	jobFetchSource = "fetch_source"
	jobParseBlob   = "parse_blob"
	jobBackfill    = "backfill"
	jobReconcile   = "reconcile"
	jobImportGear  = "import_gear"
)

// Rate-limit buckets are per Strava app: strava:<app>:<window>, where app is
// the core-issued id of the connection's app (core reads
// <provider>.<app>.<window>). A token without an app id (older core) falls
// back to the pool's bucket strava:<pool>:<window>.
var rateBucketPool = workerName

func rateBucket(app, window string) string {
	if app == "" {
		app = rateBucketPool
	}
	return workerProvider + ":" + app + ":" + window
}

// tokenApps remembers which app each access token belongs to, so the API
// client can attribute usage headers and 429s to the right bucket.
var tokenApps sync.Map

// fetchToken is w.FetchToken plus the token→app bookkeeping.
func fetchToken(ctx context.Context, w *workersdk.Worker, accountID string) (workersdk.Token, error) {
	tok, err := w.FetchToken(ctx, accountID)
	if err == nil && tok.AccessToken != "" {
		tokenApps.Store(tok.AccessToken, tok.AppID)
	}
	return tok, err
}

func appFor(accessToken string) string {
	if v, ok := tokenApps.Load(accessToken); ok {
		return v.(string)
	}
	return ""
}

// workerRelease is the release tag, stamped at build time
// (-ldflags "-X main.workerRelease=v0.5.0"); core requires the same major.minor.
var workerRelease = ""

func main() {
	cfg, err := loadConfig()
	if err != nil {
		fmt.Fprintln(os.Stderr, "load config:", err)
		os.Exit(1)
	}
	logger := slog.New(slog.NewJSONHandler(os.Stderr, &slog.HandlerOptions{Level: cfg.LogLevel}))

	if err := run(cfg, logger); err != nil {
		logger.Error("worker exited with error", "error", err)
		os.Exit(1)
	}
}

func run(cfg workerConfig, logger *slog.Logger) error {
	rateBucketPool = cfg.WorkerName
	bus, err := connectBus(cfg, logger)
	if err != nil {
		return fmt.Errorf("connect bus: %w", err)
	}
	defer bus.Close()

	limiter := newRateLimiter(bus, logger)

	w, err := workersdk.New(workersdk.Config{
		Name:       cfg.WorkerName,
		InstanceID: cfg.InstanceID,
		Version:    workerVersion,
		Release:    workerRelease,
		Package:    workerPackage,
		Provider:   workerProvider,
		Bus:        bus,
		Limiter:    limiter,
		Auth:       NewStravaAuthHandler(),
		JobStream:  jobStream,
		Logger:     logger,
		Manifest:   stravaManifest(),
	})
	if err != nil {
		return err
	}

	client := newStravaClient()
	registerHandlers(w, cfg, client, logger)

	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()

	// Discovery responder (request/reply): the Core asks what's importable.
	discSub, err := bus.RespondTo(ctx, w.DiscoverSubject(), makeDiscoverHandler(w, client))
	if err != nil {
		return fmt.Errorf("register discover responder: %w", err)
	}
	defer func() { _ = discSub.Close(context.Background()) }()

	logger.Info("strava worker starting",
		"instance", cfg.InstanceID,
		"version", workerVersion,
		"release", workerRelease,
		"pool", w.JobSubject("*"),
	)

	return w.Run(ctx)
}

// registerHandlers wires the usage sync, the job handlers and, when this
// instance owns the webhook, the webhook handlers.
func registerHandlers(w *workersdk.Worker, cfg workerConfig, client *stravaClient, logger *slog.Logger) {
	// Sync the shared limiter to the budget Strava reports on every response —
	// authoritative capacities instead of the configured guesses, anchored to
	// Strava's real reset boundaries (quarter-hour / midnight UTC).
	client.appOf = appFor
	client.onUsage = func(app string, usedShort, limitShort, usedDaily, limitDaily int) {
		bg := context.Background()
		now := time.Now().UTC()
		_ = w.ObserveUsage(bg, rateBucket(app, "short"), usedShort, limitShort, nextQuarterHour(now))
		_ = w.ObserveUsage(bg, rateBucket(app, "daily"), usedDaily, limitDaily, nextMidnightUTC(now))
	}
	handlers := map[string]workersdk.HandlerFunc{
		jobFetchSource: makeFetchSourceHandler(cfg, client),
		jobParseBlob:   makeParseBlobHandler(cfg),
		jobBackfill:    makeBackfillHandler(cfg, client),
		jobReconcile:   makeReconcileHandler(cfg, client),
		jobImportGear:  makeImportGearHandler(cfg, client),
	}
	for job, fn := range handlers {
		if job == jobParseBlob {
			// parser_too_old hands the job back parserTooOldAttempts times.
			w.HandleWithMaxDeliver(w.JobSubject(job), fn, parserTooOldAttempts+2)
		} else {
			w.Handle(w.JobSubject(job), fn)
		}
		// Pre-0.5 subject, drained once after the upgrade. Remove in 0.6.
		w.HandleLegacy("cairn.jobs."+job+"."+workerProvider, fn)
	}

	// Webhook handlers — registered ONLY if CAIRN_STRAVA_WEBHOOK_VERIFY_TOKEN
	// is set. Without it, this worker instance doesn't claim Strava's
	// webhook subject and Strava verify requests will time out at the
	// server (so don't run multiple Strava workers with this token set;
	// only one should own the webhook subject).
	if os.Getenv("CAIRN_STRAVA_WEBHOOK_VERIFY_TOKEN") != "" {
		w.WebhookEvent(handleWebhookEvent)
		w.WebhookVerify(handleWebhookVerify)
		logger.Info("strava webhook handlers registered")
	}
}
