// Package workos implements the WorkOS calls pushq's SaaS mode needs: AuthKit
// sign-in (authorization URL, code exchange) and Organizations as teams
// (organizations, memberships, invitations). Plain REST, no SDK.
package workos

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

const DefaultBaseURL = "https://api.workos.com"

type Client struct {
	ClientID string
	APIKey   string
	BaseURL  string // default https://api.workos.com
	HTTP     *http.Client
}

// User is the subset of the WorkOS user pushq uses.
type User struct {
	ID            string `json:"id"`
	Email         string `json:"email"`
	EmailVerified bool   `json:"email_verified"`
	FirstName     string `json:"first_name"`
	LastName      string `json:"last_name"`
}

// Name returns "First Last", trimmed.
func (u User) Name() string {
	return strings.TrimSpace(u.FirstName + " " + u.LastName)
}

func (c *Client) base() string {
	if c.BaseURL != "" {
		return strings.TrimRight(c.BaseURL, "/")
	}
	return DefaultBaseURL
}

// AuthorizeURL is where the browser goes to sign in with AuthKit. WorkOS
// redirects back to redirectURI with ?code=…&state=….
func (c *Client) AuthorizeURL(redirectURI, state string) string {
	q := url.Values{
		"client_id":     {c.ClientID},
		"redirect_uri":  {redirectURI},
		"response_type": {"code"},
		"provider":      {"authkit"},
		"state":         {state},
	}
	return c.base() + "/user_management/authorize?" + q.Encode()
}

// Error is a WorkOS API error.
type Error struct {
	Status      int
	Code        string
	Description string

	pendingToken string   // organization_selection_required
	orgIDs       []string // organization_selection_required
}

// NotFound reports a WorkOS 404 (e.g. a membership already removed there).
func IsNotFound(err error) bool {
	var e *Error
	return errors.As(err, &e) && e.Status == http.StatusNotFound
}

func (e *Error) Error() string {
	return fmt.Sprintf("workos: %s (%d): %s", e.Code, e.Status, e.Description)
}

// AuthResult is a completed sign-in.
type AuthResult struct {
	User User
	// OrganizationID is the organization the session was issued for, if any.
	OrganizationID string
}

// Authenticate exchanges an authorization code for the signed-in user. When
// WorkOS needs an organization picked first (the user belongs to several),
// it completes the sign-in for the first one; pushq switches teams itself.
func (c *Client) Authenticate(ctx context.Context, code string) (AuthResult, error) {
	res, err := c.authenticate(ctx, map[string]string{"grant_type": "authorization_code", "code": code})
	var se *Error
	if errors.As(err, &se) && se.Code == "organization_selection_required" && se.pendingToken != "" && len(se.orgIDs) > 0 {
		return c.authenticate(ctx, map[string]string{
			"grant_type":                   "urn:workos:oauth:grant-type:organization-selection",
			"pending_authentication_token": se.pendingToken,
			"organization_id":              se.orgIDs[0],
		})
	}
	return res, err
}

func (c *Client) authenticate(ctx context.Context, grant map[string]string) (AuthResult, error) {
	body := map[string]string{"client_id": c.ClientID, "client_secret": c.APIKey}
	for k, v := range grant {
		body[k] = v
	}
	var out struct {
		User           User   `json:"user"`
		OrganizationID string `json:"organization_id"`
	}
	if err := c.call(ctx, http.MethodPost, "/user_management/authenticate", body, &out, false); err != nil {
		return AuthResult{}, err
	}
	if out.User.ID == "" || out.User.Email == "" {
		return AuthResult{}, fmt.Errorf("workos: authenticate response has no user")
	}
	return AuthResult{User: out.User, OrganizationID: out.OrganizationID}, nil
}

// --- organizations (pushq teams) ---

type Organization struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

func (c *Client) CreateOrganization(ctx context.Context, name string) (Organization, error) {
	var o Organization
	err := c.call(ctx, http.MethodPost, "/organizations", map[string]any{"name": name}, &o, true)
	return o, err
}

func (c *Client) GetOrganization(ctx context.Context, id string) (Organization, error) {
	var o Organization
	err := c.call(ctx, http.MethodGet, "/organizations/"+url.PathEscape(id), nil, &o, true)
	return o, err
}

func (c *Client) UpdateOrganization(ctx context.Context, id, name string) (Organization, error) {
	var o Organization
	err := c.call(ctx, http.MethodPut, "/organizations/"+url.PathEscape(id), map[string]any{"name": name}, &o, true)
	return o, err
}

func (c *Client) GetUser(ctx context.Context, id string) (User, error) {
	var u User
	err := c.call(ctx, http.MethodGet, "/user_management/users/"+url.PathEscape(id), nil, &u, true)
	return u, err
}

// --- memberships ---

type Membership struct {
	ID             string `json:"id"`
	UserID         string `json:"user_id"`
	OrganizationID string `json:"organization_id"`
	Status         string `json:"status"` // active | pending | inactive
	Role           struct {
		Slug string `json:"slug"`
	} `json:"role"`
}

// ListMemberships lists active memberships for a user and/or organization.
func (c *Client) ListMemberships(ctx context.Context, userID, orgID string) ([]Membership, error) {
	var all []Membership
	after := ""
	for page := 0; page < 50; page++ {
		q := url.Values{"limit": {"100"}, "statuses": {"active"}}
		if userID != "" {
			q.Set("user_id", userID)
		}
		if orgID != "" {
			q.Set("organization_id", orgID)
		}
		if after != "" {
			q.Set("after", after)
		}
		var out struct {
			Data         []Membership `json:"data"`
			ListMetadata struct {
				After string `json:"after"`
			} `json:"list_metadata"`
		}
		if err := c.call(ctx, http.MethodGet, "/user_management/organization_memberships?"+q.Encode(), nil, &out, true); err != nil {
			return nil, err
		}
		for _, m := range out.Data {
			if m.Status == "" || m.Status == "active" {
				all = append(all, m)
			}
		}
		if out.ListMetadata.After == "" {
			break
		}
		after = out.ListMetadata.After
	}
	return all, nil
}

func (c *Client) CreateMembership(ctx context.Context, userID, orgID, roleSlug string) (Membership, error) {
	var m Membership
	body := map[string]any{"user_id": userID, "organization_id": orgID}
	if roleSlug != "" {
		body["role_slug"] = roleSlug
	}
	err := c.call(ctx, http.MethodPost, "/user_management/organization_memberships", body, &m, true)
	return m, err
}

func (c *Client) UpdateMembershipRole(ctx context.Context, membershipID, roleSlug string) error {
	return c.call(ctx, http.MethodPut, "/user_management/organization_memberships/"+url.PathEscape(membershipID),
		map[string]any{"role_slug": roleSlug}, nil, true)
}

func (c *Client) DeleteMembership(ctx context.Context, membershipID string) error {
	return c.call(ctx, http.MethodDelete, "/user_management/organization_memberships/"+url.PathEscape(membershipID), nil, nil, true)
}

// --- invitations (WorkOS sends the email) ---

type Invitation struct {
	ID             string    `json:"id"`
	Email          string    `json:"email"`
	State          string    `json:"state"` // pending | accepted | expired | revoked
	OrganizationID string    `json:"organization_id"`
	ExpiresAt      time.Time `json:"expires_at"`
	CreatedAt      time.Time `json:"created_at"`
}

func (c *Client) SendInvitation(ctx context.Context, email, orgID, inviterUserID, roleSlug string) (Invitation, error) {
	body := map[string]any{"email": email, "organization_id": orgID}
	if roleSlug != "" {
		body["role_slug"] = roleSlug
	}
	if inviterUserID != "" {
		body["inviter_user_id"] = inviterUserID
	}
	var inv Invitation
	err := c.call(ctx, http.MethodPost, "/user_management/invitations", body, &inv, true)
	return inv, err
}

func (c *Client) ListInvitations(ctx context.Context, orgID string) ([]Invitation, error) {
	q := url.Values{"organization_id": {orgID}, "limit": {"100"}}
	var out struct {
		Data []Invitation `json:"data"`
	}
	err := c.call(ctx, http.MethodGet, "/user_management/invitations?"+q.Encode(), nil, &out, true)
	return out.Data, err
}

func (c *Client) RevokeInvitation(ctx context.Context, id string) error {
	return c.call(ctx, http.MethodPost, "/user_management/invitations/"+url.PathEscape(id)+"/revoke", nil, nil, true)
}

// --- plumbing ---

// call sends a JSON request. bearer=true authenticates with the API key
// (the authenticate endpoint takes it as client_secret in the body instead).
func (c *Client) call(ctx context.Context, method, path string, body, out any, bearer bool) error {
	var rd io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return err
		}
		rd = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, method, c.base()+path, rd)
	if err != nil {
		return err
	}
	req.Header.Set("Accept", "application/json")
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if bearer {
		req.Header.Set("Authorization", "Bearer "+c.APIKey)
	}
	hc := c.HTTP
	if hc == nil {
		hc = &http.Client{Timeout: 15 * time.Second}
	}
	resp, err := hc.Do(req)
	if err != nil {
		return fmt.Errorf("workos: %w", err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))

	if resp.StatusCode/100 != 2 {
		var e struct {
			Error        string `json:"error"`
			Code         string `json:"code"`
			Description  string `json:"error_description"`
			Message      string `json:"message"`
			PendingToken string `json:"pending_authentication_token"`
			Orgs         []struct {
				ID string `json:"id"`
			} `json:"organizations"`
		}
		_ = json.Unmarshal(raw, &e)
		werr := &Error{Status: resp.StatusCode, Code: firstNonEmpty(e.Code, e.Error, "request_failed"),
			Description: firstNonEmpty(e.Description, e.Message), pendingToken: e.PendingToken}
		for _, o := range e.Orgs {
			werr.orgIDs = append(werr.orgIDs, o.ID)
		}
		return werr
	}
	if out != nil && len(raw) > 0 {
		if err := json.Unmarshal(raw, out); err != nil {
			return fmt.Errorf("workos: decode %s %s: %w", method, path, err)
		}
	}
	return nil
}

func firstNonEmpty(v ...string) string {
	for _, s := range v {
		if s != "" {
			return s
		}
	}
	return ""
}
