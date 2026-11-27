package api

import (
	"encoding/json"
	"fmt"
	"net/http"
	"reflect"
	"sort"
	"strings"
	"sync"
	"time"
	"unicode"

	"github.com/go-chi/chi/v5"

	"github.com/blissfulrays/pushq/internal/version"
)

// The OpenAPI document is generated at runtime, not written by hand:
//
//   - which endpoints exist comes from walking the router's own route table,
//     so a route can't be missing from the docs;
//   - request and response shapes come from reflecting over the Go types the
//     handlers decode and encode (json tags, plus optional `doc:"…"` and
//     `enum:"a,b"` tags for descriptions and allowed values);
//   - summaries and access rules come from routeDocs (openapi_routes.go),
//     which a test keeps in step with the router in both directions.

// SpecRoute is one documented endpoint (as seen by the generator).
type SpecRoute struct {
	Method string
	Path   string
}

// openAPI builds the spec for router.
func openAPI(router chi.Routes) (map[string]any, error) {
	g := &schemaGen{components: map[string]any{}, names: map[reflect.Type]string{}}
	paths := map[string]map[string]any{}
	var undocumented []string

	err := chi.Walk(router, func(method, route string, _ http.Handler, _ ...func(http.Handler) http.Handler) error {
		route = strings.TrimSuffix(route, "/*")
		if route == "" || route == "/*" {
			return nil
		}
		route = strings.ReplaceAll(route, "/*/", "/")
		op, ok := routeDocs[method+" "+route]
		if !ok {
			undocumented = append(undocumented, method+" "+route)
			op = opDoc{Summary: method + " " + route, Tag: "Other"}
		}
		if paths[route] == nil {
			paths[route] = map[string]any{}
		}
		paths[route][strings.ToLower(method)] = g.operation(method, route, op)
		return nil
	})
	if err != nil {
		return nil, err
	}
	sort.Strings(undocumented)

	g.components["Problem"] = g.inline(reflect.TypeOf(problem{}))
	spec := map[string]any{
		"openapi": "3.1.0",
		"info": map[string]any{
			"title":   "pushq API",
			"version": version.Version,
			"description": strings.TrimSpace(`
pushq is a self-hosted HTTP push task queue: an open-source alternative to Google Cloud Tasks.
Tasks are delivered as HTTP requests to your endpoints with per-queue rate limits, concurrency caps
and Cloud Tasks-compatible retries.

**Authentication.** Send an API key as ` + "`Authorization: Bearer <key>`" + `. The dashboard uses a
session cookie instead; requests that change something with the cookie must also send
` + "`X-Requested-With: pushq`" + `.

**Queue names** may contain ` + "`/`" + ` (folders, e.g. ` + "`user/instagram`" + `); in paths the whole
name is one URL-encoded segment (` + "`user%2Finstagram`" + `).

**Errors** are RFC 9457 problem+json with a stable ` + "`code`" + `.

This document is generated from the running server's routes.`),
		},
		"tags":  tagOrder(),
		"paths": paths,
		"components": map[string]any{
			"schemas": g.components,
			"securitySchemes": map[string]any{
				"apiKey": map[string]any{"type": "http", "scheme": "bearer",
					"description": "An API key from the dashboard (Settings → API keys) or the server config."},
				"session": map[string]any{"type": "apiKey", "in": "cookie", "name": "pushq_session",
					"description": "Dashboard sign-in session."},
			},
		},
	}
	if len(undocumented) > 0 {
		spec["x-undocumented-routes"] = undocumented
	}
	return spec, nil
}

func tagOrder() []map[string]any {
	out := []map[string]any{}
	for _, t := range []struct{ name, desc string }{
		{"Queues", "Create, configure, pause and purge queues."},
		{"Tasks", "Enqueue, inspect, run and delete tasks."},
		{"Sign-in", "Dashboard sign-in. Self-hosted: local accounts. SaaS mode: email code or GitHub."},
		{"Account", "The signed-in user."},
		{"Teams", "SaaS mode: teams (WorkOS Organizations) and switching between them."},
		{"Team members", "Members and invites of the current team (or the server, when self-hosted)."},
		{"API keys", "Keys for apps and scripts."},
		{"System", "Health, readiness, metrics and these docs."},
	} {
		out = append(out, map[string]any{"name": t.name, "description": t.desc})
	}
	return out
}

func (g *schemaGen) operation(method, route string, op opDoc) map[string]any {
	o := map[string]any{
		"summary":     op.Summary,
		"tags":        []string{op.Tag},
		"operationId": operationID(method, route),
	}
	desc := op.Description
	if note := accessNote(op.Access); note != "" {
		desc = strings.TrimSpace(desc + "\n\n" + note)
	}
	if desc != "" {
		o["description"] = desc
	}
	if op.Access == accessPublic {
		o["security"] = []any{}
	} else {
		o["security"] = []any{map[string]any{"apiKey": []any{}}, map[string]any{"session": []any{}}}
	}

	var params []any
	for _, name := range pathParams(route) {
		params = append(params, map[string]any{
			"name": name, "in": "path", "required": true,
			"description": pathParamDocs[name], "schema": map[string]any{"type": "string"},
		})
	}
	for _, q := range op.Query {
		p := map[string]any{"name": q.Name, "in": "query", "description": q.Doc, "schema": map[string]any{"type": "string"}}
		if len(q.Enum) > 0 {
			p["schema"] = map[string]any{"type": "string", "enum": q.Enum}
		}
		if q.Integer {
			p["schema"] = map[string]any{"type": "integer"}
		}
		params = append(params, p)
	}
	if len(params) > 0 {
		o["parameters"] = params
	}

	if op.Request != nil {
		o["requestBody"] = map[string]any{
			"required": true,
			"content":  map[string]any{"application/json": map[string]any{"schema": g.schema(reflect.TypeOf(op.Request))}},
		}
	}

	problemRef := map[string]any{"content": map[string]any{
		"application/problem+json": map[string]any{"schema": map[string]any{"$ref": "#/components/schemas/Problem"}}}}
	responses := map[string]any{}
	status := op.Status
	switch {
	case op.Redirect:
		responses["302"] = map[string]any{"description": "Redirects the browser (see description)."}
	case op.ContentType != "":
		if status == 0 {
			status = http.StatusOK
		}
		responses[fmt.Sprint(status)] = map[string]any{"description": "OK",
			"content": map[string]any{op.ContentType: map[string]any{"schema": map[string]any{"type": "string"}}}}
	case op.Response != nil:
		if status == 0 {
			status = http.StatusOK
		}
		responses[fmt.Sprint(status)] = map[string]any{"description": http.StatusText(status),
			"content": map[string]any{"application/json": map[string]any{"schema": g.schema(reflect.TypeOf(op.Response))}}}
	default:
		if status == 0 {
			status = http.StatusNoContent
		}
		responses[fmt.Sprint(status)] = map[string]any{"description": http.StatusText(status)}
	}
	if !op.Redirect && op.ContentType == "" {
		responses["4XX"] = map[string]any{"description": "Client error (problem+json with a stable `code`).", "content": problemRef["content"]}
		responses["5XX"] = map[string]any{"description": "Server error.", "content": problemRef["content"]}
	}
	o["responses"] = responses
	return o
}

func accessNote(a access) string {
	switch a {
	case accessPublic:
		return "No credentials needed."
	case accessRead:
		return "**Access:** read — any role (writer/admin keys; viewer, member, admin users)."
	case accessWrite:
		return "**Access:** write — writer/admin keys; member or admin users."
	case accessList:
		return "**Access:** list — admin keys; any dashboard user. (Writer keys can't list.)"
	case accessDeleteQueue:
		return "**Access:** delete queues — admin keys; member or admin users."
	case accessManage:
		return "**Access:** manage — admin keys; admin users."
	case accessUser:
		return "**Access:** a signed-in dashboard user (session cookie)."
	case accessOperator:
		return "**Access:** admin keys. In SaaS mode, only the operator's config-file keys."
	}
	return ""
}

var pathParamDocs = map[string]string{
	"queue":  "Queue name, URL-encoded as one segment (user/instagram → user%2Finstagram).",
	"taskID": "Task ID.",
	"id":     "Numeric ID.",
	"token":  "The secret token from an invite or password-reset link.",
}

func pathParams(route string) []string {
	var out []string
	for {
		i := strings.IndexByte(route, '{')
		if i < 0 {
			return out
		}
		j := strings.IndexByte(route[i:], '}')
		if j < 0 {
			return out
		}
		out = append(out, route[i+1:i+j])
		route = route[i+j+1:]
	}
}

// operationID turns "POST /v1/queues/{queue}:pause" into "postQueuesQueuePause".
func operationID(method, route string) string {
	var b strings.Builder
	b.WriteString(strings.ToLower(method))
	upper := true
	for _, r := range strings.TrimPrefix(route, "/v1") {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			if upper {
				r = unicode.ToUpper(r)
			}
			b.WriteRune(r)
			upper = false
		} else {
			upper = true
		}
	}
	return b.String()
}

// --- schemas from Go types ---

type schemaGen struct {
	components map[string]any
	names      map[reflect.Type]string
}

var timeType = reflect.TypeOf(time.Time{})
var rawJSONType = reflect.TypeOf(json.RawMessage{})

// schema returns a schema for t, registering named structs as components.
func (g *schemaGen) schema(t reflect.Type) map[string]any {
	for t.Kind() == reflect.Pointer {
		t = t.Elem()
	}
	switch {
	case t == timeType:
		return map[string]any{"type": "string", "format": "date-time"}
	case t == rawJSONType:
		return map[string]any{}
	}
	switch t.Kind() {
	case reflect.String:
		return map[string]any{"type": "string"}
	case reflect.Bool:
		return map[string]any{"type": "boolean"}
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64,
		reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		return map[string]any{"type": "integer"}
	case reflect.Float32, reflect.Float64:
		return map[string]any{"type": "number"}
	case reflect.Slice, reflect.Array:
		if t.Elem().Kind() == reflect.Uint8 {
			return map[string]any{"type": "string", "format": "byte"}
		}
		return map[string]any{"type": "array", "items": g.schema(t.Elem())}
	case reflect.Map:
		return map[string]any{"type": "object", "additionalProperties": g.schema(t.Elem())}
	case reflect.Interface:
		return map[string]any{} // any JSON value
	case reflect.Struct:
		if t.Name() == "" {
			return g.inline(t)
		}
		name, ok := g.names[t]
		if !ok {
			name = schemaName(t, g.names)
			g.names[t] = name
			g.components[name] = map[string]any{} // placeholder for recursion
			g.components[name] = g.inline(t)
		}
		return map[string]any{"$ref": "#/components/schemas/" + name}
	}
	return map[string]any{}
}

// inline describes a struct's JSON fields (embedded structs are flattened,
// as encoding/json does).
func (g *schemaGen) inline(t reflect.Type) map[string]any {
	props := map[string]any{}
	g.addFields(t, props)
	return map[string]any{"type": "object", "properties": props}
}

func (g *schemaGen) addFields(t reflect.Type, props map[string]any) {
	for i := 0; i < t.NumField(); i++ {
		f := t.Field(i)
		tag := f.Tag.Get("json")
		if tag == "-" {
			continue
		}
		name, _, _ := strings.Cut(tag, ",")
		if f.Anonymous && name == "" {
			ft := f.Type
			for ft.Kind() == reflect.Pointer {
				ft = ft.Elem()
			}
			if ft.Kind() == reflect.Struct {
				g.addFields(ft, props)
				continue
			}
		}
		if !f.IsExported() {
			continue
		}
		if name == "" {
			name = f.Name
		}
		s := g.schema(f.Type)
		if doc := f.Tag.Get("doc"); doc != "" || f.Tag.Get("enum") != "" {
			// A $ref can't carry siblings in every tool; wrap it.
			if _, isRef := s["$ref"]; isRef {
				s = map[string]any{"allOf": []any{s}}
			} else {
				cp := map[string]any{}
				for k, v := range s {
					cp[k] = v
				}
				s = cp
			}
			if doc != "" {
				s["description"] = doc
			}
			if enum := f.Tag.Get("enum"); enum != "" {
				s["enum"] = strings.Split(enum, ",")
			}
		}
		props[name] = s
	}
}

// schemaName gives a type a readable, unique component name: "userDTO" ->
// "User", "teamNameBody" -> "TeamNameBody", "types.QueueResponse" -> "QueueResponse".
func schemaName(t reflect.Type, taken map[reflect.Type]string) string {
	name := strings.TrimSuffix(t.Name(), "DTO")
	if name == "" {
		name = "Object"
	}
	r := []rune(name)
	r[0] = unicode.ToUpper(r[0])
	name = string(r)
	used := map[string]bool{}
	for _, n := range taken {
		used[n] = true
	}
	base := name
	for i := 2; used[name]; i++ {
		name = fmt.Sprintf("%s%d", base, i)
	}
	return name
}

// --- serving ---

// specCache holds a server's generated spec (routes don't change after start).
type specCache struct {
	once sync.Once
	json []byte
	err  error
}

func (s *Server) handleOpenAPI(w http.ResponseWriter, r *http.Request) {
	s.spec.once.Do(func() { s.spec.json, s.spec.err = s.OpenAPISpec() })
	if s.spec.err != nil {
		s.writeErr(w, r, s.spec.err)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-cache")
	w.Write(s.spec.json)
}

// OpenAPISpec returns the generated spec as JSON (for `pushq openapi`).
func (s *Server) OpenAPISpec() ([]byte, error) {
	if s.routes == nil {
		s.Router()
	}
	spec, err := openAPI(s.routes)
	if err != nil {
		return nil, err
	}
	return json.MarshalIndent(spec, "", "  ")
}
