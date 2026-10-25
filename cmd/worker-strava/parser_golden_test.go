package main

import (
	"bytes"
	"encoding/json"
	"flag"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"google.golang.org/protobuf/encoding/protojson"

	workerv1 "github.com/opencairn-org/cairn-provider-strava/proto/cairn/worker/v1"
)

var updateGolden = flag.Bool("update", false, "write golden files for a new parser version")

// TestParserGolden pins the parsed output of every fixture archive to the
// parser version (cairn-core ADR 0005). Changing the output needs a new
// workerVersion: bump M, then `go test ./cmd/worker-strava -run
// TestParserGolden -update` writes the new version's goldens. -update never
// overwrites an existing version's golden.
func TestParserGolden(t *testing.T) {
	archives, err := filepath.Glob("testdata/archives/*.json")
	if err != nil || len(archives) == 0 {
		t.Fatalf("no fixture archives: %v", err)
	}
	for _, path := range archives {
		name := strings.TrimSuffix(filepath.Base(path), ".json")
		t.Run(name, func(t *testing.T) {
			raw, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			var arch archivedActivity
			if err := json.Unmarshal(raw, &arch); err != nil {
				t.Fatal(err)
			}
			if arch.Version != datasetVersion {
				t.Fatalf("fixture dataset %d, parser reads %d", arch.Version, datasetVersion)
			}
			got := canonicalEvents(t, parseArchive(fetchSourceJob{UserID: "u1", AccountID: "a1"}, "", "blob-1", arch))
			golden := filepath.Join("testdata", "golden", name+".parser-"+workerVersion+".json")
			if all, _ := filepath.Glob(filepath.Join("testdata", "golden", name+".parser-*.json")); len(all) > 1 || (len(all) == 1 && all[0] != golden) {
				t.Fatalf("stale goldens of an older parser: delete all but %s (%v)", golden, all)
			}
			want, err := os.ReadFile(golden)
			switch {
			case os.IsNotExist(err) && *updateGolden:
				if err := os.MkdirAll(filepath.Dir(golden), 0o755); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(golden, got, 0o644); err != nil {
					t.Fatal(err)
				}
				return
			case os.IsNotExist(err):
				t.Fatalf("no golden for parser %s: run with -update", workerVersion)
			case err != nil:
				t.Fatal(err)
			}
			if !bytes.Equal(got, want) {
				t.Fatalf("parsed output of %s changed under parser %s: bump workerVersion (M) and run with -update\n--- got\n%s", name, workerVersion, got)
			}
		})
	}
}

// canonicalEvents renders events as stable, indented JSON (protojson's own
// output is deliberately unstable).
func canonicalEvents(t *testing.T, events []*workerv1.WorkerEvent) []byte {
	t.Helper()
	out := make([]any, 0, len(events))
	for _, ev := range events {
		b, err := protojson.Marshal(ev)
		if err != nil {
			t.Fatal(err)
		}
		var v any
		if err := json.Unmarshal(b, &v); err != nil {
			t.Fatal(err)
		}
		out = append(out, v)
	}
	b, err := json.MarshalIndent(out, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	return append(b, '\n')
}
