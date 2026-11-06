package mockstrava

import (
	"net/http"
	"net/url"
	"strings"
	"time"
)

// authorize auto-approves: it hands a single-use code back to redirect_uri.
func (s *Server) authorize(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	redirect := q.Get("redirect_uri")
	if q.Get("response_type") != "code" || redirect == "" || q.Get("client_id") == "" {
		writeJSON(w, http.StatusBadRequest, errBody("Bad Request", "Application", "redirect_uri", "invalid"))
		return
	}
	if !loopback.MatchString(redirect) {
		writeJSON(w, http.StatusBadRequest, errBody("Bad Request", "Application", "redirect_uri", "invalid"))
		return
	}
	if s.opts.ClientID != "" && q.Get("client_id") != s.opts.ClientID {
		writeJSON(w, http.StatusBadRequest, errBody("Bad Request", "Application", "client_id", "invalid"))
		return
	}
	code := "code-" + random()
	s.mu.Lock()
	s.codes[code] = struct{}{}
	s.mu.Unlock()

	params := url.Values{}
	params.Set("state", q.Get("state"))
	params.Set("code", code)
	scope := q.Get("scope")
	if scope == "" {
		scope = Scope
	}
	params.Set("scope", scope)
	sep := "?"
	if strings.Contains(redirect, "?") {
		sep = "&"
	}
	http.Redirect(w, r, redirect+sep+params.Encode(), http.StatusFound) //nolint:gosec // G710: redirect_uri is a loopback URL (checked above)
}

// tokenReply is the token endpoint's success body.
type tokenReply struct {
	TokenType    string   `json:"token_type"`
	AccessToken  string   `json:"access_token"`
	RefreshToken string   `json:"refresh_token"`
	ExpiresAt    int64    `json:"expires_at"`
	ExpiresIn    int64    `json:"expires_in"`
	Scope        string   `json:"scope"`
	Athlete      *athlete `json:"athlete,omitempty"`
}

type athlete struct {
	ID        int64  `json:"id"`
	Firstname string `json:"firstname"`
	Lastname  string `json:"lastname"`
}

// token serves the authorization_code and refresh_token grants.
func (s *Server) token(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		writeJSON(w, http.StatusBadRequest, errBody("Bad Request", "Application", "body", "invalid"))
		return
	}
	if !s.clientOK(r.Form.Get("client_id"), r.Form.Get("client_secret")) {
		writeJSON(w, http.StatusUnauthorized, errBody("Bad Request", "Application", "client_id", "invalid_client"))
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	switch r.Form.Get("grant_type") {
	case "authorization_code":
		s.exchange(w, r.Form.Get("code"))
	case "refresh_token":
		s.refreshGrant(w, r.Form.Get("refresh_token"))
	default:
		writeJSON(w, http.StatusBadRequest, errBody("Bad Request", "Application", "grant_type", "invalid"))
	}
}

// clientOK checks the app credentials when the mock was given any.
func (s *Server) clientOK(id, secret string) bool {
	if id == "" || secret == "" {
		return false
	}
	if s.opts.ClientID == "" && s.opts.ClientSecret == "" {
		return true
	}
	return id == s.opts.ClientID && secret == s.opts.ClientSecret
}

// exchange trades a single-use code for tokens; callers hold s.mu.
func (s *Server) exchange(w http.ResponseWriter, code string) {
	if _, ok := s.codes[code]; !ok {
		writeJSON(w, http.StatusBadRequest, errBody("Bad Request", "AuthorizationCode", "code", "invalid"))
		return
	}
	delete(s.codes, code)
	s.stats.Exchanges++
	reply := s.tokenReply(s.opts.ExchangeTokenTTL)
	reply.Athlete = &athlete{ID: s.opts.AthleteID, Firstname: "E2E", Lastname: "Athlete"}
	writeJSON(w, http.StatusOK, reply)
}

// refreshGrant issues a fresh access token for the (non-rotating) refresh
// token and revokes the earlier ones; callers hold s.mu.
func (s *Server) refreshGrant(w http.ResponseWriter, refresh string) {
	if refresh != s.refresh {
		writeJSON(w, http.StatusBadRequest, errBody("Bad Request", "RefreshToken", "refresh_token", "invalid"))
		return
	}
	for _, t := range s.access {
		t.revoked = true
	}
	s.stats.Refreshes++
	writeJSON(w, http.StatusOK, s.tokenReply(refreshTokenTTL))
}

func (s *Server) tokenReply(ttl time.Duration) tokenReply {
	access := s.issueAccess(ttl)
	return tokenReply{
		TokenType: "Bearer", AccessToken: access, RefreshToken: s.refresh,
		ExpiresAt: s.access[access].expiresAt.Unix(), ExpiresIn: int64(ttl / time.Second), Scope: Scope,
	}
}

// issueAccess mints an access token valid for ttl; callers hold s.mu.
func (s *Server) issueAccess(ttl time.Duration) string {
	access := "at-" + random()
	s.access[access] = &accessToken{expiresAt: s.now().Add(ttl)}
	return access
}

// authorized reports whether the bearer header names a live token; callers hold s.mu.
func (s *Server) authorized(header string, now time.Time) bool {
	tok, ok := s.access[strings.TrimPrefix(header, "Bearer ")]
	return ok && !tok.revoked && now.Before(tok.expiresAt)
}
