package mockstrava

import (
	"net/http"
	"testing"
	"time"
)

// seeded returns a server, its base URL and a valid token.
func seeded(t *testing.T, opts Options) (*Server, string, string) {
	t.Helper()
	s, srv := start(t, opts)
	access, _, _ := s.IssueToken()
	return s, srv.URL, access
}

func TestListActivities(t *testing.T) {
	s, base, tok := seeded(t, Options{Activities: 3})
	newest := s.AddActivity("Fresh", fixedNow.Add(-time.Hour), true)

	var page []summary
	decodeBody(t, get(t, base+"/api/v3/athlete/activities?page=1&per_page=2", tok), &page)
	if len(page) != 2 || page[0].ID != newest || page[1].Name != "Seeded ride 1" || page[0].Athlete.ID != DefaultAthleteID {
		t.Fatalf("page 1 = %+v", page)
	}
	if page[0].SportType != "Ride" || page[0].Distance <= 0 || page[0].GearID != gearBikeID || page[0].StartDate != fixedNow.Add(-time.Hour).Format(time.RFC3339) {
		t.Errorf("summary = %+v", page[0])
	}
	decodeBody(t, get(t, base+"/api/v3/athlete/activities?page=2&per_page=2", tok), &page)
	if len(page) != 2 || page[0].Name != "Seeded ride 2" {
		t.Errorf("page 2 = %+v", page)
	}
	decodeBody(t, get(t, base+"/api/v3/athlete/activities?page=3&per_page=2", tok), &page)
	if len(page) != 0 {
		t.Errorf("page 3 = %+v", page)
	}
	// after keeps rides newer than the Unix time: only the fresh one.
	after := fixedNow.Add(-30 * 24 * time.Hour).Unix()
	decodeBody(t, get(t, base+"/api/v3/athlete/activities?after="+itoa(int(after)), tok), &page)
	if len(page) != 1 || page[0].ID != newest {
		t.Errorf("after = %+v", page)
	}
	// Seeded rides start SeedAge ago, one day apart, older than the floor.
	var detail activity
	decodeBody(t, get(t, base+"/api/v3/activities/1000003", tok), &detail)
	if detail.StartDate != fixedNow.Add(-DefaultSeedAge-2*24*time.Hour).Format(time.RFC3339) {
		t.Errorf("seed 3 start = %s", detail.StartDate)
	}
}

func TestActivityDetailStreamsPhotos(t *testing.T) {
	s, base, tok := seeded(t, Options{Segments: true})
	var a activity
	decodeBody(t, get(t, base+"/api/v3/activities/1000001", tok), &a)
	if a.ID != 1000001 || len(a.Laps) != lapCount || a.Gear == nil || a.Gear.ID != gearBikeID || a.GearID != gearBikeID {
		t.Fatalf("detail = %+v", a)
	}
	if a.Athlete.ID != DefaultAthleteID || a.Photos.Primary != nil || a.ElapsedTime != sampleCount || !a.HasHeartrate || a.Calories <= 0 {
		t.Errorf("detail = %+v", a)
	}
	if len(a.SegmentEfforts) != 1 || a.SegmentEfforts[0].Segment.ID != segmentID || a.Laps[1].LapIndex != 2 || a.Laps[1].StartIndex != sampleCount/2 {
		t.Errorf("efforts/laps = %+v %+v", a.SegmentEfforts, a.Laps)
	}

	var streams map[string]stream
	decodeBody(t, get(t, base+"/api/v3/activities/1000001/streams?keys=time,latlng,heartrate,nope&key_by_type=true", tok), &streams)
	if len(streams) != 3 || streams["time"].Type != "time" || streams["latlng"].OriginalSize != sampleCount {
		t.Fatalf("streams = %v", streams)
	}
	if pts, ok := streams["latlng"].Data.([]any); !ok || len(pts) != sampleCount {
		t.Errorf("latlng = %v", streams["latlng"].Data)
	}
	decodeBody(t, get(t, base+"/api/v3/activities/1000001/streams", tok), &streams)
	if len(streams) != 10 {
		t.Errorf("all streams = %d", len(streams))
	}
	plain := s.AddActivity("No GPS", fixedNow, false)
	none := get(t, base+"/api/v3/activities/"+itoa(int(plain))+"/streams?keys=time", tok)
	none.Body.Close()
	if none.StatusCode != http.StatusNotFound {
		t.Errorf("no-stream ride: %d", none.StatusCode)
	}

	var photos []struct{}
	decodeBody(t, get(t, base+"/api/v3/activities/1000001/photos?size=2048", tok), &photos)
	if len(photos) != 0 {
		t.Errorf("photos = %v", photos)
	}
	for _, p := range []string{"/activities/99", "/activities/x/streams", "/activities/99/photos", "/gear/zz", "/segments/8"} {
		resp := get(t, base+"/api/v3"+p, tok)
		resp.Body.Close()
		if resp.StatusCode != http.StatusNotFound {
			t.Errorf("%s: %d", p, resp.StatusCode)
		}
	}
}

func TestAthleteGearSegment(t *testing.T) {
	_, base, tok := seeded(t, Options{})
	var ath athleteGear
	decodeBody(t, get(t, base+"/api/v3/athlete", tok), &ath)
	if ath.ID != DefaultAthleteID || len(ath.Bikes) != 1 || !ath.Bikes[0].Primary || len(ath.Shoes) != 1 || ath.Bikes[0].BrandName != "" {
		t.Errorf("athlete = %+v", ath)
	}
	var g gear
	decodeBody(t, get(t, base+"/api/v3/gear/b1", tok), &g)
	if g.BrandName != "Canyon" || g.ModelName != "Grail" || g.Description == "" || g.Distance != 1500.5 {
		t.Errorf("gear = %+v", g)
	}
	decodeBody(t, get(t, base+"/api/v3/gear/g1", tok), &g)
	if g.Name != "Pegasus" || g.BrandName == "" {
		t.Errorf("shoe = %+v", g)
	}
	var seg detailedSegment
	decodeBody(t, get(t, base+"/api/v3/segments/7", tok), &seg)
	if seg.ID != segmentID || seg.Map.Polyline == "" || seg.Name == "" || seg.TotalElevationGain <= 0 {
		t.Errorf("segment = %+v", seg)
	}
	var plain activity
	decodeBody(t, get(t, base+"/api/v3/activities/1000001", tok), &plain)
	if len(plain.SegmentEfforts) != 0 {
		t.Errorf("efforts without --segments = %+v", plain.SegmentEfforts)
	}
}

func TestFailNextAndUsage(t *testing.T) {
	s, base, tok := seeded(t, Options{})
	resp := postJSON(t, base+"/_mock/fail-next", map[string]any{"status": 429, "count": 2, "retry_after": 2})
	resp.Body.Close()
	for i := range 2 {
		r := get(t, base+"/api/v3/athlete", tok)
		r.Body.Close()
		if r.StatusCode != http.StatusTooManyRequests || r.Header.Get("Retry-After") != "2" || r.Header.Get("X-ReadRateLimit-Usage") != "100,"+itoa(i+1) {
			t.Errorf("call %d: %d %v", i, r.StatusCode, r.Header)
		}
	}
	r := get(t, base+"/api/v3/athlete", tok)
	r.Body.Close()
	if r.StatusCode != http.StatusOK || r.Header.Get("X-ReadRateLimit-Usage") != "3,3" || r.Header.Get("X-RateLimit-Usage") != "3,3" {
		t.Errorf("after fail-next: %d %v", r.StatusCode, r.Header)
	}
	if st := s.Stats(); st.RateLimitedServed != 2 || st.APICalls != 3 {
		t.Errorf("stats = %+v", st)
	}

	// Other statuses carry a generic body; a failure beats the auth check.
	resp = postJSON(t, base+"/_mock/fail-next", map[string]any{"status": 503})
	resp.Body.Close()
	r = get(t, base+"/api/v3/athlete", "bad-token")
	r.Body.Close()
	if r.StatusCode != http.StatusServiceUnavailable {
		t.Errorf("503 scripted: %d", r.StatusCode)
	}
	for _, body := range []map[string]any{{"status": 200}, {"status": "x"}} {
		resp = postJSON(t, base+"/_mock/fail-next", body)
		resp.Body.Close()
		if resp.StatusCode != http.StatusBadRequest {
			t.Errorf("fail-next %v: %d", body, resp.StatusCode)
		}
	}
}

func TestUsageWindowsRoll(t *testing.T) {
	now := fixedNow
	s := New(Options{Now: func() time.Time { return now }})
	s.countCall(now)
	s.countCall(now)
	now = now.Add(16 * time.Minute)
	short, daily := s.countCall(now)
	if short != 1 || daily != 3 {
		t.Errorf("after the quarter hour: %d,%d", short, daily)
	}
	now = now.Add(24 * time.Hour)
	if short, daily = s.countCall(now); short != 1 || daily != 1 {
		t.Errorf("after the day: %d,%d", short, daily)
	}
}

func TestReset(t *testing.T) {
	s, base, tok := seeded(t, Options{})
	resp := postJSON(t, base+"/_mock/fail-next", map[string]any{"status": 429, "count": 5})
	resp.Body.Close()
	s.mu.Lock()
	s.access[tok].revoked = true
	s.mu.Unlock()
	resp = postJSON(t, base+"/_mock/reset", nil)
	resp.Body.Close()
	r := get(t, base+"/api/v3/athlete", tok)
	r.Body.Close()
	if r.StatusCode != http.StatusOK {
		t.Errorf("after reset: %d", r.StatusCode)
	}
}
