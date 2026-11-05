// Package mockstrava is a stand-in Strava for end-to-end runs: the OAuth
// round-trip (auto-approving authorize, code and refresh grants), the subset
// of the v3 API the worker reads (athlete, activity list/detail/streams/
// photos, gear, segments), Strava-shaped rate-limit headers and error bodies,
// webhook push subscriptions, and a /_mock/* control API the test harness
// drives (seed activities, emit webhook events, inject failures, read
// counters). cmd/mock-strava serves it as a binary; tests mount Handler()
// on httptest.
package mockstrava

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"sync"
	"time"
)

// Defaults for the zero Options.
const (
	DefaultAthleteID        int64 = 4242
	DefaultActivities             = 3
	DefaultSeedAge                = 60 * 24 * time.Hour
	DefaultExchangeTokenTTL       = 6 * time.Hour
	DefaultVerifyToken            = "e2e-verify"
	// refreshTokenTTL is the lifetime of tokens the refresh grant issues.
	refreshTokenTTL = 6 * time.Hour
	// Scope every grant carries.
	Scope = "read,activity:read_all"
	// Rate-limit headers: overall and read budgets (short window, daily).
	limitOverall = "200,2000"
	limitRead    = "100,1000"
	shortLimit   = 100
)

// Options configures a Server; zero values take the defaults.
type Options struct {
	// AthleteID is the single athlete every token belongs to.
	AthleteID int64
	// Activities is how many rides to seed.
	Activities int
	// SeedAge is how long ago the newest seeded ride started; older ones
	// follow one day apart. Keep it beyond the SDK's 30-day reconcile floor.
	SeedAge time.Duration
	// ExchangeTokenTTL is the expires_in of tokens the code grant issues; the
	// refresh grant always issues 6h.
	ExchangeTokenTTL time.Duration
	// VerifyToken is the default verify_token of push subscriptions.
	VerifyToken string
	// ClientID/ClientSecret, when set, are checked by the token endpoint.
	ClientID, ClientSecret string
	// Segments adds one segment effort (segment 7) to every activity.
	Segments bool
	// Now is the clock; nil means time.Now.
	Now func() time.Time
	// Client sends webhook events and subscription handshakes; nil means a
	// 10-second-timeout client.
	Client *http.Client
}

func (o Options) withDefaults() Options {
	if o.AthleteID == 0 {
		o.AthleteID = DefaultAthleteID
	}
	if o.Activities == 0 {
		o.Activities = DefaultActivities
	}
	if o.SeedAge == 0 {
		o.SeedAge = DefaultSeedAge
	}
	if o.ExchangeTokenTTL == 0 {
		o.ExchangeTokenTTL = DefaultExchangeTokenTTL
	}
	if o.VerifyToken == "" {
		o.VerifyToken = DefaultVerifyToken
	}
	if o.Now == nil {
		o.Now = time.Now
	}
	if o.Client == nil {
		o.Client = &http.Client{Timeout: 10 * time.Second}
	}
	return o
}

// Stats are the counters /_mock/stats reports.
type Stats struct {
	Refreshes         int `json:"refreshes"`
	Exchanges         int `json:"exchanges"`
	APICalls          int `json:"api_calls"`
	Unauthorized      int `json:"unauthorized"`
	RateLimitedServed int `json:"rate_limited_served"`
	WebhooksSent      int `json:"webhooks_sent"`
	Activities        int `json:"activities"`
}

// accessToken is one issued bearer token.
type accessToken struct {
	expiresAt time.Time
	revoked   bool
}

// failNext is a scripted failure for the next count API calls.
type failNext struct {
	status, count, retryAfter int
}

// usage counts calls per Strava window (15 min, UTC day).
type usage struct {
	short, daily           int
	shortReset, dailyReset time.Time
}

// Server is the in-memory Strava.
type Server struct {
	opts Options

	mu         sync.Mutex
	activities []*activity
	nextID     int64
	codes      map[string]struct{}
	access     map[string]*accessToken
	refresh    string
	subs       []subscription
	nextSubID  int64
	fail       failNext
	stats      Stats
	usage      usage
}

// New builds a Server with the seeded rides.
func New(opts Options) *Server {
	s := &Server{
		opts:    opts.withDefaults(),
		nextID:  1000001,
		codes:   map[string]struct{}{},
		access:  map[string]*accessToken{},
		refresh: "rt-" + random(),
	}
	now := s.opts.Now()
	for i := range s.opts.Activities {
		start := now.Add(-s.opts.SeedAge - time.Duration(i)*24*time.Hour)
		s.addActivity("Seeded ride "+itoa(i+1), start, true)
	}
	return s
}

// Handler serves the OAuth, API and control routes.
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /oauth/authorize", s.authorize)
	mux.HandleFunc("POST /oauth/token", s.token)
	mux.HandleFunc("POST /api/v3/push_subscriptions", s.createSubscription)
	mux.HandleFunc("GET /api/v3/push_subscriptions", s.listSubscriptions)
	mux.HandleFunc("DELETE /api/v3/push_subscriptions/{id}", s.deleteSubscription)
	mux.HandleFunc("GET /api/v3/athlete", s.api(s.athlete))
	mux.HandleFunc("GET /api/v3/athlete/activities", s.api(s.listActivities))
	mux.HandleFunc("GET /api/v3/activities/{id}", s.api(s.activityDetail))
	mux.HandleFunc("GET /api/v3/activities/{id}/streams", s.api(s.activityStreams))
	mux.HandleFunc("GET /api/v3/activities/{id}/photos", s.api(s.activityPhotos))
	mux.HandleFunc("GET /api/v3/gear/{id}", s.api(s.gear))
	mux.HandleFunc("GET /api/v3/segments/{id}", s.api(s.segment))
	mux.HandleFunc("GET /_mock/healthz", s.healthz)
	mux.HandleFunc("GET /_mock/stats", s.getStats)
	mux.HandleFunc("POST /_mock/activities", s.controlAddActivity)
	mux.HandleFunc("POST /_mock/webhooks/emit", s.controlEmitWebhook)
	mux.HandleFunc("POST /_mock/fail-next", s.controlFailNext)
	mux.HandleFunc("POST /_mock/reset", s.controlReset)
	return mux
}

// Stats returns a snapshot of the counters.
func (s *Server) Stats() Stats {
	s.mu.Lock()
	defer s.mu.Unlock()
	st := s.stats
	st.Activities = len(s.activities)
	return st
}

// AddActivity seeds one more ride starting at start and returns its id.
func (s *Server) AddActivity(name string, start time.Time, streams bool) int64 {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.addActivity(name, start, streams)
}

// IssueToken mints an access token directly (seeding a token store).
func (s *Server) IssueToken() (access, refresh string, expiresAt time.Time) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.issueAccess(refreshTokenTTL), s.refresh, s.opts.Now().Add(refreshTokenTTL)
}

// AthleteID is the athlete every token belongs to.
func (s *Server) AthleteID() int64 { return s.opts.AthleteID }

func (s *Server) now() time.Time { return s.opts.Now() }

// countCall records one API call and returns the window usage.
func (s *Server) countCall(now time.Time) (short, daily int) {
	if !now.Before(s.usage.shortReset) {
		s.usage.short = 0
		s.usage.shortReset = now.Truncate(15 * time.Minute).Add(15 * time.Minute)
	}
	if !now.Before(s.usage.dailyReset) {
		s.usage.daily = 0
		s.usage.dailyReset = now.UTC().Truncate(24 * time.Hour).Add(24 * time.Hour)
	}
	s.usage.short++
	s.usage.daily++
	return s.usage.short, s.usage.daily
}

func random() string {
	var b [12]byte
	_, _ = rand.Read(b[:])
	return hex.EncodeToString(b[:])
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

// stravaError is Strava's error envelope.
type stravaError struct {
	Message string        `json:"message"`
	Errors  []stravaFault `json:"errors"`
}

type stravaFault struct {
	Resource string `json:"resource"`
	Field    string `json:"field"`
	Code     string `json:"code"`
}

func errBody(message, resource, field, code string) stravaError {
	return stravaError{Message: message, Errors: []stravaFault{{Resource: resource, Field: field, Code: code}}}
}
