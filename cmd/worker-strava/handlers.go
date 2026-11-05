package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"time"

	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/types/known/timestamppb"

	"github.com/opencairn-org/cairn-provider-sdk-go/natsbus"
	"github.com/opencairn-org/cairn-provider-sdk-go/port"
	workerv1 "github.com/opencairn-org/cairn-provider-sdk-go/proto/cairn/worker/v1"
	"github.com/opencairn-org/cairn-provider-sdk-go/workersdk"
)

// workerConfig is the environment the SDK reads (CAIRN_*); loadConfig
// adds nothing Strava-specific.
type workerConfig = workersdk.EnvConfig

func loadConfig() (workerConfig, error) { return workersdk.LoadEnv(workerName) }

// ---------------------------------------------------------------------------
// Rate limiter
// ---------------------------------------------------------------------------

// newRateLimiter returns nil (no local limiting) when the shared KV bucket
// is unreachable, so a missing bucket degrades to relying on upstream 429s.
func newRateLimiter(bus port.JobBus, logger *slog.Logger) port.RateLimiter {
	return workersdk.NewRateLimiter(bus, logger, map[string]natsbus.Limit{
		// Fallback guesses only: Strava's default READ limits for
		// non-approved apps. The authoritative values come from the
		// X-ReadRateLimit-* headers via SyncUsage on every API response.
		// Keyed per pool: per-app buckets are learned from the first usage
		// headers of each app. Windows follow the bucket suffix (15 min / day).
		rateBucket("", "short"):   {Capacity: 100},
		rateBucket("", "daily"):   {Capacity: 1000},
		rateBucket("", "webhook"): {Capacity: 20},
	})
}

// ===========================================================================
// Handlers
//
// Each handler decodes its job body, runs the provider-specific logic
// (HTTP calls via stravaClient, mapping via mapping.go), and publishes
// a result on the reply subject. Errors map onto workersdk's three
// flavors:
//
//	*port.TerminalError       → JetStream Term (DLQ)   — bad payload, needs_reauth
//	*port.NakWithDelayError   → NakWithDelay           — rate-limit hit
//	any other error           → NAK (backoff applies)  — transient / 5xx
//
// Returning nil = success → ACK.
// ===========================================================================

// ---------------------------------------------------------------------------
// discover.strava: request/reply — list all importable activity ids+times
//
// The Core asks "what can you import for this account?"; the worker lists
// activity summaries (cheap, no streams) and returns ids + start times. The
// Core summarizes, lets the user choose, and fills the persisted import queue.
// ---------------------------------------------------------------------------

// The wire shapes are the SDK's; Complete is true only once the LAST page
// (a 0-item page) has been reached — a short (<200) page is NOT the end on
// Strava.
type (
	discoverItem     = workersdk.DiscoverItem
	discoverResponse = workersdk.DiscoverResponse
)

// discoverMaxPagesPerCall bounds how many pages one request/reply walks, so a
// single call stays well under the NATS request timeout. The server stitches
// calls together via NextPage.
const discoverMaxPagesPerCall = 25

func makeDiscoverHandler(w *workersdk.Worker, client *stravaClient) workersdk.DiscoverFunc {
	return func(ctx context.Context, req workersdk.DiscoverRequest) discoverResponse {
		token, err := fetchToken(ctx, w, req.AccountID)
		if err != nil {
			return discoverResponse{Error: err.Error()}
		}
		return discoverPages(ctx, w, client, token.AccessToken, max(req.StartPage, 1))
	}
}

// discoverPages walks up to discoverMaxPagesPerCall listing pages from start.
func discoverPages(ctx context.Context, w *workersdk.Worker, client *stravaClient, accessToken string, start int) discoverResponse {
	const perPage = 200
	var items []discoverItem
	for i := 0; ; i++ {
		page := start + i
		if err := reserveRead(ctx, w, rateBucket(appFor(accessToken), "short")); err != nil {
			return discoverReserveFailed(items, page, err)
		}
		sums, err := client.ListAthleteActivities(ctx, accessToken, 0, page, perPage)
		if err != nil {
			if len(items) == 0 {
				return discoverResponse{Error: err.Error()}
			}
			// Transient mid-batch error: hand back what we have and let the
			// server retry from this page.
			return discoverResponse{Items: items, NextPage: page}
		}
		// Empty page = end of data. This is the ONLY stop condition — a
		// short (<200) page is NOT the last page on Strava.
		if len(sums) == 0 {
			return discoverResponse{Items: items, Complete: true}
		}
		for _, s := range sums {
			items = append(items, discoverItem{
				ItemType:   "activity",
				ExternalID: formatActivityID(s.ID),
				ItemTime:   s.StartDate,
			})
		}
		if i+1 >= discoverMaxPagesPerCall {
			// Batch boundary (not rate-limited) — server requests NextPage
			// immediately.
			return discoverResponse{Items: items, NextPage: page + 1}
		}
	}
}

// discoverReserveFailed answers a discover call whose budget reservation failed.
func discoverReserveFailed(items []discoverItem, page int, err error) discoverResponse {
	var nak *port.NakWithDelayError
	rateLimited := errors.As(err, &nak)
	switch {
	case rateLimited && len(items) == 0 && page == 1:
		// On the very first page with nothing yet, surface a clean error so a
		// preview shows "try again shortly".
		return discoverResponse{Error: "rate_limited: provider budget exhausted, try again shortly"}
	case !rateLimited && len(items) == 0:
		return discoverResponse{Error: err.Error()}
	default:
		return discoverResponse{Items: items, NextPage: page, RateLimited: true}
	}
}

// ---------------------------------------------------------------------------
// fetch_source.strava: pull one activity + its streams, publish result
// ---------------------------------------------------------------------------

type fetchSourceJob = workersdk.FetchSourceJob

// reserveRead reserves one read from the provider's DAILY budget and then
// from the per-window bucket. Every Strava call counts against both real
// windows; reserving only the short bucket let the worker keep firing
// requests (each a guaranteed 429) after the daily budget was spent. The
// daily bucket's reset is anchored to midnight UTC by the usage-header sync,
// so exhaustion yields a NakWithDelay that sleeps until the budget is real.
func reserveRead(ctx context.Context, w *workersdk.Worker, bucket string) error {
	if err := w.ReserveAPI(ctx, siblingBucket(bucket, "daily"), 1); err != nil {
		return err
	}
	return w.ReserveAPI(ctx, bucket, 1)
}

// siblingBucket is the same app's bucket for another window
// (strava:<app>:short → strava:<app>:daily).
func siblingBucket(bucket, window string) string {
	return bucket[:strings.LastIndex(bucket, ":")+1] + window
}

// segmentCache memoises fetched segment geometry (SegmentImport) by Strava
// segment id, so a segment traversed by many activities is fetched once.
type segmentCache struct {
	mu sync.Mutex
	m  map[string]*workerv1.SegmentImport
}

func newSegmentCache() *segmentCache { return &segmentCache{m: map[string]*workerv1.SegmentImport{}} }

func (c *segmentCache) get(id string) (*workerv1.SegmentImport, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	s, ok := c.m[id]
	return s, ok
}

func (c *segmentCache) put(id string, s *workerv1.SegmentImport) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.m[id] = s
}

func makeFetchSourceHandler(client *stravaClient) func(context.Context, *workersdk.Worker, workersdk.Job) error {
	// Shared across every fetch_source job this handler processes: a segment's
	// geometry (polyline) is fetched at most once per worker lifetime, since
	// segments are effectively immutable. Bounds the extra API load.
	segCache := newSegmentCache()
	return func(ctx context.Context, w *workersdk.Worker, job workersdk.Job) error {
		var in fetchSourceJob
		if err := json.Unmarshal(job.Body, &in); err != nil {
			return &port.TerminalError{Reason: "bad_payload", Cause: err}
		}
		if in.ExtID == "" {
			return &port.TerminalError{Reason: "missing_ext_id"}
		}

		// A terminal failure publishes a small failure result first (best-
		// effort) so the server fails the queue item with the true reason —
		// Term alone leaves it to the stale reaper's masked "no result".
		err := fetchSourceOnce(ctx, w, client, segCache, job, in)
		var term *port.TerminalError
		if errors.As(err, &term) {
			publishFailureResult(ctx, w, job, in, term.Reason, term.Cause)
		}
		return err
	}
}

// fetchSourceOnce is one fetch_source attempt: pull the activity (+streams,
// segments), archive the raw JSON, and publish the claim-checked result.
func fetchSourceOnce(ctx context.Context, w *workersdk.Worker, client *stravaClient, segCache *segmentCache, job workersdk.Job, in fetchSourceJob) error {
	token, err := fetchToken(ctx, w, in.AccountID)
	if err != nil {
		return err // already a TerminalError or NakWithDelay
	}

	window := "short"
	if in.Reason == "webhook" {
		window = "webhook"
	}
	bucket := rateBucket(token.AppID, window)
	if err := reserveRead(ctx, w, bucket); err != nil {
		return err
	}

	activity, err := client.GetActivity(ctx, token.AccessToken, in.ExtID)
	if err != nil {
		return mapStravaErr(ctx, w, job, in, err, "fetch_activity")
	}

	rawStreams, err := fetchStreams(ctx, w, client, job, in, token.AccessToken, bucket)
	if err != nil {
		return err
	}

	imported := &workerv1.ImportedActivity{
		Ref:     externalRef(in, in.ExtID),
		Payload: mapActivityToPayload(activity),
		Stream:  mapStreamsToProto(rawStreams),
	}

	// Archive the raw fetched JSON (+streams) so this source can later be
	// re-parsed (or re-downloaded by the user) WITHOUT spending provider API
	// budget. Best-effort: a failure (no blob store, presign timeout) leaves
	// raw_blob_id empty — the import is durable regardless.
	if id, size, aerr := archiveActivityBlob(ctx, w, in.UserID, activity, rawStreams); aerr != nil {
		w.Logger().Warn("archive activity blob failed", "ext_id", in.ExtID, "error", aerr)
	} else {
		imported.RawBlobId, imported.RawContentType, imported.RawSizeBytes = id, "application/json", size
	}

	// Mirror the activity's photos into blob storage so Cairn owns the bytes
	// (Strava photo URLs are short-lived CDN links). Best-effort.
	imported.Attachments = fetchActivityAttachments(ctx, w, client, token.AccessToken, bucket, in.UserID, in.ExtID, activity)

	events := activityEvents(in, activity, imported)

	var segs segmentBatch
	if in.FetchStreams {
		segs = collectSegments(ctx, w, client, segCache, token.AccessToken, bucket, in, activity)
	}

	// Publish the activity (+stream) on its own, claim-checked through the
	// blob store — the NATS message is just the envelope, so stream size
	// no longer matters. Kept separate from segments so a mid-segment rate
	// limit can't fail the durable activity. The msg-id carries the
	// delivery attempt: a deferred segment retry re-publishes on a later
	// delivery and must not be swallowed by the stream's dedup window
	// (ingest dedups idempotently).
	msgID := fmt.Sprintf("result:%s:d%d", in.JobID, job.DeliveryAttempt)
	if err := publishResultViaBlob(ctx, w, job, msgID, newJobResult(w, events...)); err != nil {
		return err
	}

	publishSegments(ctx, w, job, msgID+":seg", segs)

	// Rate-limited mid-segments: the activity above is durable; retry the
	// job after the window so the remaining segments complete. Cached
	// segments make the retry pass cheap (refetch of the activity + streams
	// is the unavoidable cost of completing it).
	if segs.retryAfter > 0 {
		return &port.NakWithDelayError{Reason: "rate_limited_segments", Delay: segs.retryAfter}
	}
	return nil
}

// fetchStreams pulls the activity's streams. They are optional: the reconcile
// path skips them by setting FetchStreams=false (the per-activity fetch pulls
// them on the user's first detail view), and Strava returns 404 when the
// activity has no GPS or stream data at all.
func fetchStreams(ctx context.Context, w *workersdk.Worker, client *stravaClient, job workersdk.Job, in fetchSourceJob, accessToken, bucket string) (stravaStreamsResponse, error) {
	if !in.FetchStreams {
		return nil, nil
	}
	if err := reserveRead(ctx, w, bucket); err != nil {
		return nil, err
	}
	streams, err := client.GetActivityStreams(ctx, accessToken, in.ExtID)
	switch {
	case errors.Is(err, ErrStravaNotFound):
		return nil, nil
	case err != nil:
		return nil, mapStravaErr(ctx, w, job, in, err, "fetch_streams")
	}
	return streams, nil
}

// activityEvents is the activity's event list: the GEAR event for the gear
// it was recorded with (from the DetailedActivity summary, no extra call)
// first, so the Core can resolve the reference, then the activity itself.
func activityEvents(in fetchSourceJob, activity *stravaActivity, imported *workerv1.ImportedActivity) []*workerv1.WorkerEvent {
	var events []*workerv1.WorkerEvent
	if activity.Gear != nil && activity.Gear.ID != "" {
		events = append(events, gearEvent(externalRef(in, activity.Gear.ID), mapGearSummary(activity.Gear)))
	}
	return append(events, &workerv1.WorkerEvent{
		Type:   workerv1.WorkerEventType_WORKER_EVENT_TYPE_ACTIVITY,
		Entity: &workerv1.WorkerEvent_Activity{Activity: imported},
	})
}

// externalRef builds the ref for one entity of the job's account.
func externalRef(in fetchSourceJob, extID string) *workerv1.ExternalRef {
	return &workerv1.ExternalRef{
		UserId:            in.UserID,
		Provider:          workerProvider,
		ExternalAccountId: in.AccountID,
		ExternalId:        extID,
	}
}

// newJobResult stamps events with this worker's identity (name, parser
// version, package and release from the worker's config).
func newJobResult(w *workersdk.Worker, events ...*workerv1.WorkerEvent) *workerv1.JobResult {
	return w.NewJobResult(events...)
}

// segmentBatch is the segments and efforts of one activity, plus how long to
// wait before retrying when a rate limit cut the collection short.
type segmentBatch struct {
	segments   []*workerv1.WorkerEvent
	efforts    []*workerv1.WorkerEvent
	retryAfter time.Duration
}

// collectSegments builds segment and effort events. Contract order is
// segments → activity → efforts, so the bundle is self-contained (efforts
// resolve against segments emitted just before). Each unique segment's
// geometry is fetched once (cached) and ReserveAPI-throttled. A rate limit
// mid-loop must NOT fail the activity: stop fetching, return what we have,
// and let the caller NAK-with-delay so a retry after the window completes
// the remaining segments (cache hits make the retry cheap).
func collectSegments(ctx context.Context, w *workersdk.Worker, client *stravaClient, segCache *segmentCache, accessToken, bucket string, in fetchSourceJob, activity *stravaActivity) segmentBatch {
	var out segmentBatch
	emitted := map[string]bool{}
	for i := range activity.SegmentEfforts {
		eff := &activity.SegmentEfforts[i]
		segExtID := formatActivityID(eff.Segment.ID)

		seg, stop, retryAfter := resolveSegment(ctx, w, client, segCache, accessToken, bucket, segExtID)
		if stop {
			out.retryAfter = retryAfter
			break // budget exhausted; retry completes the rest
		}
		if seg == nil {
			continue
		}

		if !emitted[segExtID] {
			emitted[segExtID] = true
			out.segments = append(out.segments, &workerv1.WorkerEvent{
				Type: workerv1.WorkerEventType_WORKER_EVENT_TYPE_SEGMENT,
				Entity: &workerv1.WorkerEvent_Segment{Segment: &workerv1.ImportedSegment{
					Ref:     externalRef(in, segExtID),
					Payload: seg,
				}},
			})
		}
		out.efforts = append(out.efforts, &workerv1.WorkerEvent{
			Type: workerv1.WorkerEventType_WORKER_EVENT_TYPE_SEGMENT_EFFORT,
			Entity: &workerv1.WorkerEvent_SegmentEffort{SegmentEffort: &workerv1.ImportedSegmentEffort{
				Ref:                externalRef(in, formatActivityID(eff.ID)),
				ActivityExternalId: in.ExtID,
				SegmentExternalId:  segExtID,
				Payload:            mapSegmentEffortToProto(eff),
			}},
		})
	}
	return out
}

// resolveSegment returns a segment's geometry from the cache or the API. A nil
// segment without stop means skip this one; stop means the budget ran out.
func resolveSegment(ctx context.Context, w *workersdk.Worker, client *stravaClient, segCache *segmentCache, accessToken, bucket, segExtID string) (seg *workerv1.SegmentImport, stop bool, retryAfter time.Duration) {
	if seg, ok := segCache.get(segExtID); ok {
		return seg, false, 0
	}
	if err := reserveRead(ctx, w, bucket); err != nil {
		var nak *port.NakWithDelayError
		if errors.As(err, &nak) {
			retryAfter = nak.Delay
		}
		return nil, true, retryAfter
	}
	detail, err := client.GetSegment(ctx, accessToken, segExtID)
	if err != nil {
		var rl *StravaRateLimitError
		if errors.As(err, &rl) {
			// Sync the limiter and stop — hammering the remaining segments
			// would just 429 too.
			retryAfter = observe429(ctx, w, rl)
			w.Logger().Info("segment fetch rate-limited; deferring rest",
				"segment", segExtID, "retry_in", retryAfter)
			return nil, true, retryAfter
		}
		w.Logger().Warn("segment fetch failed", "segment", segExtID, "error", err)
		return nil, false, 0
	}
	if detail.Map.Polyline == "" {
		w.Logger().Warn("segment has no polyline; skipping", "segment", segExtID)
		return nil, false, 0
	}
	seg = mapSegmentToProto(detail)
	segCache.put(segExtID, seg)
	return seg, false, 0
}

// publishSegments sends segments + efforts as a separate result. The server
// resolves each effort's activity by external id, so this message is
// self-contained. Best-effort: the activity is already durable, and a
// failed/dropped segment batch is re-emitted on the next import.
func publishSegments(ctx context.Context, w *workersdk.Worker, job workersdk.Job, msgID string, segs segmentBatch) {
	if len(segs.segments) == 0 {
		return
	}
	events := append(segs.segments, segs.efforts...)
	if err := publishResultViaBlob(ctx, w, job, msgID, newJobResult(w, events...)); err != nil {
		w.Logger().Warn("segment result publish failed", "error", err)
		return
	}
	w.Logger().Info("published segments", "count", len(segs.segments), "efforts", len(segs.efforts))
}

// mapStravaErr translates a Strava-client error into the workersdk
// error flavor. Centralised here so every handler that hits the API
// shares the same routing semantics.
func mapStravaErr(ctx context.Context, w *workersdk.Worker, job workersdk.Job, in fetchSourceJob, err error, op string) error {
	switch {
	case errors.Is(err, ErrStravaUnauthorized):
		// Token is invalid. The next attempt with a fresh token will
		// succeed; the AuthHandler emits needs_reauth if refresh itself
		// fails.
		return &port.TerminalError{Reason: "needs_reauth", Cause: err}

	case errors.Is(err, ErrStravaNotFound):
		// The activity is gone upstream. Publish a domain event so the
		// server marks the source as orphaned, fail the queue item with the
		// true reason (an ACK alone would leave it to the stale reaper),
		// then ACK — nothing more to retry.
		evt, _ := json.Marshal(map[string]any{
			"user_id":     in.UserID,
			"account_id":  in.AccountID,
			"provider":    workerProvider,
			"external_id": in.ExtID,
			"reason":      op + ":404",
		})
		msgID := "deleted:" + workerProvider + ":" + in.ExtID
		_ = w.PublishEvent(ctx, "source.deleted_upstream", msgID, evt)
		publishFailureResult(ctx, w, job, in, "not_found", err)
		return nil

	default:
		// Other 4xx (403 app inactive / missing scope, 400) repeat on every
		// retry: fail the item instead of NAK-looping.
		var ue *stravaUnexpectedError
		if errors.As(err, &ue) && ue.Status >= 400 && ue.Status < 500 {
			reason := fmt.Sprintf("strava_%d", ue.Status)
			publishFailureResult(ctx, w, job, in, reason, err)
			return &port.TerminalError{Reason: reason, Cause: fmt.Errorf("strava %s: %w", op, err)}
		}
		var rl *StravaRateLimitError
		if errors.As(err, &rl) {
			return &port.NakWithDelayError{
				Reason: "rate_limited",
				Delay:  observe429(ctx, w, rl),
				Cause:  err,
			}
		}
		// 5xx, network errors, decode errors — all transient NAK.
		return fmt.Errorf("strava %s: %w", op, err)
	}
}

// observe429 anchors the limiter to the window a 429 actually tripped (daily
// → midnight UTC, otherwise → next quarter-hour; a provider Retry-After, when
// present, wins) and returns how long to wait before retrying.
func observe429(ctx context.Context, w *workersdk.Worker, rl *StravaRateLimitError) time.Duration {
	now := time.Now().UTC()
	windowReset, window := rateLimitReset(rl.Headers, now)
	bucket := rateBucket(rl.App, window)
	if rl.RetryAfter > 0 {
		windowReset = now.Add(rl.RetryAfter)
	}
	_ = w.ObserveRateLimit429(ctx, bucket, 0, windowReset)
	delay := time.Until(windowReset)
	if delay < time.Second {
		delay = time.Second
	}
	return delay
}

// ---------------------------------------------------------------------------
// parse_blob.strava: re-parse a stored blob with the current worker logic
// ---------------------------------------------------------------------------

type parseBlobJob = workersdk.ParseBlobJob

// makeParseBlobHandler re-parses an archived activity blob with the CURRENT
// worker mapping logic — no provider API call. It downloads the
// {activity,streams} JSON the worker stored at import time, re-maps it, and
// publishes a JobResult; the server's ingest pipeline finds the existing
// source (Stage 1) and handleReimport replaces the payload + re-merges.
//
// This is the quota-free reimport path: a worker logic/mapping change can be
// applied to historical activities without re-downloading from the provider.
func makeParseBlobHandler() func(context.Context, *workersdk.Worker, workersdk.Job) error {
	return func(ctx context.Context, w *workersdk.Worker, job workersdk.Job) error {
		var in parseBlobJob
		if err := json.Unmarshal(job.Body, &in); err != nil {
			return &port.TerminalError{Reason: "bad_payload", Cause: err}
		}
		// Rolling upgrade: leave it to a newer instance of the pool, backing
		// off; after ParserTooOldAttempts core is told and keeps the source
		// flagged for a later re-parse.
		if err := w.CheckParserVersion(job, in.MinParserVersion); err != nil {
			var term *port.TerminalError
			if errors.As(err, &term) {
				publishFailureResult(ctx, w, job, fetchSourceJob{UserID: in.UserID, AccountID: in.AccountID, ExtID: in.ExtID}, term.Reason, term.Cause)
			}
			return err
		}

		url, err := w.BlobDownloadURL(ctx, in.Blob)
		if err != nil {
			return err
		}

		arch, err := downloadArchivedActivity(ctx, url)
		if err != nil {
			return fmt.Errorf("download archive: %w", err) // transient → NAK
		}
		if arch.Version != datasetVersion {
			return &port.TerminalError{Reason: "dataset_mismatch", Cause: fmt.Errorf("archive dataset %d, parser reads %d", arch.Version, datasetVersion)}
		}

		ref := fetchSourceJob{UserID: in.UserID, AccountID: in.AccountID}
		events := parseArchive(ref, in.ExtID, in.Blob.FallbackHandle, arch)
		return publishResultViaBlob(ctx, w, job, "result:"+in.JobID, newJobResult(w, events...))
	}
}

// parseArchive is the parser proper: archived bytes in, events out. Its
// output is pinned per parser version by TestParserGolden.
func parseArchive(ref fetchSourceJob, extID, blobID string, arch archivedActivity) []*workerv1.WorkerEvent {
	if extID == "" {
		extID = formatActivityID(arch.Activity.ID)
	}
	// Archived JSON carries the gear summary, so a reparse backfills gear
	// on old imports without spending API budget.
	return activityEvents(ref, arch.Activity, &workerv1.ImportedActivity{
		Ref:       externalRef(ref, extID),
		Payload:   mapActivityToPayload(arch.Activity),
		Stream:    mapStreamsToProto(arch.Streams),
		RawBlobId: blobID, // keep the archive reference stable
	})
}

// parserTooOldAttempts bounds how long an old instance keeps handing a
// re-parse back (workersdk.CheckParserVersion).
const parserTooOldAttempts = workersdk.ParserTooOldAttempts

// ---------------------------------------------------------------------------
// backfill.strava: paginate /athlete/activities, enqueue per-activity jobs
// ---------------------------------------------------------------------------

type backfillJob struct {
	JobID         string     `json:"job_id"`
	AccountID     string     `json:"account_id"`
	UserID        string     `json:"user_id"`
	AthleteID     string     `json:"athlete_id"`
	Since         *time.Time `json:"since,omitempty"`
	PageSize      int        `json:"page_size"`
	MaxActivities int        `json:"max_activities,omitempty"`
}

func makeBackfillHandler(client *stravaClient) func(context.Context, *workersdk.Worker, workersdk.Job) error {
	return func(ctx context.Context, w *workersdk.Worker, job workersdk.Job) error {
		return runBackfill(ctx, w, client, job)
	}
}

func runBackfill(ctx context.Context, w *workersdk.Worker, client *stravaClient, job workersdk.Job) error {
	var in backfillJob
	if err := json.Unmarshal(job.Body, &in); err != nil {
		return &port.TerminalError{Reason: "bad_payload", Cause: err}
	}

	token, err := fetchToken(ctx, w, in.AccountID)
	if err != nil {
		return err
	}

	perPage := in.PageSize
	if perPage <= 0 || perPage > 200 {
		perPage = 100
	}
	var after int64
	if in.Since != nil {
		after = in.Since.Unix()
	}

	enqueued, err := backfillEnqueue(ctx, w, client, in, token.AccessToken, after, perPage)
	if err != nil {
		return listErr(ctx, w, job, in.AccountID, in.UserID, err, "list_activities")
	}

	// Backfill enqueues per-activity fetch_source sub-jobs; the result
	// itself carries no data events, just the worker stamp.
	w.Logger().Info("backfill enqueued fetch_source jobs", "account_id", in.AccountID, "enqueued", enqueued)
	body, err := protojson.Marshal(newJobResult(w))
	if err != nil {
		return &port.TerminalError{Reason: "marshal_result", Cause: err}
	}
	return w.PublishResult(ctx, job, "result:"+in.JobID, body)
}

// backfillEnqueue fans out a fetch_source job per listed activity, up to
// MaxActivities when set.
func backfillEnqueue(ctx context.Context, w *workersdk.Worker, client *stravaClient, in backfillJob, accessToken string, after int64, perPage int) (int, error) {
	var enqueued int
	err := listActivityPages(ctx, w, client, accessToken, after, perPage, func(summaries []stravaActivitySummary) (bool, error) {
		for _, s := range summaries {
			if err := enqueueFetchSource(ctx, w, in.JobID, in.AccountID, in.UserID, "backfill", formatActivityID(s.ID)); err != nil {
				return false, fmt.Errorf("enqueue fetch_source %d: %w", s.ID, err)
			}
			enqueued++
			if in.MaxActivities > 0 && enqueued >= in.MaxActivities {
				return false, nil
			}
		}
		return true, nil
	})
	return enqueued, err
}

// activityListError marks a failed listing call so callers can map it.
type activityListError struct{ err error }

func (e *activityListError) Error() string { return e.err.Error() }
func (e *activityListError) Unwrap() error { return e.err }

// listActivityPages walks the athlete's activities page by page until an
// empty or short page, or until visit reports no more. Listing failures come
// back as *activityListError; reserve and visit errors pass through.
func listActivityPages(ctx context.Context, w *workersdk.Worker, client *stravaClient, accessToken string, after int64, perPage int, visit func([]stravaActivitySummary) (more bool, err error)) error {
	for page := 1; ; page++ {
		if err := reserveRead(ctx, w, rateBucket(appFor(accessToken), "short")); err != nil {
			return err
		}
		summaries, err := client.ListAthleteActivities(ctx, accessToken, after, page, perPage)
		if err != nil {
			return &activityListError{err: err}
		}
		if len(summaries) == 0 {
			return nil
		}
		more, err := visit(summaries)
		if err != nil || !more || len(summaries) < perPage {
			return err
		}
	}
}

// listErr maps a listActivityPages error: listing failures go through
// mapStravaErr, everything else is returned as is.
func listErr(ctx context.Context, w *workersdk.Worker, job workersdk.Job, accountID, userID string, err error, op string) error {
	var le *activityListError
	if !errors.As(err, &le) {
		return err
	}
	return mapStravaErr(ctx, w, job, fetchSourceJob{AccountID: accountID, UserID: userID}, le.err, op)
}

// enqueueFetchSource fans out one fetch_source sub-job. The deterministic
// msg-id collapses duplicate enqueues within JetStream's dedup window.
func enqueueFetchSource(ctx context.Context, w *workersdk.Worker, jobID, accountID, userID, reason, extID string) error {
	return w.EnqueueFetchSource(ctx, jobID, accountID, userID, reason, extID)
}

// ---------------------------------------------------------------------------
// reconcile.strava: drift-safety + polling sync (see cairn-core docs/architecture.md §6.4)
// ---------------------------------------------------------------------------

type reconcileJob = workersdk.ReconcileJob

// reconcileLookbackFloor bounds how far back reconcile ever lists, even with
// no watermark (workersdk.ReconcileWindowStart).
const reconcileLookbackFloor = workersdk.ReconcileLookbackFloor

// defaultReconcileMaxEnqueue caps how many fetch_source sub-jobs one reconcile
// run enqueues, as a safety valve against a large recent burst.
const defaultReconcileMaxEnqueue = 500

func makeReconcileHandler(client *stravaClient) func(context.Context, *workersdk.Worker, workersdk.Job) error {
	return func(ctx context.Context, w *workersdk.Worker, job workersdk.Job) error {
		return runReconcile(ctx, w, client, job)
	}
}

func runReconcile(ctx context.Context, w *workersdk.Worker, client *stravaClient, job workersdk.Job) error {
	var in reconcileJob
	if err := json.Unmarshal(job.Body, &in); err != nil {
		return &port.TerminalError{Reason: "bad_payload", Cause: err}
	}

	token, err := fetchToken(ctx, w, in.AccountID)
	if err != nil {
		return err
	}

	newImports, highWaterMark, err := reconcileEnqueue(ctx, w, client, in, token.AccessToken)
	if err != nil {
		return listErr(ctx, w, job, in.AccountID, in.UserID, err, "reconcile_list")
	}

	// Reconcile enqueues fetch_source sub-jobs for missing activities;
	// the result carries the new high-water-mark watermark. Published on
	// an ACCOUNT-SUFFIXED subject (cairn.results.reconcile.strava.<acct>)
	// so the server can attribute the watermark + last_sync_at without the
	// JobResult wire format carrying an account field. The server MUST see
	// this even when zero activities were found — advancing last_sync_at
	// is what stops the scheduler from re-polling every tick.
	w.Logger().Info("reconcile enqueued fetch_source jobs",
		"account_id", in.AccountID, "new_imports", newImports, "watermark", highWaterMark)
	result := newJobResult(w)
	result.NewWatermark = timestamppb.New(highWaterMark)
	body, err := protojson.Marshal(result)
	if err != nil {
		return &port.TerminalError{Reason: "marshal_result", Cause: err}
	}
	subject := w.ResultSubject(jobReconcile) + "." + in.AccountID
	return w.Enqueue(ctx, subject, "result:"+in.JobID, body)
}

// reconcileEnqueue fans out a fetch_source job per recent activity not in
// KnownExtIDs and returns the count and the advanced high-water mark.
func reconcileEnqueue(ctx context.Context, w *workersdk.Worker, client *stravaClient, in reconcileJob, accessToken string) (int, time.Time, error) {
	maxEnqueue := in.MaxEnqueue
	if maxEnqueue <= 0 {
		maxEnqueue = defaultReconcileMaxEnqueue
	}

	known := make(map[string]struct{}, len(in.KnownExtIDs))
	for _, id := range in.KnownExtIDs {
		known[id] = struct{}{}
	}

	var newImports int
	var highWaterMark time.Time
	if in.Watermark != nil {
		highWaterMark = *in.Watermark
	}
	const perPage = 100
	after := workersdk.ReconcileWindowStart(in.Watermark, time.Now())
	err := listActivityPages(ctx, w, client, accessToken, after, perPage, func(summaries []stravaActivitySummary) (bool, error) {
		for _, s := range summaries {
			extID := formatActivityID(s.ID)
			if _, ok := known[extID]; ok {
				continue
			}
			if err := enqueueFetchSource(ctx, w, in.JobID, in.AccountID, in.UserID, "reconcile", extID); err != nil {
				return false, fmt.Errorf("enqueue fetch_source: %w", err)
			}
			newImports++
			if t := parseStravaTime(s.StartDate); !t.IsZero() && t.After(highWaterMark) {
				highWaterMark = t
			}
		}
		return newImports < maxEnqueue, nil
	})
	return newImports, highWaterMark, err
}
