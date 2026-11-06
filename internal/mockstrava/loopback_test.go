package mockstrava

import (
	"context"
	"net/http"
	"net/url"
	"testing"
)

func TestLoopbackPattern(t *testing.T) {
	for raw, want := range map[string]bool{
		"http://127.0.0.1:8080/cb":         true,
		"https://localhost/cb?x=1":         true,
		"http://[::1]:9":                   true,
		"http://localhost":                 true,
		"http://localhost#f":               true,
		"":                                 false,
		"http://core.test/cb":              false,
		"http://127.0.0.1@evil.test/":      false,
		"http://localhost:80@evil.test/":   false,
		"http://localhost.evil.test/":      false,
		"http://127.0.0.1.evil.test/":      false,
		"ftp://127.0.0.1/":                 false,
		"//localhost/cb":                   false,
		"http://169.254.169.254/latest/x/": false,
	} {
		if got := loopback.MatchString(raw); got != want {
			t.Errorf("%q: %v, want %v", raw, got, want)
		}
	}
}

func TestNonLoopbackTargetsRefused(t *testing.T) {
	s, srv := start(t, Options{ClientID: "id1", ClientSecret: "sec"})

	q := url.Values{"client_id": {"id1"}, "redirect_uri": {"http://core.test/cb"}, "response_type": {"code"}}
	resp, err := noRedirect().Get(srv.URL + "/oauth/authorize?" + q.Encode())
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("authorize to a remote redirect_uri: %d", resp.StatusCode)
	}

	form := url.Values{"client_id": {"id1"}, "client_secret": {"sec"}, "callback_url": {"http://core.test/webhooks/x"}}
	sub := postForm(t, srv.URL+"/api/v3/push_subscriptions", form)
	sub.Body.Close()
	if sub.StatusCode != http.StatusBadRequest {
		t.Errorf("subscription to a remote callback: %d", sub.StatusCode)
	}

	form.Set("callback_url", srv.URL+"/webhooks/x")
	form.Set("verify_token", "no spaces & no ampersands")
	tok := postForm(t, srv.URL+"/api/v3/push_subscriptions", form)
	tok.Body.Close()
	if tok.StatusCode != http.StatusBadRequest {
		t.Errorf("subscription with a non-URL-safe verify token: %d", tok.StatusCode)
	}

	if _, _, err := s.EmitWebhook(context.Background(), "http://core.test/webhooks/x", "create", 1); err == nil {
		t.Error("EmitWebhook to a remote URL: no error")
	}
}
