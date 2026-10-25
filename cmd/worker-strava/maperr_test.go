package main

import (
	"context"
	"errors"
	"testing"

	"github.com/opencairn-org/cairn-provider-strava/internal/port"
	"github.com/opencairn-org/cairn-provider-strava/internal/workersdk"
)

func TestMapStravaErr_ClientErrorsAreTerminal(t *testing.T) {
	ctx := context.Background()
	for _, status := range []int{400, 403} {
		err := mapStravaErr(ctx, nil, workerConfig{}, workersdk.Job{}, fetchSourceJob{}, &stravaUnexpectedError{Status: status, Body: "x"}, "fetch_gear")
		var term *port.TerminalError
		if !errors.As(err, &term) {
			t.Fatalf("%d: want TerminalError, got %T %v", status, err, err)
		}
	}
	// 5xx stays transient.
	err := mapStravaErr(ctx, nil, workerConfig{}, workersdk.Job{}, fetchSourceJob{}, &StravaServerError{Status: 502}, "fetch_gear")
	var term *port.TerminalError
	if err == nil || errors.As(err, &term) {
		t.Fatalf("5xx: want plain error, got %v", err)
	}
}
