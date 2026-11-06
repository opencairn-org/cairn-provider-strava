package mockstrava

import (
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"time"
)

// subscription is a Strava push subscription.
type subscription struct {
	ID            int64  `json:"id"`
	ApplicationID int64  `json:"application_id"`
	CallbackURL   string `json:"callback_url"`
	CreatedAt     string `json:"created_at"`
	UpdatedAt     string `json:"updated_at"`
}

// createSubscription runs Strava's handshake: GET callback_url with
// hub.mode/hub.verify_token/hub.challenge, expecting the challenge echoed
// as {"hub.challenge": …}. App credentials travel in the form, not a bearer.
func (s *Server) createSubscription(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil || !s.clientOK(r.Form.Get("client_id"), r.Form.Get("client_secret")) {
		writeJSON(w, http.StatusUnauthorized, errBody("Bad Request", "Application", "client_id", "invalid_client"))
		return
	}
	callback := r.Form.Get("callback_url")
	if callback == "" {
		writeJSON(w, http.StatusBadRequest, errBody("Bad Request", "PushSubscription", "callback url", "required"))
		return
	}
	if !loopback.MatchString(callback) {
		writeJSON(w, http.StatusBadRequest, errBody("Bad Request", "PushSubscription", "callback url", "invalid"))
		return
	}
	verify := r.Form.Get("verify_token")
	if verify == "" {
		verify = s.opts.VerifyToken
	}
	if !s.handshake(r, callback, verify) {
		writeJSON(w, http.StatusBadRequest, errBody("Bad Request", "PushSubscription", "callback url", "not verifiable"))
		return
	}
	s.mu.Lock()
	s.nextSubID++
	now := s.now().UTC().Format(time.RFC3339)
	sub := subscription{ID: s.nextSubID, ApplicationID: 1, CallbackURL: callback, CreatedAt: now, UpdatedAt: now}
	s.subs = append(s.subs, sub)
	s.mu.Unlock()
	writeJSON(w, http.StatusCreated, sub)
}

// handshake verifies the callback echoes a fresh challenge.
func (s *Server) handshake(r *http.Request, callback, verify string) bool {
	challenge := random()
	q := url.Values{"hub.mode": {"subscribe"}, "hub.verify_token": {verify}, "hub.challenge": {challenge}}
	sep := "?"
	if u, err := url.Parse(callback); err == nil && u.RawQuery != "" {
		sep = "&"
	}
	req, err := http.NewRequestWithContext(r.Context(), http.MethodGet, callback+sep+q.Encode(), nil) //nolint:gosec // G704: the subscriber's loopback callback, by design
	if err != nil {
		return false
	}
	resp, err := s.opts.Client.Do(req) //nolint:gosec // G704: see above
	if err != nil {
		return false
	}
	defer resp.Body.Close()
	var reply struct {
		Challenge string `json:"hub.challenge"`
	}
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 64<<10))
	return resp.StatusCode == http.StatusOK && json.Unmarshal(body, &reply) == nil && reply.Challenge == challenge
}

func (s *Server) listSubscriptions(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	if !s.clientOK(q.Get("client_id"), q.Get("client_secret")) {
		writeJSON(w, http.StatusUnauthorized, errBody("Bad Request", "Application", "client_id", "invalid_client"))
		return
	}
	s.mu.Lock()
	out := append([]subscription{}, s.subs...)
	s.mu.Unlock()
	writeJSON(w, http.StatusOK, out)
}

func (s *Server) deleteSubscription(w http.ResponseWriter, r *http.Request) {
	id, _ := strconv.ParseInt(r.PathValue("id"), 10, 64)
	s.mu.Lock()
	defer s.mu.Unlock()
	for i, sub := range s.subs {
		if sub.ID == id {
			s.subs = append(s.subs[:i], s.subs[i+1:]...)
			w.WriteHeader(http.StatusNoContent)
			return
		}
	}
	writeJSON(w, http.StatusNotFound, errBody("Record Not Found", "PushSubscription", "id", "invalid"))
}
