// Package api implements the pushq REST API (see api/openapi.yaml).
package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/blissfulrays/pushq/internal/accounts"
	"github.com/blissfulrays/pushq/internal/auth"
	"github.com/blissfulrays/pushq/internal/config"
	"github.com/blissfulrays/pushq/internal/store"
	"github.com/blissfulrays/pushq/internal/workos"
	"github.com/blissfulrays/pushq/web"
)

type Server struct {
	Store    store.Store
	Auth     *auth.Auth
	Accounts *accounts.Store // nil disables dashboard accounts

	// WorkOS switches on SaaS mode together with Auth.EnableSaaS: WorkOS
	// sign-in only, teams as WorkOS Organizations, team-isolated data.
	WorkOS            *workos.Client
	WorkOSRedirectURI string // "" = derive from the request
	Log               *slog.Logger
	Limits            config.Limits
	MetricsPublic     bool
	MetricsHandler    http.Handler
	Now               func() time.Time
	Role              func() string // "leader" | "follower" for readyz

	loginLimiter limiterMap
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
			r.With(s.authenticate, s.require(auth.PermList), s.operatorOnlyInSaaS).Handle("/metrics", s.MetricsHandler)
		}
	}

	r.Route("/v1", func(r chi.Router) {
		r.Use(s.authenticate)

		// Sign-in flow; no credentials needed.
		r.Get("/auth/session", s.handleSession)
		r.Post("/auth/setup", s.handleSetup)
		r.Post("/auth/login", s.handleLogin)
		r.Post("/auth/logout", s.handleLogout)
		r.Get("/auth/invites/{token}", s.handleInviteGet)
		r.Get("/auth/workos/login", s.handleWorkOSLogin)
		r.Get("/auth/workos/callback", s.handleWorkOSCallback)
		r.Post("/auth/invites/{token}:accept", s.handleInviteAccept)

		read := r.With(s.require(auth.PermRead))
		write := r.With(s.require(auth.PermWrite))

		read.Get("/version", s.handleVersion)

		write.Post("/queues", s.handleQueueCreate)
		write.Put("/queues/{queue}", s.handleQueuePut)
		read.Get("/queues/{queue}", s.handleQueueGet)
		write.Patch("/queues/{queue}", s.handleQueuePatch)
		write.Post("/queues/{queue}:pause", s.handleQueuePause)
		write.Post("/queues/{queue}:resume", s.handleQueueResume)
		write.Post("/queues/{queue}:purge", s.handleQueuePurge)
		read.Get("/queues/{queue}/stats", s.handleQueueStats)

		write.Post("/queues/{queue}/tasks", s.handleTaskCreate)
		write.Post("/queues/{queue}/tasks:batch", s.handleTaskBatch)
		read.Get("/queues/{queue}/tasks", s.handleTaskList)
		read.Get("/queues/{queue}/tasks/{taskID}", s.handleTaskGet)
		write.Delete("/queues/{queue}/tasks/{taskID}", s.handleTaskDelete)
		write.Post("/queues/{queue}/tasks/{taskID}:run", s.handleTaskRun)
		read.Get("/queues/{queue}/tasks/{taskID}/attempts", s.handleAttemptsList)

		r.With(s.require(auth.PermList)).Get("/queues", s.handleQueueList)
		r.With(s.require(auth.PermDeleteQueue)).Delete("/queues/{queue}", s.handleQueueDelete)

		// The signed-in user's own account.
		r.Group(func(r chi.Router) {
			r.Use(s.requireUser)
			r.Patch("/me", s.handleMePatch)
			r.Post("/me/password", s.handleMePassword)

			// SaaS mode: the user's teams (WorkOS Organizations).
			r.Get("/teams", s.handleTeamsList)
			r.Post("/teams", s.handleTeamCreate)
			r.Post("/teams/{id}:switch", s.handleTeamSwitch)
		})
		r.With(s.require(auth.PermManage), s.requireAccounts).Patch("/teams/current", s.handleTeamRename)

		// Team and API keys (admins).
		r.Group(func(r chi.Router) {
			r.Use(s.require(auth.PermManage), s.requireAccounts)
			r.Get("/users", s.handleUserList)
			r.Patch("/users/{id}", s.handleUserPatch)
			r.Delete("/users/{id}", s.handleUserDelete)
			r.Post("/users/{id}/reset-link", s.handleUserResetLink)
			r.Get("/invites", s.handleInviteList)
			r.Post("/invites", s.handleInviteCreate)
			r.Delete("/invites/{id}", s.handleInviteDelete)
			r.Get("/api-keys", s.handleKeyList)
			r.Post("/api-keys", s.handleKeyCreate)
			r.Delete("/api-keys/{id}", s.handleKeyDelete)
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
		msg := "no such endpoint"
		if rest, ok := strings.CutPrefix(r.URL.Path, "/v1/queues/"); ok && strings.Contains(rest, "/") {
			msg += "; if the queue name contains '/', URL-encode it in the path (user/instagram -> user%2Finstagram)"
		}
		writeProblem(w, r, http.StatusNotFound, CodeNotFound, msg)
	})
	r.MethodNotAllowed(func(w http.ResponseWriter, r *http.Request) {
		writeProblem(w, r, http.StatusMethodNotAllowed, CodeInvalidArgument, "method not allowed")
	})
	return r
}

// pathParam returns a decoded URL path parameter. chi matches routes against
// r.URL.RawPath when the request used percent-encoding (which is what lets a
// queue name like "user/instagram" travel as one segment, user%2Finstagram),
// and in that case the captured value is still encoded.
func pathParam(r *http.Request, key string) string {
	v := chi.URLParam(r, key)
	if r.URL.RawPath == "" {
		return v
	}
	if dec, err := url.PathUnescape(v); err == nil {
		return dec
	}
	return v
}

func queueParam(r *http.Request) string { return pathParam(r, "queue") }
func taskParam(r *http.Request) string  { return pathParam(r, "taskID") }

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

type principalKey struct{}

func principal(r *http.Request) auth.Principal {
	p, _ := r.Context().Value(principalKey{}).(auth.Principal)
	return p
}

// csrfHeader must accompany state-changing requests that rely on the session
// cookie. Browsers only let same-origin scripts set custom headers (pushq
// serves no CORS), so a forged cross-site form or fetch can't. Bearer-key
// requests are unaffected.
const csrfHeader = "X-Requested-With"

func unsafeMethod(m string) bool {
	return m != http.MethodGet && m != http.MethodHead && m != http.MethodOptions
}

// authenticate resolves the caller (without rejecting anyone) and enforces
// the CSRF rule for cookie-based requests.
func (s *Server) authenticate(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if unsafeMethod(r.Method) && r.Header.Get("Authorization") == "" && r.Header.Get(csrfHeader) == "" {
			_, cookieErr := r.Cookie(auth.SessionCookie)
			if cookieErr == nil || strings.HasPrefix(r.URL.Path, "/v1/auth/") {
				writeProblem(w, r, http.StatusForbidden, CodePermissionDenied,
					"browser requests must send the "+csrfHeader+" header")
				return
			}
		}
		p, renewed, err := s.Auth.Authenticate(r)
		if err != nil {
			s.writeErr(w, r, err)
			return
		}
		if renewed != nil {
			setSessionCookie(w, r, p.SessionToken, *renewed)
		}
		next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), principalKey{}, p)))
	})
}

// require rejects callers lacking the given permission.
func (s *Server) require(perm auth.Perm) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			p := principal(r)
			if p.Kind == auth.KindNone {
				writeProblem(w, r, http.StatusUnauthorized, CodeUnauthenticated, "sign in or send a valid API key")
				return
			}
			if !p.Can(perm) {
				writeProblem(w, r, http.StatusForbidden, CodePermissionDenied,
					fmt.Sprintf("the %s role is not allowed to do this", p.Role))
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}

// operatorOnlyInSaaS keeps instance-wide endpoints (/metrics names every
// team's queues) to the operator's config-file keys in SaaS mode.
func (s *Server) operatorOnlyInSaaS(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if s.Auth.SaaS() {
			if p := principal(r); p.Kind != auth.KindKey || p.Team != nil {
				writeProblem(w, r, http.StatusForbidden, CodePermissionDenied, "only the operator's API keys can read instance metrics")
				return
			}
		}
		next.ServeHTTP(w, r)
	})
}

// requireUser rejects callers that aren't a signed-in dashboard user.
func (s *Server) requireUser(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if principal(r).Kind != auth.KindUser {
			writeProblem(w, r, http.StatusUnauthorized, CodeUnauthenticated, "sign in to the dashboard to manage your account")
			return
		}
		next.ServeHTTP(w, r)
	})
}

func (s *Server) requireAccounts(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if s.Accounts == nil {
			writeProblem(w, r, http.StatusNotFound, CodeNotFound, "dashboard accounts are not enabled")
			return
		}
		next.ServeHTTP(w, r)
	})
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
	var inv *accounts.InvalidError
	var maxErr *http.MaxBytesError
	switch {
	case errors.As(err, &br):
		writeProblem(w, r, http.StatusBadRequest, CodeInvalidArgument, br.msg)
	case errors.As(err, &inv):
		writeProblem(w, r, http.StatusBadRequest, CodeInvalidArgument, inv.Msg)
	case errors.Is(err, accounts.ErrBadCredentials):
		writeProblem(w, r, http.StatusUnauthorized, CodeUnauthenticated, err.Error())
	case errors.As(err, &maxErr):
		writeProblem(w, r, http.StatusRequestEntityTooLarge, CodePayloadTooLarge, "request body too large")
	case errors.Is(err, store.ErrNotFound), errors.Is(err, store.ErrAlreadyExists), errors.Is(err, store.ErrFailedPrecondition):
		writeStoreErr(w, r, err)
	default:
		s.Log.Error("internal error", "path", r.URL.Path, "err", err)
		writeProblem(w, r, http.StatusInternalServerError, CodeInternal, "internal error")
	}
}
