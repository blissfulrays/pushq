package api

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/blissfulrays/pushq/internal/accounts"
	"github.com/blissfulrays/pushq/internal/workos"
)

// SaaS mode sign-in: WorkOS AuthKit is the only way in (it also handles
// sign-up). Enabled when WORKOS_CLIENT_ID and WORKOS_API_KEY are set.

const workosStateCookie = "pushq_workos_state"

// workosRedirectURI is where WorkOS sends the browser back. It must be listed
// under Redirects in the WorkOS dashboard.
func (s *Server) workosRedirectURI(r *http.Request) string {
	if s.WorkOSRedirectURI != "" {
		return s.WorkOSRedirectURI
	}
	scheme := "http"
	if secureRequest(r) {
		scheme = "https"
	}
	return scheme + "://" + r.Host + "/v1/auth/workos/callback"
}

// safeNext keeps post-login redirects inside the dashboard (no open redirects).
func safeNext(next string) string {
	if strings.HasPrefix(next, "/ui/") && !strings.HasPrefix(next, "//") && !strings.Contains(next, "\\") {
		return next
	}
	return "/ui/"
}

// loginError sends the browser back to the sign-in page with a message.
func loginError(w http.ResponseWriter, r *http.Request, msg string) {
	http.Redirect(w, r, "/ui/login?error="+url.QueryEscape(msg), http.StatusFound)
}

func (s *Server) handleWorkOSLogin(w http.ResponseWriter, r *http.Request) {
	if !s.saas() {
		writeProblem(w, r, http.StatusNotFound, CodeNotFound, "WorkOS sign-in is not configured")
		return
	}
	b := make([]byte, 24)
	if _, err := rand.Read(b); err != nil {
		s.writeErr(w, r, err)
		return
	}
	state := base64.RawURLEncoding.EncodeToString(b)
	next := safeNext(r.URL.Query().Get("next"))
	// SameSite=Lax (not Strict): the callback is a top-level navigation coming
	// from WorkOS, and a Strict cookie wouldn't be sent on it.
	http.SetCookie(w, &http.Cookie{
		Name: workosStateCookie, Value: state + "|" + next, Path: "/v1/auth/workos",
		MaxAge: int((10 * time.Minute).Seconds()), HttpOnly: true, Secure: secureRequest(r),
		SameSite: http.SameSiteLaxMode,
	})
	http.Redirect(w, r, s.WorkOS.AuthorizeURL(s.workosRedirectURI(r), state), http.StatusFound)
}

func (s *Server) handleWorkOSCallback(w http.ResponseWriter, r *http.Request) {
	if !s.saas() {
		writeProblem(w, r, http.StatusNotFound, CodeNotFound, "WorkOS sign-in is not configured")
		return
	}
	q := r.URL.Query()

	// The state must match the cookie set when this sign-in started, or the
	// callback could be forged to sign someone into another person's account.
	c, err := r.Cookie(workosStateCookie)
	http.SetCookie(w, &http.Cookie{Name: workosStateCookie, Value: "", Path: "/v1/auth/workos", MaxAge: -1,
		HttpOnly: true, Secure: secureRequest(r), SameSite: http.SameSiteLaxMode})
	if err != nil {
		loginError(w, r, "Your sign-in session expired. Please try again.")
		return
	}
	wantState, next, _ := strings.Cut(c.Value, "|")
	if got := q.Get("state"); got == "" || subtle.ConstantTimeCompare([]byte(got), []byte(wantState)) != 1 {
		loginError(w, r, "Sign-in could not be verified. Please try again.")
		return
	}
	if e := q.Get("error"); e != "" {
		msg := q.Get("error_description")
		if msg == "" {
			msg = e
		}
		loginError(w, r, "WorkOS: "+msg)
		return
	}
	code := q.Get("code")
	if code == "" {
		loginError(w, r, "WorkOS didn't return a sign-in code.")
		return
	}

	res, err := s.WorkOS.Authenticate(r.Context(), code)
	if err != nil {
		s.Log.Warn("workos authenticate failed", "err", err)
		var we *workos.Error
		if errors.As(err, &we) && we.Status < 500 {
			loginError(w, r, "WorkOS rejected the sign-in ("+we.Code+"). Please try again.")
			return
		}
		loginError(w, r, "Couldn't reach WorkOS. Please try again.")
		return
	}
	wu := res.User

	u, err := s.Accounts.UpsertWorkOSUser(r.Context(), accounts.ExternalIdentity{
		WorkOSUserID: wu.ID, Email: wu.Email, EmailVerified: wu.EmailVerified, Name: wu.Name(),
	}, s.Now())
	if err != nil {
		var inv *accounts.InvalidError
		switch {
		case errors.As(err, &inv):
			loginError(w, r, inv.Msg)
		case errors.Is(err, accounts.ErrIdentityConflict):
			loginError(w, r, "This email is already linked to a different WorkOS account.")
		default:
			s.Log.Error("workos sign-in", "err", err)
			loginError(w, r, "Sign-in failed. Please try again.")
		}
		return
	}

	// Mirror their WorkOS organizations as teams and pick one to start in.
	team, err := s.syncUserTeams(r.Context(), u.ID, wu.ID, res.OrganizationID)
	if err != nil {
		s.Log.Error("sync teams from workos", "user", u.Email, "err", err)
		loginError(w, r, "Couldn't load your teams from WorkOS. Please try again.")
		return
	}

	token, expires, err := s.Accounts.CreateTeamSession(r.Context(), u.ID, team, s.Now())
	if err != nil {
		s.writeErr(w, r, err)
		return
	}
	if team > 0 {
		if err := s.Accounts.SetSessionTeam(r.Context(), token, u.ID, team); err != nil {
			s.writeErr(w, r, err)
			return
		}
	}
	setSessionCookie(w, r, token, expires)
	http.Redirect(w, r, safeNext(next), http.StatusFound)
}
