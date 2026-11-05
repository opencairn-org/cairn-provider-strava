package mockstrava

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strconv"
	"time"
)

// The /_mock/* control contract shared by the provider mocks:
//
//	GET  /_mock/healthz        {"ok":true}
//	GET  /_mock/stats          Stats
//	POST /_mock/activities     {"name","start"?,"streams"?} → 201 {"id"}
//	POST /_mock/webhooks/emit  {"url","aspect","activity_id"} → {"status","body"}
//	POST /_mock/fail-next      {"status","count","retry_after"?}
//	POST /_mock/reset          clears fail-next and revoked tokens, keeps data

func (s *Server) healthz(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

func (s *Server) getStats(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, s.Stats())
}

func decode(w http.ResponseWriter, r *http.Request, v any) bool {
	if err := json.NewDecoder(io.LimitReader(r.Body, 1<<20)).Decode(v); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "bad body: " + err.Error()})
		return false
	}
	return true
}

func (s *Server) controlAddActivity(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Name    string `json:"name"`
		Start   string `json:"start"`
		Streams *bool  `json:"streams"`
	}
	if !decode(w, r, &in) {
		return
	}
	start := s.now()
	if in.Start != "" {
		t, err := time.Parse(time.RFC3339, in.Start)
		if err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "start: " + err.Error()})
			return
		}
		start = t
	}
	if in.Name == "" {
		in.Name = "Ride " + start.UTC().Format("2006-01-02 15:04")
	}
	id := s.AddActivity(in.Name, start, in.Streams == nil || *in.Streams)
	writeJSON(w, http.StatusCreated, map[string]string{"id": strconv.FormatInt(id, 10)})
}

// webhookEvent is Strava's push event.
type webhookEvent struct {
	ObjectType     string         `json:"object_type"`
	ObjectID       int64          `json:"object_id"`
	AspectType     string         `json:"aspect_type"`
	OwnerID        int64          `json:"owner_id"`
	SubscriptionID int64          `json:"subscription_id"`
	EventTime      int64          `json:"event_time"`
	Updates        map[string]any `json:"updates"`
}

func (s *Server) controlEmitWebhook(w http.ResponseWriter, r *http.Request) {
	var in struct {
		URL        string `json:"url"`
		Aspect     string `json:"aspect"`
		ActivityID string `json:"activity_id"`
	}
	if !decode(w, r, &in) {
		return
	}
	id, err := strconv.ParseInt(in.ActivityID, 10, 64)
	if in.URL == "" || err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "url and a numeric activity_id are required"})
		return
	}
	if in.Aspect == "" {
		in.Aspect = "create"
	}
	status, body, err := s.EmitWebhook(r.Context(), in.URL, in.Aspect, id)
	if err != nil {
		writeJSON(w, http.StatusBadGateway, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"status": status, "body": body})
}

// EmitWebhook POSTs a Strava activity event to url and returns the reply.
func (s *Server) EmitWebhook(ctx context.Context, url, aspect string, activityID int64) (status int, body string, err error) {
	s.mu.Lock()
	subID := int64(1)
	if len(s.subs) > 0 {
		subID = s.subs[0].ID
	}
	s.mu.Unlock()
	ev := webhookEvent{
		ObjectType: "activity", ObjectID: activityID, AspectType: aspect, OwnerID: s.opts.AthleteID,
		SubscriptionID: subID, EventTime: s.now().Unix(), Updates: map[string]any{},
	}
	payload, _ := json.Marshal(ev)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(payload))
	if err != nil {
		return 0, "", err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := s.opts.Client.Do(req)
	if err != nil {
		return 0, "", err
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(io.LimitReader(resp.Body, 64<<10))
	s.mu.Lock()
	s.stats.WebhooksSent++
	s.mu.Unlock()
	return resp.StatusCode, string(b), nil
}

func (s *Server) controlFailNext(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Status     int `json:"status"`
		Count      int `json:"count"`
		RetryAfter int `json:"retry_after"`
	}
	if !decode(w, r, &in) {
		return
	}
	if in.Status < 400 || in.Status > 599 {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "status must be 4xx or 5xx"})
		return
	}
	if in.Count <= 0 {
		in.Count = 1
	}
	s.mu.Lock()
	s.fail = failNext{status: in.Status, count: in.Count, retryAfter: in.RetryAfter}
	s.mu.Unlock()
	writeJSON(w, http.StatusOK, map[string]any{"status": in.Status, "count": in.Count, "retry_after": in.RetryAfter})
}

// controlReset clears scripted failures and token revocations; data stays.
func (s *Server) controlReset(w http.ResponseWriter, _ *http.Request) {
	s.mu.Lock()
	s.fail = failNext{}
	for _, t := range s.access {
		t.revoked = false
	}
	s.mu.Unlock()
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}
