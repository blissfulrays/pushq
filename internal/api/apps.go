package api

import (
	"context"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/blissfulrays/pushq/internal/accounts"
	"github.com/blissfulrays/pushq/internal/auth"
	"github.com/blissfulrays/pushq/internal/store"
)

// Applications: every queue belongs to one. Queue requests act in the
// application named by the X-PushQ-App header (an application id), in the
// one an API key is restricted to, or else in the team's Default
// application.

// AppHeader picks the application a queue request acts in.
const AppHeader = "X-PushQ-App"

type appDTO struct {
	ID        int64     `json:"id"`
	Name      string    `json:"name"`
	Icon      string    `json:"icon,omitempty" doc:"A small image as a data: URL; absent when the application shows its initials."`
	Default   bool      `json:"default" doc:"The team's Default application; it can't be deleted."`
	CreatedAt time.Time `json:"created_at"`
}

type appsList struct {
	Apps []appDTO `json:"apps"`
}

type appCreateBody struct {
	Name string `json:"name" doc:"1-60 characters, unique within the team."`
	Icon string `json:"icon,omitempty" doc:"Optional base64 data: URL of a PNG, JPEG, WebP or GIF image, at most 64 KB."`
}

type appPatchBody struct {
	Name *string `json:"name,omitempty" doc:"1-60 characters, unique within the team."`
	Icon *string `json:"icon,omitempty" doc:"Base64 data: URL of a PNG, JPEG, WebP or GIF image (at most 64 KB); \"\" removes the icon."`
}

// appBodyLimit leaves room for an icon.
const appBodyLimit = accounts.MaxAppIconBytes + 4<<10

func toAppDTO(a accounts.App) appDTO {
	return appDTO{ID: a.ID, Name: a.Name, Icon: a.Icon, Default: a.Default, CreatedAt: a.CreatedAt}
}

// withApp resolves the application a queue request acts in (Principal.App).
func (s *Server) withApp(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		p := principal(r)
		if s.Accounts == nil || p.Kind == auth.KindNone || (s.Auth.SaaS() && p.Team == nil) {
			// No accounts database, or no credentials (rejected next), or the
			// operator's instance-wide keys in SaaS mode.
			next.ServeHTTP(w, r)
			return
		}
		ctx := r.Context()
		tid := teamID(p)
		hdr := strings.TrimSpace(r.Header.Get(AppHeader))
		var app accounts.App
		var err error
		switch {
		case p.KeyAppID > 0:
			if hdr != "" && hdr != strconv.FormatInt(p.KeyAppID, 10) {
				writeProblem(w, r, http.StatusForbidden, CodePermissionDenied, "this API key only has access to its own application")
				return
			}
			app, err = s.Accounts.GetApp(ctx, tid, p.KeyAppID)
		case hdr != "":
			id, perr := strconv.ParseInt(hdr, 10, 64)
			if perr != nil || id <= 0 {
				writeProblem(w, r, http.StatusBadRequest, CodeInvalidArgument, AppHeader+" must be an application id")
				return
			}
			app, err = s.Accounts.GetApp(ctx, tid, id)
		default:
			app, err = s.Accounts.DefaultApp(ctx, tid)
		}
		if err != nil {
			s.writeErr(w, r, err)
			return
		}
		p.App = &app
		next.ServeHTTP(w, r.WithContext(context.WithValue(ctx, principalKey{}, p)))
	})
}

// appsTeam is the team whose applications the caller manages; false (with a
// problem written) for the operator's keys in SaaS mode, which aren't in one.
func (s *Server) appsTeam(w http.ResponseWriter, r *http.Request) (int64, bool) {
	p := principal(r)
	if s.Auth.SaaS() && p.Team == nil {
		writeProblem(w, r, http.StatusBadRequest, CodeFailedPrecondition, "applications belong to a team; sign in to one first")
		return 0, false
	}
	return teamID(p), true
}

func appIDParam(r *http.Request) (int64, error) {
	id, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	if err != nil {
		return 0, errf("invalid application id")
	}
	return id, nil
}

func (s *Server) handleAppList(w http.ResponseWriter, r *http.Request) {
	tid, ok := s.appsTeam(w, r)
	if !ok {
		return
	}
	p := principal(r)
	out := appsList{Apps: []appDTO{}}
	if p.KeyAppID > 0 {
		a, err := s.Accounts.GetApp(r.Context(), tid, p.KeyAppID)
		if err != nil {
			s.writeErr(w, r, err)
			return
		}
		out.Apps = append(out.Apps, toAppDTO(a))
		writeJSON(w, http.StatusOK, out)
		return
	}
	apps, err := s.Accounts.ListApps(r.Context(), tid)
	if err != nil {
		s.writeErr(w, r, err)
		return
	}
	for _, a := range apps {
		out.Apps = append(out.Apps, toAppDTO(a))
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *Server) handleAppCreate(w http.ResponseWriter, r *http.Request) {
	tid, ok := s.appsTeam(w, r)
	if !ok {
		return
	}
	var body appCreateBody
	if err := s.decodeJSON(w, r, &body, appBodyLimit); err != nil {
		s.writeErr(w, r, err)
		return
	}
	a, err := s.Accounts.CreateApp(r.Context(), tid, body.Name, body.Icon, s.Now())
	if err != nil {
		s.writeErr(w, r, err)
		return
	}
	writeJSON(w, http.StatusCreated, toAppDTO(a))
}

func (s *Server) handleAppUpdate(w http.ResponseWriter, r *http.Request) {
	tid, ok := s.appsTeam(w, r)
	if !ok {
		return
	}
	id, err := appIDParam(r)
	if err != nil {
		s.writeErr(w, r, err)
		return
	}
	var body appPatchBody
	if err := s.decodeJSON(w, r, &body, appBodyLimit); err != nil {
		s.writeErr(w, r, err)
		return
	}
	a, err := s.Accounts.UpdateApp(r.Context(), tid, id, body.Name, body.Icon, s.Now())
	if err != nil {
		s.writeErr(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, toAppDTO(a))
}

// handleAppDelete deletes an application that has no queues left, along
// with the API keys restricted to it.
func (s *Server) handleAppDelete(w http.ResponseWriter, r *http.Request) {
	tid, ok := s.appsTeam(w, r)
	if !ok {
		return
	}
	id, err := appIDParam(r)
	if err != nil {
		s.writeErr(w, r, err)
		return
	}
	a, err := s.Accounts.GetApp(r.Context(), tid, id)
	if err != nil {
		s.writeErr(w, r, err)
		return
	}
	if a.Default {
		s.writeErr(w, r, accounts.ErrDefaultApp)
		return
	}
	qs, _, err := s.Store.ListQueues(r.Context(), store.ListQueuesOpts{NamePrefix: a.NS + ":", PageSize: 1})
	if err != nil {
		s.writeErr(w, r, err)
		return
	}
	if len(qs) > 0 {
		writeProblem(w, r, http.StatusBadRequest, CodeFailedPrecondition, "delete this application's queues first")
		return
	}
	if err := s.Accounts.DeleteApp(r.Context(), tid, id); err != nil {
		s.writeErr(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
