package mockstrava

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"
)

var fixedNow = time.Date(2026, 11, 5, 12, 0, 0, 0, time.UTC)

// start mounts a Server on httptest with a fixed clock.
func start(t *testing.T, opts Options) (*Server, *httptest.Server) {
	t.Helper()
	if opts.Now == nil {
		opts.Now = func() time.Time { return fixedNow }
	}
	s := New(opts)
	srv := httptest.NewServer(s.Handler())
	t.Cleanup(srv.Close)
	return s, srv
}

// noRedirect returns a client that stops at the first redirect.
func noRedirect() *http.Client {
	return &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
}

func decodeBody(t *testing.T, resp *http.Response, v any) {
	t.Helper()
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	if err := json.Unmarshal(b, v); err != nil {
		t.Fatalf("decode %s: %v", b, err)
	}
}

func postForm(t *testing.T, u string, form url.Values) *http.Response {
	t.Helper()
	resp, err := http.PostForm(u, form)
	if err != nil {
		t.Fatal(err)
	}
	return resp
}

func get(t *testing.T, u, token string) *http.Response {
	t.Helper()
	req, _ := http.NewRequest(http.MethodGet, u, nil)
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	return resp
}

func postJSON(t *testing.T, u string, v any) *http.Response {
	t.Helper()
	b, _ := json.Marshal(v)
	resp, err := http.Post(u, "application/json", bytes.NewReader(b))
	if err != nil {
		t.Fatal(err)
	}
	return resp
}

// connect runs authorize → code → token and returns the token reply.
func connect(t *testing.T, base string) tokenReply {
	t.Helper()
	q := url.Values{"client_id": {"app"}, "redirect_uri": {"http://127.0.0.1:9/cb?x=1"}, "response_type": {"code"},
		"scope": {Scope}, "state": {"st"}, "approval_prompt": {"force"}}
	resp, err := noRedirect().Get(base + "/oauth/authorize?" + q.Encode())
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusFound {
		t.Fatalf("authorize status %d", resp.StatusCode)
	}
	loc, err := url.Parse(resp.Header.Get("Location"))
	if err != nil || loc.Host != "127.0.0.1:9" || loc.Query().Get("state") != "st" || loc.Query().Get("code") == "" || loc.Query().Get("x") != "1" {
		t.Fatalf("redirect = %s (%v)", resp.Header.Get("Location"), err)
	}
	form := url.Values{"client_id": {"app"}, "client_secret": {"s"}, "grant_type": {"authorization_code"},
		"code": {loc.Query().Get("code")}, "redirect_uri": {"http://127.0.0.1:9/cb"}}
	tr := postForm(t, base+"/oauth/token", form)
	if tr.StatusCode != http.StatusOK {
		t.Fatalf("token status %d", tr.StatusCode)
	}
	var reply tokenReply
	decodeBody(t, tr, &reply)
	return reply
}

func TestOAuthFlow(t *testing.T) {
	s, srv := start(t, Options{ExchangeTokenTTL: 30 * time.Second})
	reply := connect(t, srv.URL)
	if reply.Athlete == nil || reply.Athlete.ID != DefaultAthleteID || reply.TokenType != "Bearer" || reply.Scope != Scope {
		t.Fatalf("reply = %+v", reply)
	}
	if reply.ExpiresIn != 30 || reply.ExpiresAt != fixedNow.Add(30*time.Second).Unix() || reply.RefreshToken == "" {
		t.Errorf("expiry = %+v", reply)
	}
	if st := s.Stats(); st.Exchanges != 1 || st.Activities != DefaultActivities {
		t.Errorf("stats = %+v", st)
	}

	// The code is single-use.
	reused := postForm(t, srv.URL+"/oauth/token", url.Values{"client_id": {"a"}, "client_secret": {"b"}, "grant_type": {"authorization_code"}, "code": {"code-x"}})
	reused.Body.Close()
	if reused.StatusCode != http.StatusBadRequest {
		t.Errorf("reused code status %d", reused.StatusCode)
	}

	// The API accepts the token; a refresh rotates the access token and revokes it.
	ok := get(t, srv.URL+"/api/v3/athlete", reply.AccessToken)
	ok.Body.Close()
	if ok.StatusCode != http.StatusOK || ok.Header.Get("X-ReadRateLimit-Usage") != "1,1" || ok.Header.Get("X-RateLimit-Limit") != limitOverall {
		t.Fatalf("athlete status %d headers %v", ok.StatusCode, ok.Header)
	}
	rr := postForm(t, srv.URL+"/oauth/token", url.Values{"client_id": {"a"}, "client_secret": {"b"}, "grant_type": {"refresh_token"}, "refresh_token": {reply.RefreshToken}})
	var refreshed tokenReply
	decodeBody(t, rr, &refreshed)
	if refreshed.Athlete != nil || refreshed.RefreshToken != reply.RefreshToken || refreshed.AccessToken == reply.AccessToken || refreshed.ExpiresIn != 6*3600 {
		t.Fatalf("refresh = %+v", refreshed)
	}
	old := get(t, srv.URL+"/api/v3/athlete", reply.AccessToken)
	old.Body.Close()
	fresh := get(t, srv.URL+"/api/v3/athlete", refreshed.AccessToken)
	fresh.Body.Close()
	if old.StatusCode != http.StatusUnauthorized || fresh.StatusCode != http.StatusOK {
		t.Errorf("old %d fresh %d", old.StatusCode, fresh.StatusCode)
	}
	if st := s.Stats(); st.Refreshes != 1 || st.Unauthorized != 1 || st.APICalls != 3 {
		t.Errorf("stats = %+v", st)
	}

	// Bad refresh token, bad grant, missing credentials.
	for name, form := range map[string]url.Values{
		"bad refresh": {"client_id": {"a"}, "client_secret": {"b"}, "grant_type": {"refresh_token"}, "refresh_token": {"nope"}},
		"bad grant":   {"client_id": {"a"}, "client_secret": {"b"}, "grant_type": {"password"}},
	} {
		resp := postForm(t, srv.URL+"/oauth/token", form)
		resp.Body.Close()
		if resp.StatusCode != http.StatusBadRequest {
			t.Errorf("%s: status %d", name, resp.StatusCode)
		}
	}
	noCreds := postForm(t, srv.URL+"/oauth/token", url.Values{"grant_type": {"refresh_token"}, "refresh_token": {reply.RefreshToken}})
	noCreds.Body.Close()
	if noCreds.StatusCode != http.StatusUnauthorized {
		t.Errorf("no creds: status %d", noCreds.StatusCode)
	}
}

func TestOAuthClientCredentials(t *testing.T) {
	_, srv := start(t, Options{ClientID: "id1", ClientSecret: "sec"})
	q := url.Values{"client_id": {"other"}, "redirect_uri": {"http://localhost/cb"}, "response_type": {"code"}}
	resp, err := noRedirect().Get(srv.URL + "/oauth/authorize?" + q.Encode())
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("authorize with wrong client: %d", resp.StatusCode)
	}
	bad := postForm(t, srv.URL+"/oauth/token", url.Values{"client_id": {"id1"}, "client_secret": {"wrong"}, "grant_type": {"refresh_token"}})
	bad.Body.Close()
	if bad.StatusCode != http.StatusUnauthorized {
		t.Errorf("wrong secret: %d", bad.StatusCode)
	}
	q.Set("client_id", "id1")
	resp, err = noRedirect().Get(srv.URL + "/oauth/authorize?" + q.Encode())
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusFound || !strings.Contains(resp.Header.Get("Location"), "scope="+url.QueryEscape(Scope)) {
		t.Errorf("authorize = %d %s", resp.StatusCode, resp.Header.Get("Location"))
	}
	missing, err := noRedirect().Get(srv.URL + "/oauth/authorize?response_type=token")
	if err != nil {
		t.Fatal(err)
	}
	missing.Body.Close()
	if missing.StatusCode != http.StatusBadRequest {
		t.Errorf("bad authorize params: %d", missing.StatusCode)
	}
}

func TestIssueTokenAndExpiry(t *testing.T) {
	now := fixedNow
	s, srv := start(t, Options{Now: func() time.Time { return now }})
	access, refresh, exp := s.IssueToken()
	if refresh == "" || !exp.Equal(fixedNow.Add(refreshTokenTTL)) {
		t.Fatalf("issue = %s %v", refresh, exp)
	}
	ok := get(t, srv.URL+"/api/v3/athlete", access)
	ok.Body.Close()
	now = fixedNow.Add(7 * time.Hour)
	expired := get(t, srv.URL+"/api/v3/athlete", access)
	expired.Body.Close()
	if ok.StatusCode != http.StatusOK || expired.StatusCode != http.StatusUnauthorized {
		t.Errorf("ok %d expired %d", ok.StatusCode, expired.StatusCode)
	}
	if s.AthleteID() != DefaultAthleteID {
		t.Error("athlete id")
	}
}
