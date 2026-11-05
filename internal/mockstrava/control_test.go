package mockstrava

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestHealthzAndStats(t *testing.T) {
	_, base, _ := seeded(t, Options{Activities: 2})
	var ok map[string]bool
	decodeBody(t, get(t, base+"/_mock/healthz", ""), &ok)
	if !ok["ok"] {
		t.Error("healthz")
	}
	var st map[string]int
	decodeBody(t, get(t, base+"/_mock/stats", ""), &st)
	want := []string{"refreshes", "exchanges", "api_calls", "unauthorized", "rate_limited_served", "webhooks_sent", "activities"}
	for _, k := range want {
		if _, present := st[k]; !present {
			t.Errorf("stats lacks %s: %v", k, st)
		}
	}
	if st["activities"] != 2 {
		t.Errorf("activities = %d", st["activities"])
	}
}

func TestControlAddActivity(t *testing.T) {
	_, base, tok := seeded(t, Options{Activities: 1})
	var created map[string]string
	resp := postJSON(t, base+"/_mock/activities", map[string]any{"name": "Lunch ride"})
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("add: %d", resp.StatusCode)
	}
	decodeBody(t, resp, &created)
	if created["id"] != "1000002" {
		t.Fatalf("id = %v", created)
	}
	var a activity
	decodeBody(t, get(t, base+"/api/v3/activities/"+created["id"], tok), &a)
	if a.Name != "Lunch ride" || a.StartDate != fixedNow.Format(time.RFC3339) {
		t.Errorf("added = %+v", a)
	}
	st := get(t, base+"/api/v3/activities/"+created["id"]+"/streams?keys=time", tok)
	st.Body.Close()
	if st.StatusCode != http.StatusOK {
		t.Errorf("default streams: %d", st.StatusCode)
	}

	resp = postJSON(t, base+"/_mock/activities", map[string]any{"start": "2026-01-02T03:04:05Z", "streams": false})
	decodeBody(t, resp, &created)
	decodeBody(t, get(t, base+"/api/v3/activities/"+created["id"], tok), &a)
	if a.StartDate != "2026-01-02T03:04:05Z" || !strings.HasPrefix(a.Name, "Ride 2026-01-02") {
		t.Errorf("explicit start = %+v", a)
	}
	st = get(t, base+"/api/v3/activities/"+created["id"]+"/streams?keys=time", tok)
	st.Body.Close()
	if st.StatusCode != http.StatusNotFound {
		t.Errorf("streams=false: %d", st.StatusCode)
	}

	for _, body := range []string{`{"start":"yesterday"}`, `{bad`} {
		r, err := http.Post(base+"/_mock/activities", "application/json", strings.NewReader(body))
		if err != nil {
			t.Fatal(err)
		}
		r.Body.Close()
		if r.StatusCode != http.StatusBadRequest {
			t.Errorf("%s: %d", body, r.StatusCode)
		}
	}
}

// sink records webhook POSTs and answers Strava's handshake.
type sink struct {
	mu     sync.Mutex
	events []webhookEvent
	verify string
	status int
}

func (k *sink) handler(w http.ResponseWriter, r *http.Request) {
	k.mu.Lock()
	defer k.mu.Unlock()
	if r.Method == http.MethodGet {
		if r.URL.Query().Get("hub.verify_token") != k.verify {
			http.Error(w, "forbidden", http.StatusForbidden)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]string{"hub.challenge": r.URL.Query().Get("hub.challenge")})
		return
	}
	var ev webhookEvent
	b, _ := io.ReadAll(r.Body)
	_ = json.Unmarshal(b, &ev)
	k.events = append(k.events, ev)
	status := k.status
	if status == 0 {
		status = http.StatusOK
	}
	w.WriteHeader(status)
	_, _ = w.Write([]byte("thanks"))
}

func TestEmitWebhook(t *testing.T) {
	s, base, _ := seeded(t, Options{})
	k := &sink{verify: DefaultVerifyToken}
	target := httptest.NewServer(http.HandlerFunc(k.handler))
	t.Cleanup(target.Close)

	var reply struct {
		Status int    `json:"status"`
		Body   string `json:"body"`
	}
	resp := postJSON(t, base+"/_mock/webhooks/emit", map[string]any{"url": target.URL + "/webhooks/strava-fetcher-strava", "aspect": "update", "activity_id": "1000001"})
	decodeBody(t, resp, &reply)
	if reply.Status != http.StatusOK || reply.Body != "thanks" {
		t.Fatalf("reply = %+v", reply)
	}
	k.mu.Lock()
	ev := k.events[0]
	k.mu.Unlock()
	if ev.ObjectType != "activity" || ev.ObjectID != 1000001 || ev.AspectType != "update" || ev.OwnerID != DefaultAthleteID || ev.SubscriptionID != 1 || ev.EventTime != fixedNow.Unix() || ev.Updates == nil {
		t.Errorf("event = %+v", ev)
	}
	// The default aspect is create; core's status is relayed as is.
	k.status = http.StatusBadGateway
	resp = postJSON(t, base+"/_mock/webhooks/emit", map[string]any{"url": target.URL, "activity_id": "7"})
	decodeBody(t, resp, &reply)
	if reply.Status != http.StatusBadGateway || k.events[1].AspectType != "create" {
		t.Errorf("reply = %+v event %+v", reply, k.events[1])
	}
	if st := s.Stats(); st.WebhooksSent != 2 {
		t.Errorf("webhooks_sent = %d", st.WebhooksSent)
	}

	bad := postJSON(t, base+"/_mock/webhooks/emit", map[string]any{"url": "http://127.0.0.1:1/x", "activity_id": "1"})
	bad.Body.Close()
	missing := postJSON(t, base+"/_mock/webhooks/emit", map[string]any{"activity_id": "x"})
	missing.Body.Close()
	if bad.StatusCode != http.StatusBadGateway || missing.StatusCode != http.StatusBadRequest {
		t.Errorf("bad %d missing %d", bad.StatusCode, missing.StatusCode)
	}
}

func TestSubscriptions(t *testing.T) {
	_, base, _ := seeded(t, Options{ClientID: "id1", ClientSecret: "sec"})
	k := &sink{verify: "custom"}
	target := httptest.NewServer(http.HandlerFunc(k.handler))
	t.Cleanup(target.Close)
	creds := url.Values{"client_id": {"id1"}, "client_secret": {"sec"}}

	form := url.Values{"client_id": {"id1"}, "client_secret": {"sec"}, "callback_url": {target.URL + "/webhooks/x?a=1"}, "verify_token": {"custom"}}
	resp := postForm(t, base+"/api/v3/push_subscriptions", form)
	var sub subscription
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("create: %d", resp.StatusCode)
	}
	decodeBody(t, resp, &sub)
	if sub.ID != 1 || sub.CallbackURL != form.Get("callback_url") {
		t.Errorf("sub = %+v", sub)
	}
	var list []subscription
	decodeBody(t, get(t, base+"/api/v3/push_subscriptions?"+creds.Encode(), ""), &list)
	if len(list) != 1 {
		t.Errorf("list = %+v", list)
	}

	// The default verify token does not match this sink; wrong creds; no callback.
	form.Del("verify_token")
	failed := postForm(t, base+"/api/v3/push_subscriptions", form)
	failed.Body.Close()
	form.Set("verify_token", "custom")
	form.Set("client_secret", "nope")
	denied := postForm(t, base+"/api/v3/push_subscriptions", form)
	denied.Body.Close()
	noCallback := postForm(t, base+"/api/v3/push_subscriptions", creds)
	noCallback.Body.Close()
	unlisted := get(t, base+"/api/v3/push_subscriptions", "")
	unlisted.Body.Close()
	if failed.StatusCode != http.StatusBadRequest || denied.StatusCode != http.StatusUnauthorized || noCallback.StatusCode != http.StatusBadRequest || unlisted.StatusCode != http.StatusUnauthorized {
		t.Errorf("failed %d denied %d noCallback %d unlisted %d", failed.StatusCode, denied.StatusCode, noCallback.StatusCode, unlisted.StatusCode)
	}

	req, _ := http.NewRequest(http.MethodDelete, base+"/api/v3/push_subscriptions/1", nil)
	del, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	del.Body.Close()
	again, _ := http.DefaultClient.Do(req)
	again.Body.Close()
	if del.StatusCode != http.StatusNoContent || again.StatusCode != http.StatusNotFound {
		t.Errorf("delete %d again %d", del.StatusCode, again.StatusCode)
	}
}
