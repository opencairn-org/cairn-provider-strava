package main

import (
	"context"

	workerv1 "github.com/opencairn-org/cairn-provider-sdk-go/proto/cairn/worker/v1"
	"github.com/opencairn-org/cairn-provider-sdk-go/workersdk"
)

// publishResultViaBlob claim-checks an event-carrying JobResult through the
// blob store (workersdk.Worker.PublishResultViaBlob): only the envelope with
// payload_ref travels over NATS, so result size is unbounded.
func publishResultViaBlob(ctx context.Context, w *workersdk.Worker, job workersdk.Job, msgID string, jr *workerv1.JobResult) error {
	return w.PublishResultViaBlob(ctx, job, msgID, jr)
}

// publishFailureResult tells the server a job failed terminally, so the
// import-queue item is failed NOW with the true reason
// (workersdk.Worker.PublishFailureResult). Best-effort.
func publishFailureResult(ctx context.Context, w *workersdk.Worker, job workersdk.Job, in fetchSourceJob, reason string, cause error) {
	w.PublishFailureResult(ctx, job, externalRef(in, in.ExtID), reason, cause)
}
