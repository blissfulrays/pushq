// Package api implements the pushq REST API (see api/openapi.yaml).
package api

import (
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/blissfulrays/pushq/internal/auth"
	"github.com/blissfulrays/pushq/internal/config"
	"github.com/blissfulrays/pushq/internal/store"
	"github.com/blissfulrays/pushq/web"
)

type Server struct {
	Store          store.Store
	Auth           *auth.Auth
	Log            *slog.Logger
	Limits         config.Limits
	MetricsPublic  bool
	MetricsHandler http.Handler
	Now            func() time.Time
	Role           func() string // "leader" | "follower" for readyz
}

func (s *Server) Router() http.Handler {
	r := chi.NewRouter()
	r.Use(s.recoverer, s.accessLog)

	r.Get("/healthz", s.handleHealthz)
	r.Get("/readyz", s.handleReadyz)
	if s.MetricsHandler != nil {
		if s.MetricsPublic {
			r.Handle("/metrics", s.MetricsHandler)
		} else {
			r.With(s.requireRole(auth.RoleAdmin)).Handle("/metrics", s.MetricsHandler)
		}
	}

	r.Route("/v1", func(r chi.Router) {
		r.With(s.requireRole(auth.RoleWriter)).Get("/version", s.handleVersion)

		// Writer endpoints.
		r.Group(func(r chi.Router) {
			r.Use(s.requireRole(auth.RoleWriter))

			r.Post("/queues", s.handleQueueCreate)
			r.Put("/queues/{queue}", s.handleQueuePut)
			r.Get("/queues/{queue}", s.handleQueueGet)
			r.Patch("/queues/{queue}", s.handleQueuePatch)
			r.Post("/queues/{queue}:pause", s.handleQueuePause)
			r.Post("/queues/{queue}:resume", s.handleQueueResume)
			r.Post("/queues/{queue}:purge", s.handleQueuePurge)
			r.Get("/queues/{queue}/stats", s.handleQueueStats)

			r.Post("/queues/{queue}/tasks", s.handleTaskCreate)
			r.Post("/queues/{queue}/tasks:batch", s.handleTaskBatch)
			r.Get("/queues/{queue}/tasks", s.handleTaskList)
			r.Get("/queues/{queue}/tasks/{taskID}", s.handleTaskGet)
			r.Delete("/queues/{queue}/tasks/{taskID}", s.handleTaskDelete)
			r.Post("/queues/{queue}/tasks/{taskID}:run", s.handleTaskRun)
			r.Get("/queues/{queue}/tasks/{taskID}/attempts", s.handleAttemptsList)
		})

		// Admin endpoints.
		r.Group(func(r chi.Router) {
			r.Use(s.requireRole(auth.RoleAdmin))
			r.Get("/queues", s.handleQueueList)
			r.Delete("/queues/{queue}", s.handleQueueDelete)
		})
	})

	// Embedded dashboard. Auth happens in the browser (API key entered in
	// the UI); the static assets themselves are public.
	r.Handle("/ui", http.RedirectHandler("/ui/", http.StatusMovedPermanently))
	r.Handle("/ui/*", http.StripPrefix("/ui", web.Handler()))
	r.Get("/", func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "/ui/", http.StatusFound)
	})

	r.NotFound(func(w http.ResponseWriter, r *http.Request) {
		writeProblem(w, r, http.StatusNotFound, CodeNotFound, "no such endpoint")
	})
	r.MethodNotAllowed(func(w http.ResponseWriter, r *http.Request) {
		writeProblem(w, r, http.StatusMethodNotAllowed, CodeInvalidArgument, "method not allowed")
	})
	return r
}

// --- middleware ---

func (s *Server) recoverer(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer func() {
			if rec := recover(); rec != nil {
				s.Log.Error("panic serving request", "path", r.URL.Path, "panic", rec)
				writeProblem(w, r, http.StatusInternalServerError, CodeInternal, "internal error")
			}
		}()
		next.ServeHTTP(w, r)
	})
}

type statusWriter struct {
	http.ResponseWriter
	status int
}

func (w *statusWriter) WriteHeader(code int) {
	w.status = code
	w.ResponseWriter.WriteHeader(code)
}

func (s *Server) accessLog(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		sw := &statusWriter{ResponseWriter: w, status: 200}
		start := time.Now()
		next.ServeHTTP(sw, r)
		s.Log.Debug("http", "method", r.Method, "path", r.URL.Path, "status", sw.status,
			"dur_ms", time.Since(start).Milliseconds())
	})
}

func (s *Server) requireRole(min auth.Role) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			role := s.Auth.Authenticate(r)
			if role == auth.RoleNone {
				writeProblem(w, r, http.StatusUnauthorized, CodeUnauthenticated, "missing or invalid API key")
				return
			}
			if role < min {
				writeProblem(w, r, http.StatusForbidden, CodePermissionDenied, "this endpoint requires the admin role")
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}

// decodeJSON reads a size-limited JSON body.
func (s *Server) decodeJSON(w http.ResponseWriter, r *http.Request, dst any, maxBytes int64) error {
	r.Body = http.MaxBytesReader(w, r.Body, maxBytes)
	dec := json.NewDecoder(r.Body)
	if err := dec.Decode(dst); err != nil {
		var maxErr *http.MaxBytesError
		if errors.As(err, &maxErr) {
			return maxErr
		}
		return errf("invalid JSON body: %v", err)
	}
	return nil
}

// writeErr routes any handler error to the right problem response.
func (s *Server) writeErr(w http.ResponseWriter, r *http.Request, err error) {
	var br badRequest
	var maxErr *http.MaxBytesError
	switch {
	case errors.As(err, &br):
		writeProblem(w, r, http.StatusBadRequest, CodeInvalidArgument, br.msg)
	case errors.As(err, &maxErr):
		writeProblem(w, r, http.StatusRequestEntityTooLarge, CodePayloadTooLarge, "request body too large")
	case errors.Is(err, store.ErrNotFound), errors.Is(err, store.ErrAlreadyExists), errors.Is(err, store.ErrFailedPrecondition):
		writeStoreErr(w, r, err)
	default:
		s.Log.Error("internal error", "path", r.URL.Path, "err", err)
		writeProblem(w, r, http.StatusInternalServerError, CodeInternal, "internal error")
	}
}
