package mockstrava

import (
	"net/http"
	"sort"
	"strconv"
	"strings"
)

// api wraps a v3 route: counts the call, serves a scripted failure, checks
// the bearer token and stamps the rate-limit headers.
func (s *Server) api(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		now := s.now()
		s.mu.Lock()
		s.stats.APICalls++
		short, daily := s.countCall(now)
		fail := s.takeFailNext()
		authed := s.authorized(r.Header.Get("Authorization"), now)
		switch {
		case fail.status == http.StatusTooManyRequests:
			s.stats.RateLimitedServed++
			short = shortLimit
		case fail.status == 0 && !authed:
			s.stats.Unauthorized++
		}
		s.mu.Unlock()

		setUsageHeaders(w.Header(), short, daily)
		switch {
		case fail.status != 0:
			writeFailure(w, fail)
		case !authed:
			writeJSON(w, http.StatusUnauthorized, errBody("Authorization Error", "AccessToken", "access_token", "invalid"))
		default:
			next(w, r)
		}
	}
}

// takeFailNext consumes one scripted failure; callers hold s.mu.
func (s *Server) takeFailNext() failNext {
	if s.fail.count <= 0 {
		return failNext{}
	}
	s.fail.count--
	f := s.fail
	if s.fail.count == 0 {
		s.fail = failNext{}
	}
	return f
}

func setUsageHeaders(h http.Header, short, daily int) {
	use := strconv.Itoa(short) + "," + strconv.Itoa(daily)
	h.Set("X-RateLimit-Limit", limitOverall)
	h.Set("X-RateLimit-Usage", use)
	h.Set("X-ReadRateLimit-Limit", limitRead)
	h.Set("X-ReadRateLimit-Usage", use)
}

func writeFailure(w http.ResponseWriter, f failNext) {
	if f.status == http.StatusTooManyRequests {
		if f.retryAfter > 0 {
			w.Header().Set("Retry-After", strconv.Itoa(f.retryAfter))
		}
		writeJSON(w, f.status, errBody("Rate Limit Exceeded", "Application", "rate limit", "exceeded"))
		return
	}
	writeJSON(w, f.status, stravaError{Message: "scripted failure " + strconv.Itoa(f.status), Errors: []stravaFault{}})
}

func (s *Server) athlete(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, s.athleteJSON())
}

// listActivities pages the athlete's rides newest first, after a Unix time.
func (s *Server) listActivities(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	page, _ := strconv.Atoi(q.Get("page"))
	perPage, _ := strconv.Atoi(q.Get("per_page"))
	after, _ := strconv.ParseInt(q.Get("after"), 10, 64)
	page, perPage = max(page, 1), max(perPage, 1)

	s.mu.Lock()
	var own []*activity
	for _, a := range s.activities {
		if after == 0 || a.start.Unix() > after {
			own = append(own, a)
		}
	}
	s.mu.Unlock()
	sort.SliceStable(own, func(i, j int) bool { return own[i].start.After(own[j].start) })

	from := min((page-1)*perPage, len(own))
	to := min(from+perPage, len(own))
	out := make([]summary, 0, to-from)
	for _, a := range own[from:to] {
		out = append(out, a.summary())
	}
	writeJSON(w, http.StatusOK, out)
}

// lookup resolves {id} to a stored activity, writing 404 when unknown.
func (s *Server) lookup(w http.ResponseWriter, r *http.Request) (*activity, bool) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	s.mu.Lock()
	defer s.mu.Unlock()
	a, ok := s.find(id)
	if err != nil || !ok {
		writeJSON(w, http.StatusNotFound, errBody("Record Not Found", "Activity", "id", "invalid"))
		return nil, false
	}
	return a, true
}

func (s *Server) activityDetail(w http.ResponseWriter, r *http.Request) {
	if a, ok := s.lookup(w, r); ok {
		writeJSON(w, http.StatusOK, a)
	}
}

// activityStreams answers the key_by_type layout for the requested keys;
// a ride without streams is a 404 like on Strava.
func (s *Server) activityStreams(w http.ResponseWriter, r *http.Request) {
	a, ok := s.lookup(w, r)
	if !ok {
		return
	}
	if a.streams == nil {
		writeJSON(w, http.StatusNotFound, errBody("Record Not Found", "Stream", "id", "invalid"))
		return
	}
	keys := strings.Split(r.URL.Query().Get("keys"), ",")
	out := map[string]stream{}
	for _, k := range keys {
		if st, ok := a.streams[strings.TrimSpace(k)]; ok {
			out[k] = st
		}
	}
	if r.URL.Query().Get("keys") == "" {
		out = a.streams
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *Server) activityPhotos(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.lookup(w, r); ok {
		writeJSON(w, http.StatusOK, []struct{}{})
	}
}

func (s *Server) gear(w http.ResponseWriter, r *http.Request) {
	g, ok := gearCatalog[r.PathValue("id")]
	if !ok {
		writeJSON(w, http.StatusNotFound, errBody("Record Not Found", "Gear", "id", "invalid"))
		return
	}
	writeJSON(w, http.StatusOK, g)
}

func (s *Server) segment(w http.ResponseWriter, r *http.Request) {
	if r.PathValue("id") != strconv.FormatInt(segmentID, 10) {
		writeJSON(w, http.StatusNotFound, errBody("Record Not Found", "Segment", "id", "invalid"))
		return
	}
	writeJSON(w, http.StatusOK, detailedSegment7())
}
