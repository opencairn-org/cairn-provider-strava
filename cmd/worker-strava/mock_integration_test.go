package main

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/opencairn-org/cairn-provider-sdk-go/port"
	cairnv1 "github.com/opencairn-org/cairn-provider-sdk-go/proto/cairn/v1"
	"github.com/opencairn-org/cairn-provider-sdk-go/workersdk"

	"github.com/opencairn-org/cairn-provider-strava/internal/mockstrava"
)

// mockRun points the worker's client and auth handler at internal/mockstrava
// through the env overrides, the way the e2e harness does.
func mockRun(t *testing.T, opts mockstrava.Options) (*mockstrava.Server, string, *stravaClient, *StravaAuthHandler) {
	t.Helper()
	mock := mockstrava.New(opts)
	srv := httptest.NewServer(mock.Handler())
	t.Cleanup(srv.Close)
	t.Setenv("CAIRN_STRAVA_API_BASE_URL", srv.URL+"/api/v3")
	t.Setenv("CAIRN_STRAVA_OAUTH_BASE_URL", srv.URL+"/oauth")
	t.Setenv("CAIRN_STRAVA_TOKEN_URL", "")
	return mock, srv.URL, newStravaClient(), NewStravaAuthHandler()
}

// authorizeCode runs the mock's auto-approving authorize and returns the code.
func authorizeCode(t *testing.T, authorizeURL string) string {
	t.Helper()
	q := url.Values{"client_id": {"app"}, "redirect_uri": {"http://core/cb"}, "response_type": {"code"}, "state": {"s"}}
	c := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	resp, err := c.Get(authorizeURL + "?" + q.Encode())
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	loc, _ := url.Parse(resp.Header.Get("Location"))
	return loc.Query().Get("code")
}

func TestMockStrava_WorkerRoundTrip(t *testing.T) {
	mock, _, client, auth := mockRun(t, mockstrava.Options{Activities: 2, Segments: true})
	ctx := context.Background()

	code := authorizeCode(t, stravaProviderMeta().OAuth.AuthorizeURL)
	reply := auth.Exchange(ctx, workersdk.OAuthExchangeRequest{ClientID: "app", ClientSecret: "s", Code: code, RedirectURI: "http://core/cb"})
	if reply.Error != "" || reply.ProviderAccountID != "4242" || reply.AccessToken == "" || reply.RefreshToken == "" {
		t.Fatalf("exchange = %+v", reply)
	}

	sums, err := client.ListAthleteActivities(ctx, reply.AccessToken, 0, 1, 10)
	if err != nil || len(sums) != 2 {
		t.Fatalf("list = %v, %v", sums, err)
	}
	extID := formatActivityID(sums[0].ID)
	act, err := client.GetActivity(ctx, reply.AccessToken, extID)
	if err != nil {
		t.Fatal(err)
	}
	payload := mapActivityToPayload(act)
	if payload.GetType() != cairnv1.ActivityType_ACTIVITY_TYPE_RIDE || len(payload.GetLaps()) != 2 || payload.GetSummary().GetDistanceM() <= 0 ||
		payload.GetSummary().GetAvgHeartRateBpm() == 0 || payload.GetGearExternalId() != "b1" {
		t.Errorf("payload = %+v", payload)
	}
	// Efforts become SEGMENT_EFFORT events (collectSegments), not payload fields.
	if act.Gear == nil || act.Gear.ID != "b1" || act.Athlete.ID != 4242 || len(act.SegmentEfforts) != 1 || act.SegmentEfforts[0].Segment.ID != 7 {
		t.Errorf("gear/athlete/efforts = %+v %+v %+v", act.Gear, act.Athlete, act.SegmentEfforts)
	}
	streams, err := client.GetActivityStreams(ctx, reply.AccessToken, extID)
	if err != nil {
		t.Fatal(err)
	}
	st := mapStreamsToProto(streams)
	if st.GetSampleCount() != 60 || len(st.GetHeartRateBpm()) != 60 || len(st.GetLatitude()) != 60 || len(st.GetPowerW()) != 60 || len(st.GetChannels()) < 8 {
		t.Errorf("stream = %d samples, %d channels", st.GetSampleCount(), len(st.GetChannels()))
	}
	gearList, err := client.GetAthleteGear(ctx, reply.AccessToken)
	if err != nil || len(gearList.Bikes) != 1 || len(gearList.Shoes) != 1 {
		t.Errorf("gear = %+v, %v", gearList, err)
	}
	if seg, err := client.GetSegment(ctx, reply.AccessToken, "7"); err != nil || seg.Map.Polyline == "" {
		t.Errorf("segment = %+v, %v", seg, err)
	}

	// Refresh issues a new token and revokes the old one.
	current := port.TokenState{AccessToken: reply.AccessToken, RefreshToken: reply.RefreshToken, ExpiresAt: reply.ExpiresAt}
	fresh, err := auth.Refresh(ctx, current, workersdk.OAuthClientCreds{ClientID: "app", ClientSecret: "s"})
	if err != nil || fresh.AccessToken == reply.AccessToken || time.Until(fresh.ExpiresAt) < 5*time.Hour {
		t.Fatalf("refresh = %+v, %v", fresh, err)
	}
	if _, err := client.GetActivity(ctx, reply.AccessToken, extID); !errors.Is(err, ErrStravaUnauthorized) {
		t.Errorf("old token: %v", err)
	}
	if _, err := client.GetActivity(ctx, fresh.AccessToken, extID); err != nil {
		t.Errorf("fresh token: %v", err)
	}
	if st := mock.Stats(); st.Refreshes != 1 || st.Exchanges != 1 || st.Unauthorized != 1 {
		t.Errorf("stats = %+v", st)
	}
}

func TestMockStrava_RateLimitInjection(t *testing.T) {
	mock, base, client, _ := mockRun(t, mockstrava.Options{})
	access, _, _ := mock.IssueToken()
	ctx := context.Background()
	req, _ := http.NewRequestWithContext(ctx, http.MethodPost, base+"/_mock/fail-next", strings.NewReader(`{"status":429,"count":1,"retry_after":3}`))
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()

	_, err = client.GetActivity(ctx, access, "1000001")
	var rl *StravaRateLimitError
	if !errors.As(err, &rl) || rl.RetryAfter != 3*time.Second || rl.Headers["X-ReadRateLimit-Usage"] != "100,1" {
		t.Fatalf("429 = %v", err)
	}
	if _, err := client.GetActivity(ctx, access, "1000001"); err != nil {
		t.Errorf("after the injected 429: %v", err)
	}
}
