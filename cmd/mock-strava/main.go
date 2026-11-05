// Command mock-strava is a stand-in Strava for end-to-end runs of the
// worker against a real cairn-core (internal/mockstrava). Point the worker
// at it with
//
//	CAIRN_STRAVA_API_BASE_URL=http://127.0.0.1:PORT/api/v3
//	CAIRN_STRAVA_OAUTH_BASE_URL=http://127.0.0.1:PORT/oauth
//
// and drive it on /_mock/* (healthz, stats, activities, webhooks/emit,
// fail-next, reset). It prints "listening on http://…" once bound.
package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"github.com/opencairn-org/cairn-provider-strava/internal/mockstrava"
)

func main() {
	var (
		cfg  mockstrava.RunConfig
		opts mockstrava.Options
	)
	flag.StringVar(&cfg.Listen, "listen", "", "address to bind, e.g. 127.0.0.1:0 (required)")
	flag.StringVar(&cfg.AddrFile, "addr-file", "", "write the bound base URL to this file")
	flag.Int64Var(&opts.AthleteID, "athlete-id", mockstrava.DefaultAthleteID, "the athlete every token belongs to")
	flag.IntVar(&opts.Activities, "activities", mockstrava.DefaultActivities, "rides to seed")
	flag.DurationVar(&opts.SeedAge, "seed-age", mockstrava.DefaultSeedAge, "age of the newest seeded ride; older ones follow one day apart")
	flag.DurationVar(&opts.ExchangeTokenTTL, "exchange-token-ttl", mockstrava.DefaultExchangeTokenTTL, "expires_in of tokens from the code grant (refresh issues 6h)")
	flag.StringVar(&opts.VerifyToken, "verify-token", mockstrava.DefaultVerifyToken, "default verify_token of push subscriptions")
	flag.StringVar(&opts.ClientID, "client-id", "", "app client id the token endpoint requires (empty: any)")
	flag.StringVar(&opts.ClientSecret, "client-secret", "", "app client secret the token endpoint requires (empty: any)")
	flag.BoolVar(&opts.Segments, "segments", false, "one segment effort (segment 7) per ride")
	flag.Parse()
	if cfg.Listen == "" {
		fmt.Fprintln(os.Stderr, "mock-strava: --listen is required")
		flag.Usage()
		os.Exit(2)
	}

	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()
	if err := mockstrava.Run(ctx, cfg, opts); err != nil {
		fmt.Fprintln(os.Stderr, "mock-strava:", err)
		os.Exit(1)
	}
}
