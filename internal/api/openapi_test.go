package api

import (
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"sort"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"

	"github.com/blissfulrays/pushq/internal/auth"
)

func docServer() *Server {
	s := &Server{
		Auth:           auth.New(nil, nil, false),
		Log:            slog.New(slog.NewTextHandler(io.Discard, nil)),
		MetricsHandler: http.NotFoundHandler(),
	}
	s.Router()
	return s
}

// Every route has docs and every doc entry has a route, so the generated
// spec can neither miss an endpoint nor describe one that's gone.
func TestOpenAPIDocsMatchRoutes(t *testing.T) {
	s := docServer()
	routes := map[string]bool{}
	_ = chi.Walk(s.routes, func(method, route string, _ http.Handler, _ ...func(http.Handler) http.Handler) error {
		routes[method+" "+strings.ReplaceAll(route, "/*/", "/")] = true
		return nil
	})
	var missing, stale []string
	for r := range routes {
		if _, ok := routeDocs[r]; !ok {
			missing = append(missing, r)
		}
	}
	for r := range routeDocs {
		if !routes[r] {
			stale = append(stale, r)
		}
	}
	sort.Strings(missing)
	sort.Strings(stale)
	if len(missing) > 0 {
		t.Errorf("routes without an entry in routeDocs (openapi_routes.go): %v", missing)
	}
	if len(stale) > 0 {
		t.Errorf("routeDocs entries with no matching route: %v", stale)
	}
}

// The SDK and console documents split the routes between them: each route
// appears in exactly one.
func TestOpenAPIAudiencesPartitionRoutes(t *testing.T) {
	s := docServer()
	seen := map[string]Audience{}
	for _, aud := range []Audience{AudienceSDK, AudienceConsole} {
		raw, err := s.OpenAPISpec(aud)
		if err != nil {
			t.Fatal(err)
		}
		var spec struct {
			Paths map[string]map[string]any `json:"paths"`
		}
		if err := json.Unmarshal(raw, &spec); err != nil {
			t.Fatal(err)
		}
		for path, ops := range spec.Paths {
			for method := range ops {
				k := strings.ToUpper(method) + " " + path
				if prev, dup := seen[k]; dup {
					t.Errorf("%s is in both the %s and %s documents", k, prev, aud)
				}
				seen[k] = aud
			}
		}
	}
	if len(seen) != len(routeDocs) {
		t.Errorf("documents cover %d routes, routeDocs has %d", len(seen), len(routeDocs))
	}
	for k, want := range map[string]Audience{
		"POST /v1/queues/{queue}/tasks": AudienceSDK,
		"GET /v1/apps":                  AudienceSDK,
		"POST /v1/apps":                 AudienceConsole,
		"POST /v1/api-keys":             AudienceConsole,
		"POST /v1/auth/login":           AudienceConsole,
		"GET /healthz":                  AudienceSDK,
		"GET /docs/console":             AudienceConsole,
	} {
		if seen[k] != want {
			t.Errorf("%s: in %q, want %q", k, seen[k], want)
		}
	}
}

func TestOpenAPISpecIsConsistent(t *testing.T) {
	for _, aud := range []Audience{AudienceSDK, AudienceConsole} {
		t.Run(string(aud), func(t *testing.T) { checkSpec(t, docServer(), aud) })
	}
}

func checkSpec(t *testing.T, s *Server, aud Audience) {
	raw, err := s.OpenAPISpec(aud)
	if err != nil {
		t.Fatal(err)
	}
	var spec struct {
		OpenAPI    string                               `json:"openapi"`
		Paths      map[string]map[string]map[string]any `json:"paths"`
		Components struct {
			Schemas map[string]map[string]any `json:"schemas"`
		} `json:"components"`
		Undocumented []string `json:"x-undocumented-routes"`
	}
	if err := json.Unmarshal(raw, &spec); err != nil {
		t.Fatal(err)
	}
	if spec.OpenAPI != "3.1.0" || len(spec.Paths) < 12 || len(spec.Undocumented) > 0 {
		t.Fatalf("openapi=%q paths=%d undocumented=%v", spec.OpenAPI, len(spec.Paths), spec.Undocumented)
	}

	// Every $ref resolves, and every named schema has real fields (a type
	// that reflected to nothing — say an untyped map — would document nothing).
	refs := map[string]bool{}
	var walk func(v any)
	walk = func(v any) {
		switch x := v.(type) {
		case map[string]any:
			for k, vv := range x {
				if k == "$ref" {
					refs[strings.TrimPrefix(vv.(string), "#/components/schemas/")] = true
				}
				walk(vv)
			}
		case []any:
			for _, vv := range x {
				walk(vv)
			}
		}
	}
	var whole any
	_ = json.Unmarshal(raw, &whole)
	walk(whole)
	for name := range refs {
		sch, ok := spec.Components.Schemas[name]
		if !ok {
			t.Errorf("$ref to missing schema %q", name)
			continue
		}
		if props, _ := sch["properties"].(map[string]any); len(props) == 0 {
			t.Errorf("schema %q has no properties", name)
		}
	}

	for path, ops := range spec.Paths {
		for method, op := range ops {
			if op["summary"] == "" || op["responses"] == nil || op["operationId"] == "" {
				t.Errorf("%s %s is missing summary/responses/operationId", method, path)
			}
		}
	}

	// Spot-check that shapes come from the handlers' Go types.
	if aud == AudienceConsole {
		keyRole := spec.Components.Schemas["KeyCreateBody"]["properties"].(map[string]any)["role"].(map[string]any)
		if enum, _ := keyRole["enum"].([]any); len(enum) != 2 {
			t.Errorf("enum tag not applied: %v", keyRole)
		}
		return
	}
	post := spec.Paths["/v1/queues/{queue}/tasks"]["post"]
	if b, _ := json.Marshal(post); !strings.Contains(string(b), `"#/components/schemas/TaskCreate"`) ||
		!strings.Contains(string(b), `"201"`) {
		t.Errorf("POST /v1/queues/{queue}/tasks = %s", b)
	}
	task := spec.Components.Schemas["TaskCreate"]["properties"].(map[string]any)
	for _, f := range []string{"task_id", "url", "path", "json_body", "delay_seconds", "retry"} {
		if _, ok := task[f]; !ok {
			t.Errorf("TaskCreate schema lacks %q", f)
		}
	}
	if b, _ := json.Marshal(post); !strings.Contains(string(b), `"X-PushQ-App"`) {
		t.Errorf("queue routes don't document the %s header", AppHeader)
	}
}
