package api

import (
	"html/template"
	"net/http"

	"github.com/blissfulrays/pushq/web"
)

// swaggerUIVersion must match web/package.json's swagger-ui-dist: the CDN
// fallback is used only by binaries built without the dashboard assets.
const swaggerUIVersion = "5.34.1"

var docsPage = template.Must(template.New("docs").Parse(`<!doctype html>
<html lang="en">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<title>pushq API</title>
<link rel="icon" href="/favicon.svg">
<link rel="stylesheet" href="{{.Assets}}/swagger-ui.css">
<style>
  body { margin: 0; background: #fff; }
  .pq-bar { display: flex; align-items: center; justify-content: space-between; padding: 10px 20px;
            font: 14px system-ui, sans-serif; background: #18181b; color: #fafafa; }
  .pq-bar a { color: #fafafa; text-decoration: none; opacity: .8; }
  .pq-bar a:hover { opacity: 1; }
  .swagger-ui .topbar { display: none; }
</style>
</head>
<body>
<div class="pq-bar"><strong>pushq API</strong><a href="/">← Dashboard</a></div>
<div id="swagger"></div>
<script src="{{.Assets}}/swagger-ui-bundle.js"></script>
<script>
  window.ui = SwaggerUIBundle({
    url: "/openapi.json",
    dom_id: "#swagger",
    deepLinking: true,
    persistAuthorization: true,
    displayRequestDuration: true,
    // Signed in to the dashboard? "Try it out" can use that session; writes
    // with the session cookie need this header (CSRF protection).
    requestInterceptor: (req) => { req.headers["X-Requested-With"] = "pushq"; return req; },
  });
</script>
</body>
</html>`))

// handleDocs serves Swagger UI for the generated spec. Assets are embedded in
// the binary (so it works offline); a binary built without the dashboard
// falls back to the same pinned version on a CDN.
func (s *Server) handleDocs(w http.ResponseWriter, r *http.Request) {
	assets := "/docs"
	if !web.HasFile("docs/swagger-ui-bundle.js") {
		assets = "https://cdn.jsdelivr.net/npm/swagger-ui-dist@" + swaggerUIVersion
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_ = docsPage.Execute(w, struct{ Assets string }{assets})
}
