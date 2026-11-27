package api

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/blissfulrays/pushq/internal/accounts"
	"github.com/blissfulrays/pushq/internal/workos"
)

// SaaS mode sign-in, on pushq's own sign-in page (no WorkOS-hosted pages):
//
//   - Email code: POST /v1/auth/email-code sends a 6-digit code, and
//     POST /v1/auth/email-code:verify signs in with it. New emails get an
//     account, so this is sign-up too.
//   - GitHub: /v1/auth/workos/login?provider=github redirects to GitHub's
//     consent screen; WorkOS returns to /v1/auth/workos/callback.
//
// Enabled when WORKOS_CLIENT_ID and WORKOS_API_KEY are set.

const (
	workosStateCookie = "pushq_workos_state"
	// verifyCookie holds a sign-in WorkOS paused for email verification.
	verifyCookie = "pushq_email_verify"
)

type pendingVerify struct {
	Token string `json:"t"`
	Email string `json:"e"`
	Next  string `json:"n"`
}

// pauseForVerification remembers a sign-in that WorkOS paused until the
// person enters the verification code it just emailed them.
func pauseForVerification(w http.ResponseWriter, r *http.Request, token, email, next string) {
	b, _ := json.Marshal(pendingVerify{Token: token, Email: email, Next: safeNext(next)})
	http.SetCookie(w, &http.Cookie{
		Name: verifyCookie, Value: base64.RawURLEncoding.EncodeToString(b), Path: "/v1/auth",
		MaxAge: int((15 * time.Minute).Seconds()), HttpOnly: true, Secure: secureRequest(r),
		SameSite: http.SameSiteLaxMode,
	})
}

func readPendingVerify(r *http.Request) (pendingVerify, bool) {
	c, err := r.Cookie(verifyCookie)
	if err != nil {
		return pendingVerify{}, false
	}
	raw, err := base64.RawURLEncoding.DecodeString(c.Value)
	var p pendingVerify
	if err != nil || json.Unmarshal(raw, &p) != nil || p.Token == "" {
		return pendingVerify{}, false
	}
	return p, true
}

func clearPendingVerify(w http.ResponseWriter, r *http.Request) {
	http.SetCookie(w, &http.Cookie{Name: verifyCookie, Value: "", Path: "/v1/auth", MaxAge: -1,
		HttpOnly: true, Secure: secureRequest(r), SameSite: http.SameSiteLaxMode})
}

// oauthProviders maps the sign-in page's provider names to WorkOS's.
var oauthProviders = map[string]string{"github": workos.ProviderGitHub}

// workosRedirectURI is where WorkOS sends the browser back after GitHub. It
// must be listed under Redirects in the WorkOS dashboard.
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

// safeNext keeps post-login redirects on this site (no open redirects):
// a path, not "//host" or a backslash trick, and never into the API.
func safeNext(next string) string {
	if strings.HasPrefix(next, "/") && !strings.HasPrefix(next, "//") && !strings.Contains(next, "\\") &&
		!strings.HasPrefix(next, "/v1/") {
		return next
	}
	return "/"
}

// loginError sends the browser back to the sign-in page with a message.
func loginError(w http.ResponseWriter, r *http.Request, msg string) {
	http.Redirect(w, r, "/login?error="+url.QueryEscape(msg), http.StatusFound)
}

func clientIP(r *http.Request) string {
	ip, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return ip
}

// signInError is a failure the person signing in can act on.
type signInError struct{ msg string }

func (e signInError) Error() string { return e.msg }

// completeSignIn turns a WorkOS sign-in into a pushq session: find or create
// the user, mirror their WorkOS organizations as teams, and start the
// session in the team they last used.
func (s *Server) completeSignIn(ctx context.Context, w http.ResponseWriter, r *http.Request, res workos.AuthResult) (accounts.User, error) {
	wu := res.User
	u, err := s.Accounts.UpsertWorkOSUser(ctx, accounts.ExternalIdentity{
		WorkOSUserID: wu.ID, Email: wu.Email, EmailVerified: wu.EmailVerified, Name: wu.Name(),
	}, s.Now())
	if err != nil {
		var inv *accounts.InvalidError
		switch {
		case errors.As(err, &inv):
			return u, signInError{inv.Msg}
		case errors.Is(err, accounts.ErrIdentityConflict):
			return u, signInError{"This email is already linked to a different account."}
		}
		return u, err
	}
	team, err := s.syncUserTeams(ctx, u.ID, wu.ID, res.OrganizationID)
	if err != nil {
		s.Log.Error("sync teams from workos", "user", u.Email, "err", err)
		return u, signInError{"Couldn't load your teams. Please try again."}
	}
	token, expires, err := s.Accounts.CreateTeamSession(ctx, u.ID, team, s.Now())
	if err != nil {
		return u, err
	}
	if team > 0 {
		if err := s.Accounts.SetSessionTeam(ctx, token, u.ID, team); err != nil {
			return u, err
		}
	}
	setSessionCookie(w, r, token, expires)
	return u, nil
}

// --- GitHub (OAuth via WorkOS) ---

func (s *Server) handleWorkOSLogin(w http.ResponseWriter, r *http.Request) {
	if !s.saas() {
		writeProblem(w, r, http.StatusNotFound, CodeNotFound, "WorkOS sign-in is not configured")
		return
	}
	provider, ok := oauthProviders[r.URL.Query().Get("provider")]
	if !ok {
		loginError(w, r, "Unknown sign-in option.")
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
	// from GitHub/WorkOS, and a Strict cookie wouldn't be sent on it.
	http.SetCookie(w, &http.Cookie{
		Name: workosStateCookie, Value: state + "|" + next, Path: "/v1/auth/workos",
		MaxAge: int((10 * time.Minute).Seconds()), HttpOnly: true, Secure: secureRequest(r),
		SameSite: http.SameSiteLaxMode,
	})
	http.Redirect(w, r, s.WorkOS.AuthorizeURL(s.workosRedirectURI(r), state, provider), http.StatusFound)
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
		loginError(w, r, "Sign-in was cancelled or failed: "+msg)
		return
	}
	code := q.Get("code")
	if code == "" {
		loginError(w, r, "No sign-in code came back. Please try again.")
		return
	}

	res, err := s.WorkOS.Authenticate(r.Context(), code)
	if token, email, ok := workos.PendingEmailVerification(err); ok {
		// WorkOS just emailed a verification code; the sign-in page asks for it.
		pauseForVerification(w, r, token, email, next)
		http.Redirect(w, r, "/login?verify=1", http.StatusFound)
		return
	}
	if err != nil {
		s.Log.Warn("workos authenticate failed", "err", err)
		var we *workos.Error
		if errors.As(err, &we) && we.Status < 500 {
			loginError(w, r, "Sign-in was rejected ("+we.Code+"). Please try again.")
			return
		}
		loginError(w, r, "Sign-in is unavailable right now. Please try again.")
		return
	}
	// GitHub sign-ins skip email verification: pushq accepts the email GitHub
	// provides even when WorkOS hasn't marked it verified (WorkOS's own
	// "require email verification" setting decides whether to pause first).
	res.User.EmailVerified = true
	if _, err := s.completeSignIn(r.Context(), w, r, res); err != nil {
		var se signInError
		if errors.As(err, &se) {
			loginError(w, r, se.msg)
			return
		}
		s.Log.Error("workos sign-in", "err", err)
		loginError(w, r, "Sign-in failed. Please try again.")
		return
	}
	http.Redirect(w, r, safeNext(next), http.StatusFound)
}

// --- email code (passwordless) ---

type codeSent struct {
	Sent bool `json:"sent"`
}

type verifyPending struct {
	Pending bool   `json:"pending" doc:"A sign-in is waiting for an email verification code."`
	Email   string `json:"email" doc:"Where the verification code was sent."`
}

type verifiedSignIn struct {
	User userDTO `json:"user"`
	Next string  `json:"next,omitempty" doc:"Where the dashboard should go next."`
}

type emailCodeBody struct {
	Email string `json:"email"`
	Code  string `json:"code"`
}

// handleEmailCode emails a 6-digit sign-in code. Unknown emails get a WorkOS
// user first, so the same flow signs people up. The response never says
// whether the email already had an account.
func (s *Server) handleEmailCode(w http.ResponseWriter, r *http.Request) {
	if !s.saas() {
		writeProblem(w, r, http.StatusNotFound, CodeNotFound, "email sign-in is not configured")
		return
	}
	if s.rateLimited(w, r) {
		return
	}
	var body emailCodeBody
	if err := s.decodeJSON(w, r, &body, 4<<10); err != nil {
		s.writeErr(w, r, err)
		return
	}
	email := strings.ToLower(strings.TrimSpace(body.Email))
	if !strings.Contains(email, "@") || len(email) > 254 || strings.ContainsAny(email, " \t\r\n") {
		s.writeErr(w, r, errf("enter a valid email address"))
		return
	}
	ctx := r.Context()
	if _, found, err := s.WorkOS.FindUserByEmail(ctx, email); err != nil {
		s.workosErr(w, r, "send a sign-in code", err)
		return
	} else if !found {
		if _, err := s.WorkOS.CreateUser(ctx, email); err != nil {
			s.workosErr(w, r, "send a sign-in code", err)
			return
		}
	}
	if err := s.WorkOS.SendMagicCode(ctx, email, clientIP(r), r.UserAgent()); err != nil {
		s.workosErr(w, r, "send a sign-in code", err)
		return
	}
	writeJSON(w, http.StatusOK, codeSent{Sent: true})
}

// handleEmailCodeVerify signs in with the emailed code and starts a session.
func (s *Server) handleEmailCodeVerify(w http.ResponseWriter, r *http.Request) {
	if !s.saas() {
		writeProblem(w, r, http.StatusNotFound, CodeNotFound, "email sign-in is not configured")
		return
	}
	if s.rateLimited(w, r) {
		return
	}
	var body emailCodeBody
	if err := s.decodeJSON(w, r, &body, 4<<10); err != nil {
		s.writeErr(w, r, err)
		return
	}
	email := strings.ToLower(strings.TrimSpace(body.Email))
	code := strings.TrimSpace(body.Code)
	if email == "" || code == "" {
		s.writeErr(w, r, errf("enter your email and the code we sent"))
		return
	}
	ctx := r.Context()
	res, err := s.WorkOS.AuthenticateMagicCode(ctx, email, code, clientIP(r), r.UserAgent())
	if token, vEmail, ok := workos.PendingEmailVerification(err); ok {
		pauseForVerification(w, r, token, firstNonEmpty(vEmail, email), "/")
		writeProblem(w, r, http.StatusForbidden, CodeEmailVerificationRequired, "We emailed you a verification code. Enter it to finish signing in.")
		return
	}
	if err != nil {
		var we *workos.Error
		if errors.As(err, &we) && we.Status < 500 {
			writeProblem(w, r, http.StatusUnauthorized, CodeUnauthenticated, "That code is wrong or has expired. Check the latest email, or send a new code.")
			return
		}
		s.workosErr(w, r, "verify the code", err)
		return
	}
	// Typing the code proves the person owns the inbox.
	if !res.User.EmailVerified {
		if err := s.WorkOS.MarkEmailVerified(ctx, res.User.ID); err != nil {
			s.Log.Warn("mark workos email verified", "err", err)
		}
		res.User.EmailVerified = true
	}
	u, err := s.completeSignIn(ctx, w, r, res)
	if err != nil {
		var se signInError
		if errors.As(err, &se) {
			writeProblem(w, r, http.StatusBadRequest, CodeFailedPrecondition, se.msg)
			return
		}
		s.writeErr(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, userEnvelope{User: toUserDTO(u)})
}

// --- finishing a sign-in paused for email verification ---

// handleVerifyPending tells the sign-in page whether a sign-in is waiting for
// an email verification code, and for which address.
func (s *Server) handleVerifyPending(w http.ResponseWriter, r *http.Request) {
	p, ok := readPendingVerify(r)
	writeJSON(w, http.StatusOK, verifyPending{Pending: ok, Email: p.Email})
}

func (s *Server) handleVerifyEmail(w http.ResponseWriter, r *http.Request) {
	if !s.saas() {
		writeProblem(w, r, http.StatusNotFound, CodeNotFound, "email verification is not configured")
		return
	}
	if s.rateLimited(w, r) {
		return
	}
	p, ok := readPendingVerify(r)
	if !ok {
		writeProblem(w, r, http.StatusBadRequest, CodeFailedPrecondition, "This verification has expired. Please sign in again.")
		return
	}
	var body emailCodeBody
	if err := s.decodeJSON(w, r, &body, 4<<10); err != nil {
		s.writeErr(w, r, err)
		return
	}
	res, err := s.WorkOS.AuthenticateEmailVerification(r.Context(), strings.TrimSpace(body.Code), p.Token)
	if err != nil {
		var we *workos.Error
		if errors.As(err, &we) && we.Status < 500 {
			writeProblem(w, r, http.StatusUnauthorized, CodeUnauthenticated, "That code is wrong or has expired. Check the latest email from us.")
			return
		}
		s.workosErr(w, r, "verify your email", err)
		return
	}
	res.User.EmailVerified = true // that's what the code just proved
	u, err := s.completeSignIn(r.Context(), w, r, res)
	if err != nil {
		var se signInError
		if errors.As(err, &se) {
			writeProblem(w, r, http.StatusBadRequest, CodeFailedPrecondition, se.msg)
			return
		}
		s.writeErr(w, r, err)
		return
	}
	clearPendingVerify(w, r)
	writeJSON(w, http.StatusOK, verifiedSignIn{User: toUserDTO(u), Next: p.Next})
}

func firstNonEmpty(v ...string) string {
	for _, s := range v {
		if s != "" {
			return s
		}
	}
	return ""
}
