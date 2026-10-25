package workersdk

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"golang.org/x/mod/semver"

	"github.com/opencairn-org/cairn-provider-strava/internal/port"
)

// kvCoreInfo holds core's release under key "info" (cairn-core ADR 0005).
const kvCoreInfo = "cairn_core"

// JobSubject is this worker pool's subject for a job kind:
// cairn.jobs.<job>.<provider>.<name> (cairn-core ADR 0006).
func (w *Worker) JobSubject(job string) string {
	return "cairn.jobs." + job + "." + w.cfg.Provider + "." + w.cfg.Name
}

// Name is the pool name (Config.Name).
func (w *Worker) Name() string { return w.cfg.Name }

// DiscoverSubject is the request/reply subject this pool answers discovery on.
func (w *Worker) DiscoverSubject() string {
	return "cairn.discover." + w.cfg.Provider + "." + w.cfg.Name
}

// HandleLegacy drains a pre-0.5 provider-only subject with the old durable,
// so jobs queued before the upgrade are not stranded. Failure to subscribe
// is logged, not fatal. Remove in 0.6.
func (w *Worker) HandleLegacy(subject string, fn HandlerFunc) {
	w.legacy[subject] = fn
}

// ErrIncompatibleCore is returned when core's release has another
// major.minor than this worker's.
var ErrIncompatibleCore = errors.New("workersdk: core release incompatible")

// checkCoreRelease compares core's published release with ours. Missing
// info or an untagged build on either side is not an error.
func (w *Worker) checkCoreRelease(ctx context.Context) error {
	ours := semver.MajorMinor(w.cfg.Release)
	if ours == "" {
		return nil
	}
	kv, err := w.cfg.Bus.KV(kvCoreInfo)
	if err != nil {
		w.logger.Debug("core info unavailable", "error", err)
		return nil
	}
	e, err := kv.Get(ctx, "info")
	if err != nil {
		if !errors.Is(err, port.ErrKVKeyNotFound) {
			w.logger.Debug("core info read failed", "error", err)
		}
		return nil
	}
	var info struct {
		Release string `json:"release"`
	}
	if json.Unmarshal(e.Value, &info) != nil {
		return nil
	}
	core := semver.MajorMinor(info.Release)
	if core == "" || core == ours {
		return nil
	}
	return fmt.Errorf("%w: core %s, worker %s (major.minor must match)", ErrIncompatibleCore, info.Release, w.cfg.Release)
}
