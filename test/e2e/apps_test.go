package e2e

import (
	"testing"
)

func names(out map[string]any, key string) []string {
	var ns []string
	items, _ := out[key].([]any)
	for _, it := range items {
		ns = append(ns, it.(map[string]any)["name"].(string))
	}
	return ns
}

func eq(a []string, b ...string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// Applications isolate queues: each has its own queue namespace, API keys
// can be restricted to one, and the Default application keeps the queues
// that existed before applications did.
func TestApplicationsIsolateQueues(t *testing.T) {
	base := newAccountsServer(t, nil)

	// A queue created before any account exists lands in the Default app.
	anon := newClient(t, base)
	anon.must("PUT", "/v1/queues/legacy", map[string]any{}, 201)

	admin := newClient(t, base)
	admin.must("POST", "/v1/auth/setup", map[string]any{"email": "a@example.com", "name": "Ada", "password": "admin password"}, 201)

	apps := admin.must("GET", "/v1/apps", nil, 200)
	if got := names(apps, "apps"); !eq(got, "Default") {
		t.Fatalf("apps = %v", got)
	}
	def := jsonNum(apps["apps"].([]any)[0].(map[string]any)["id"].(float64))

	billing := admin.must("POST", "/v1/apps", map[string]any{"name": "Billing"}, 201)
	billingID := jsonNum(billing["id"].(float64))
	admin.must("POST", "/v1/apps", map[string]any{"name": "billing"}, 409) // names are case-insensitively unique
	admin.must("POST", "/v1/apps", map[string]any{"name": " "}, 400)

	// The same queue name in two applications is two queues.
	admin.must("PUT", "/v1/queues/emails", map[string]any{"rate_limits": map[string]any{"max_dispatches_per_second": 5}}, 201)
	admin.app = billingID
	admin.must("PUT", "/v1/queues/emails", map[string]any{"rate_limits": map[string]any{"max_dispatches_per_second": 9}}, 201)
	admin.must("POST", "/v1/queues/emails/tasks", map[string]any{"task_id": "t1", "url": "http://127.0.0.1:1/x"}, 201)
	if q := admin.must("GET", "/v1/queues/emails", nil, 200); q["name"] != "emails" ||
		q["rate_limits"].(map[string]any)["max_dispatches_per_second"] != 9.0 {
		t.Fatalf("billing emails = %v", q)
	}
	if got := names(admin.must("GET", "/v1/queues", nil, 200), "queues"); !eq(got, "emails") {
		t.Fatalf("billing queues = %v", got)
	}
	admin.must("GET", "/v1/queues/legacy", nil, 404)

	admin.app = def
	if got := names(admin.must("GET", "/v1/queues", nil, 200), "queues"); !eq(got, "emails", "legacy") {
		t.Fatalf("default queues = %v", got)
	}
	admin.must("GET", "/v1/queues/emails/tasks/t1", nil, 404)
	admin.app = "" // no header = Default
	if q := admin.must("GET", "/v1/queues/emails", nil, 200); q["rate_limits"].(map[string]any)["max_dispatches_per_second"] != 5.0 {
		t.Fatalf("default emails = %v", q)
	}
	admin.app = "999"
	admin.must("GET", "/v1/queues", nil, 404)
	admin.app = "nope"
	admin.must("GET", "/v1/queues", nil, 400)
	admin.app = ""

	// A key restricted to Billing reaches only Billing, with or without the header.
	k := admin.must("POST", "/v1/api-keys", map[string]any{"name": "billing-svc", "role": "admin", "app_id": billing["id"]}, 201)
	if key := k["key"].(map[string]any); key["app_name"] != "Billing" {
		t.Fatalf("key = %v", key)
	}
	admin.must("POST", "/v1/api-keys", map[string]any{"name": "x", "role": "writer", "app_id": 999}, 400)
	svc := newClient(t, base)
	svc.bearer = k["secret"].(string)
	if got := names(svc.must("GET", "/v1/queues", nil, 200), "queues"); !eq(got, "emails") {
		t.Fatalf("billing key queues = %v", got)
	}
	svc.must("GET", "/v1/queues/legacy", nil, 404)
	svc.app = def
	svc.must("GET", "/v1/queues", nil, 403)
	svc.app = billingID
	svc.must("GET", "/v1/queues/emails/tasks/t1", nil, 200)
	if got := names(svc.must("GET", "/v1/apps", nil, 200), "apps"); !eq(got, "Billing") {
		t.Fatalf("billing key apps = %v", got)
	}
	// ...and, even as an admin key, can't manage the team, keys or applications.
	svc.must("GET", "/v1/api-keys", nil, 403)
	svc.must("POST", "/v1/apps", map[string]any{"name": "Mine"}, 403)

	// An unrestricted key picks the application per request.
	k2 := admin.must("POST", "/v1/api-keys", map[string]any{"name": "ops", "role": "admin"}, 201)
	ops := newClient(t, base)
	ops.bearer = k2["secret"].(string)
	if got := names(ops.must("GET", "/v1/apps", nil, 200), "apps"); !eq(got, "Default", "Billing") {
		t.Fatalf("ops apps = %v", got)
	}
	ops.app = billingID
	ops.must("GET", "/v1/queues/emails/tasks/t1", nil, 200)

	// Icons: small raster data: URLs only; "" removes it.
	png := "data:image/png;base64,iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAYAAAAfFcSJAAAADUlEQVR42mNk+M9QDwADhgGAWjR9awAAAABJRU5ErkJggg=="
	if a := admin.must("PATCH", "/v1/apps/"+billingID, map[string]any{"icon": png}, 200); a["icon"] != png || a["name"] != "Billing" {
		t.Fatalf("icon set = %v", a)
	}
	admin.must("PATCH", "/v1/apps/"+billingID, map[string]any{"icon": "data:image/svg+xml;base64,PHN2Zy8+"}, 400)
	admin.must("PATCH", "/v1/apps/"+billingID, map[string]any{"icon": "data:image/png;base64,***"}, 400)
	if a := admin.must("PATCH", "/v1/apps/"+billingID, map[string]any{"icon": ""}, 200); a["icon"] != nil {
		t.Fatalf("icon cleared = %v", a)
	}
	withIcon := admin.must("POST", "/v1/apps", map[string]any{"name": "Iconic", "icon": png}, 201)
	if withIcon["icon"] != png {
		t.Fatalf("created with icon = %v", withIcon)
	}
	admin.must("DELETE", "/v1/apps/"+jsonNum(withIcon["id"].(float64)), nil, 204)

	// Rename; delete only once empty; the Default application stays.
	if a := admin.must("PATCH", "/v1/apps/"+billingID, map[string]any{"name": "Payments"}, 200); a["name"] != "Payments" {
		t.Fatalf("renamed = %v", a)
	}
	admin.must("DELETE", "/v1/apps/"+billingID, nil, 400)
	admin.app = billingID
	admin.must("DELETE", "/v1/queues/emails", nil, 204)
	admin.app = ""
	admin.must("DELETE", "/v1/apps/"+billingID, nil, 204)
	admin.must("DELETE", "/v1/apps/"+def, nil, 400)
	svc.app = ""
	svc.must("GET", "/v1/queues", nil, 401) // its key went with the application
	if got := names(admin.must("GET", "/v1/apps", nil, 200), "apps"); !eq(got, "Default") {
		t.Fatalf("apps after delete = %v", got)
	}
}
