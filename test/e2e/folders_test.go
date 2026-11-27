package e2e

import (
	"net/url"
	"strings"
	"testing"
	"time"
)

// Queue names may contain '/' to arrange queues in folders. In API paths the
// name travels as a single percent-encoded segment (user%2Finstagram).
func TestFolderQueueNames(t *testing.T) {
	h := newHarness(t)
	const name = "user/instagram"
	qp := "/v1/queues/" + url.PathEscape(name) // user%2Finstagram

	h.upsertQueue(url.PathEscape(name), nil)
	h.upsertQueue("user", nil)        // a queue may share its name with a folder
	h.upsertQueue("users-other", nil) // must not match the "user/" prefix
	h.upsertQueue(url.PathEscape("user/tiktok/eu"), nil)

	got := h.mustDo("GET", qp, nil, 200)
	if got["name"] != name {
		t.Fatalf("name = %v", got["name"])
	}

	// Tasks: create, deliver, get, attempts, delete.
	h.mustDo("POST", qp+"/tasks", map[string]any{"task_id": "t1", "path": "/hook"}, 201)
	waitFor(t, 5*time.Second, "delivery", func() bool { return h.receiver.count("/hook") >= 1 })
	if q := h.receiver.requests()[0].Headers.Get("X-Pushq-Queue"); q != name {
		t.Errorf("X-Pushq-Queue = %q", q)
	}
	waitFor(t, 5*time.Second, "succeeded", func() bool {
		_, b := h.do("GET", qp+"/tasks/t1", nil)
		return b["state"] == "succeeded"
	})
	if b := h.mustDo("GET", qp+"/tasks/t1", nil, 200); b["queue"] != name {
		t.Errorf("task queue = %v", b["queue"])
	}
	h.mustDo("GET", qp+"/tasks/t1/attempts", nil, 200)
	h.mustDo("POST", qp+"/tasks:batch", map[string]any{"tasks": []any{
		map[string]any{"path": "/hook", "schedule_time": time.Now().Add(time.Hour).Format(time.RFC3339)},
	}}, 200)
	h.mustDo("GET", qp+"/stats", nil, 200)

	// Custom-method suffixes work after an encoded name.
	if b := h.mustDo("POST", qp+":pause", nil, 200); b["state"] != "paused" {
		t.Errorf("state after pause = %v", b["state"])
	}
	h.mustDo("POST", qp+":resume", nil, 200)
	h.mustDo("PATCH", qp, map[string]any{"rate_limits": map[string]any{"max_dispatches_per_second": 3}}, 200)

	// Folder listing via name_prefix.
	list := h.mustDo("GET", "/v1/queues?name_prefix="+url.QueryEscape("user/"), nil, 200)
	var names []string
	for _, q := range list["queues"].([]any) {
		names = append(names, q.(map[string]any)["name"].(string))
	}
	if strings.Join(names, ",") != "user/instagram,user/tiktok/eu" {
		t.Errorf("user/ listing = %v", names)
	}

	// The DLQ may live in a folder too.
	h.upsertQueue(url.PathEscape("user/instagram-dlq"), nil)
	h.mustDo("PATCH", qp, map[string]any{"dead_letter_queue": "user/instagram-dlq"}, 200)

	// Unencoded slashes don't route; the 404 explains why.
	_, nf := h.do("GET", "/v1/queues/user/instagram", nil)
	if d, _ := nf["detail"].(string); !strings.Contains(d, "%2F") {
		t.Errorf("404 detail = %q", d)
	}

	h.mustDo("DELETE", qp+"/tasks/t1", nil, 204)
	h.mustDo("DELETE", qp, nil, 204)
	h.mustDo("GET", qp, nil, 404)
}

func TestFolderQueueNameValidation(t *testing.T) {
	h := newHarness(t)
	for _, bad := range []string{
		"/lead", "trail/", "a//b", "a/-b", "A/b", "a/b c", "a/b_c", strings.Repeat("a", 201),
	} {
		status, _ := h.do("POST", "/v1/queues", map[string]any{
			"name": bad, "target": map[string]any{"base_url": h.receiver.URL},
		})
		if status != 400 {
			t.Errorf("name %q: status %d, want 400", bad, status)
		}
	}
	for _, good := range []string{"a", "a/b", "team-1/app/eu-west", strings.Repeat("a", 200)} {
		h.mustDo("POST", "/v1/queues", map[string]any{
			"name": good, "target": map[string]any{"base_url": h.receiver.URL},
		}, 201)
	}
}

// A PATCH that yields invalid merged config must be rejected without being
// persisted.
func TestPatchInvalidIsNotPersisted(t *testing.T) {
	h := newHarness(t)
	h.upsertQueue("p", map[string]any{"rate_limits": map[string]any{"max_dispatches_per_second": 5}})

	h.mustDo("PATCH", "/v1/queues/p", map[string]any{"dead_letter_queue": "p"}, 400)
	h.mustDo("PATCH", "/v1/queues/p", map[string]any{"target": map[string]any{"base_url": "ftp://x"}}, 400)

	q := h.mustDo("GET", "/v1/queues/p", nil, 200)
	if dlq, _ := q["dead_letter_queue"].(string); dlq != "" {
		t.Errorf("invalid dead_letter_queue persisted: %q", dlq)
	}
	if bu := q["target"].(map[string]any)["base_url"]; bu != h.receiver.URL {
		t.Errorf("invalid base_url persisted: %v", bu)
	}
}

// Headers net/http can't send are rejected at create time rather than
// failing every delivery attempt.
func TestInvalidHeadersRejected(t *testing.T) {
	h := newHarness(t)
	h.upsertQueue("hq", nil)
	for _, hdr := range []map[string]any{
		{"X Foo": "1"},
		{"X-Foo": "a\r\nX-Injected: 1"},
		{"X-Foo": "nul\u0000"},
	} {
		if status, b := h.do("POST", "/v1/queues/hq/tasks", map[string]any{"path": "/x", "headers": hdr}); status != 400 {
			t.Errorf("headers %q: status %d (%v), want 400", hdr, status, b)
		}
	}
	status, _ := h.do("PUT", "/v1/queues/hq2", map[string]any{
		"target": map[string]any{"base_url": h.receiver.URL, "headers": map[string]any{"Bad Name": "x"}},
	})
	if status != 400 {
		t.Errorf("queue header override with bad name: status %d, want 400", status)
	}
	h.mustDo("POST", "/v1/queues/hq/tasks", map[string]any{"path": "/x", "headers": map[string]any{"X-Ok": "tab\tok"}}, 201)
}

// A huge delay_seconds must be rejected, not overflow into the past and run
// immediately.
func TestHugeDelayRejected(t *testing.T) {
	h := newHarness(t)
	h.upsertQueue("dq", nil)
	for _, d := range []float64{1e12, 1e300} {
		if status, b := h.do("POST", "/v1/queues/dq/tasks", map[string]any{"path": "/x", "delay_seconds": d}); status != 400 {
			t.Errorf("delay_seconds=%g: status %d (%v), want 400", d, status, b)
		}
	}
	time.Sleep(200 * time.Millisecond)
	if n := h.receiver.count(""); n != 0 {
		t.Errorf("%d deliveries happened", n)
	}
}
