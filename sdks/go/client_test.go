package pushq

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestWebhookRoundTrip(t *testing.T) {
	secret := "whsec_dGVzdC1zZWNyZXQ="
	payload := []byte(`{"a":1}`)
	ts := int64(1_700_000_000)
	sig, err := Sign(secret, "q/t1", ts, payload)
	if err != nil {
		t.Fatal(err)
	}
	h := http.Header{}
	h.Set("webhook-id", "q/t1")
	h.Set("webhook-timestamp", "1700000000")
	h.Set("webhook-signature", "v1,AAAA "+sig)

	id, err := verifyAt(secret, h, payload, 5*time.Minute, time.Unix(ts, 0))
	if err != nil || id != "q/t1" {
		t.Fatalf("verify: id=%q err=%v", id, err)
	}
	if _, err := verifyAt(secret, h, append(payload, 'x'), 5*time.Minute, time.Unix(ts, 0)); err == nil {
		t.Fatal("tampered payload verified")
	}
	if _, err := verifyAt("whsec_d3Jvbmc=", h, payload, 5*time.Minute, time.Unix(ts, 0)); err == nil {
		t.Fatal("wrong secret verified")
	}
	if _, err := verifyAt(secret, h, payload, 5*time.Minute, time.Unix(ts+3600, 0)); err == nil {
		t.Fatal("stale timestamp verified")
	}
}

func TestClientPathsAndErrors(t *testing.T) {
	var gotPath, gotAuth string
	var gotBody map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.RequestURI()
		gotAuth = r.Header.Get("Authorization")
		gotBody = nil
		if r.Body != nil {
			json.NewDecoder(r.Body).Decode(&gotBody)
		}
		switch {
		case r.URL.Path == "/v1/queues/q1:pause":
			json.NewEncoder(w).Encode(map[string]any{"name": "q1", "state": "paused"})
		case r.URL.Path == "/v1/queues/missing/tasks/x:run":
			w.Header().Set("Content-Type", "application/problem+json")
			w.WriteHeader(404)
			json.NewEncoder(w).Encode(map[string]any{"code": "not_found", "detail": "no such task", "status": 404})
		default:
			json.NewEncoder(w).Encode(map[string]any{"name": "q1", "state": "running"})
		}
	}))
	defer srv.Close()

	c := New(srv.URL, "test-key")
	ctx := context.Background()

	q, err := c.PauseQueue(ctx, "q1")
	if err != nil || q.State != "paused" {
		t.Fatalf("pause: %+v err=%v", q, err)
	}
	if gotPath != "/v1/queues/q1:pause" {
		t.Fatalf("colon path mangled: %s", gotPath)
	}
	if gotAuth != "Bearer test-key" {
		t.Fatalf("auth header: %q", gotAuth)
	}

	_, err = c.RunTask(ctx, "missing", "x")
	if !IsCode(err, "not_found") {
		t.Fatalf("want not_found error, got %v", err)
	}

	// json_body wire format
	if _, err := c.CreateTask(ctx, "q1", TaskCreate{Path: "/p", JSONBody: map[string]any{"k": "v"}, DelaySeconds: Float64(5)}); err != nil {
		t.Fatal(err)
	}
	jb, ok := gotBody["json_body"].(map[string]any)
	if !ok || jb["k"] != "v" || gotBody["delay_seconds"] != float64(5) || gotBody["path"] != "/p" {
		t.Fatalf("task wire: %v", gotBody)
	}
}

func TestDefaultBaseURL(t *testing.T) {
	if got := New("", "k").baseURL; got != DefaultBaseURL {
		t.Errorf("New(\"\") base = %q, want %q", got, DefaultBaseURL)
	}
	if got := New("http://localhost:8080/", "k").baseURL; got != "http://localhost:8080" {
		t.Errorf("explicit base = %q", got)
	}
}
