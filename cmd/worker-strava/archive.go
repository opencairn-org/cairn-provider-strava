package main

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/opencairn-org/cairn-provider-sdk-go/workersdk"
)

// archivedActivity is the self-describing blob format the worker stores in
// object storage on import and re-reads in parse_blob. Holding both the raw
// activity and its streams means a re-parse can reconstruct the full payload
// (summary + stream) without any provider API call.
//
// The Provenance block records exactly which worker/version/package/manifest
// wrote the blob, so a later re-parse can tell whether the current worker logic
// differs from the one that produced it (and is therefore worth re-running).
// This metadata is embedded IN the blob — independent of the activity_sources
// row — so the archive stays self-describing even if the DB row is lost or the
// blob is inspected out of band.
type archivedActivity struct {
	Version    int                   `json:"version"`
	Provenance archiveProvenance     `json:"provenance"`
	Activity   *stravaActivity       `json:"activity"`
	Streams    stravaStreamsResponse `json:"streams,omitempty"`
}

type archiveProvenance struct {
	Provider      string    `json:"provider"`
	WorkerName    string    `json:"worker_name"`
	WorkerVersion string    `json:"worker_version"`
	Package       string    `json:"package"`
	ArchivedAt    time.Time `json:"archived_at"`
}

// archiveActivityBlob marshals the raw fetched activity (+streams) plus worker
// provenance and uploads it via a server-minted presigned PUT, returning the
// blob id (full S3 key) to store as raw_blob_id and the archived byte size (the
// content type is always application/json). Best-effort: returns an error on
// any failure; the caller logs and continues, since the import is durable
// without the archive.
func archiveActivityBlob(ctx context.Context, w *workersdk.Worker, userID string, act *stravaActivity, streams stravaStreamsResponse) (blobID string, sizeBytes int64, err error) {
	body, err := json.Marshal(archivedActivity{
		Version: datasetVersion,
		Provenance: archiveProvenance{
			Provider:      workerProvider,
			WorkerName:    w.Name(),
			WorkerVersion: workerVersion,
			Package:       workerPackage,
			ArchivedAt:    time.Now().UTC(),
		},
		Activity: act,
		Streams:  streams,
	})
	if err != nil {
		return "", 0, fmt.Errorf("marshal archive: %w", err)
	}

	return w.ArchiveBlob(ctx, userID, body, "application/json")
}

// downloadArchivedActivity GETs and decodes an archived blob from a presigned
// URL (the parse_blob read path).
func downloadArchivedActivity(ctx context.Context, url string) (archivedActivity, error) {
	data, err := workersdk.DownloadBlob(ctx, url)
	if err != nil {
		return archivedActivity{}, err
	}
	var out archivedActivity
	if err := json.Unmarshal(data, &out); err != nil {
		return archivedActivity{}, fmt.Errorf("decode archive: %w", err)
	}
	if out.Activity == nil {
		return archivedActivity{}, fmt.Errorf("archive has no activity")
	}
	return out, nil
}
